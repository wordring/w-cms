package cms

// 本文の保存API（全文保存とブロック単位保存）。認可 → 編集ロック検証 → サニタイズ →
// 正本ファイルの書き込み → インデックス同期、という順序はどちらの経路でも同じです。

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// SaveRequest はオートセーブで送られてくるJSON構造体です。
type SaveRequest struct {
	PageID string `json:"page_id"`
	HTML   string `json:"html"`
	Token  string `json:"token"` // 編集ロックのトークン（保持者の検証に使う）
}

// SaveAPIHandler はエディタからの自動保存（JSON）を受け取り、HTMLファイルとDB同期を上書き保存します。
func SaveAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// 本文（HTML）はそのまま保存し、ページ属性はサイドカーが正本。保存では
	// 更新日時だけを進める（作成日時・作成者・親・権限はサイドカーが保持）。
	// 親ページIDの変更は専用API（SetParentAPIHandler）が担い、保存では扱わない。
	id := req.PageID
	if id == "" {
		// 空の page_id での保存は許可しない。
		//
		// かつてこの経路は reserveNewPageID(NULL) で**親なし（トップレベル）**のページを
		// 作っていたが、それは「親なしにできるのはトップページ 000000 のみ」という
		// ポリシー（NewPageAPIHandler が400で拒否し、validateParentChange も admin に
		// 許さない）を迂回する穴だった。新規作成は必ず親を指定する /api/new-page を通す。
		// フロントは常に currentPageId を送る（空なら 000000 へ落とす）ので実害はない。
		http.Error(w, "ページIDが指定されていません。新規ページは /api/new-page で作成してください。", http.StatusBadRequest)
		return
	}

	// write権限を要求する
	if !page.RequirePageWrite(w, r, id) {
		return
	}
	// 編集ロックのトークン検証：他者が保持中／自分のトークン失効なら拒否する
	// （明け渡し後の古いクライアントが新しい保持者の編集を上書きしないため）。
	// ロックが無い場合は許可（無競合。フロント未対応でも従来どおり保存できる）。
	if idInt, e := strconv.Atoi(id); e == nil {
		if u := auth.CurrentUser(r); u != nil && !editlock.Locks.Validate(idInt, u.Username, req.Token) {
			http.Error(w, "編集権がありません（他の人に移ったか期限切れです）。変更を退避して再読込してください。", http.StatusConflict)
			return
		}
	}

	// 更新日時は保存のたびにサーバーが「今」を刻む（サイドカーが正本）。
	updatedAt, err := page.BumpUpdatedAt(id)
	if err != nil {
		http.Error(w, "Failed to update metadata", http.StatusInternalServerError)
		return
	}

	// 本文は許可リスト方式でサニタイズしてから保存する（docs/本文サニタイズ設計.md）。
	// 正本ファイルを清書された状態に保ち、結果はレスポンスでエディタへ返す。
	// 編集者は画面上の変化で「何が除去されたか」を知る（エコーバック方式）。
	safeHTML, sanitized := SanitizeReport(req.HTML)

	pageDir := page.GetPageDir(id)
	os.MkdirAll(pageDir, 0755)

	htmlPath := filepath.Join(pageDir, id+".html")
	if err := os.WriteFile(htmlPath, []byte(safeHTML), 0644); err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	if err := SyncIndex(id, safeHTML); err != nil {
		log.Printf("SyncIndex failed for page %s: %v\n", id, err)
		http.Error(w, "Failed to sync database: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if u := auth.CurrentUser(r); u != nil {
		auth.Audit(u.Username, "save", id)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"page_id":    id,
		"updated_at": updatedAt,
		// html はサニタイズ後の本文。sanitized が true のときエディタは差分ブロックを
		// 置き換えて、除去が起きたことを編集者へ通知する。
		"html":      safeHTML,
		"sanitized": sanitized,
		// レジストリ未定義の data-type の**告知**（拒否ではない。語彙モデル §9 の決定:
		// 未知の data-type は通し、保存時に「未定義の種別」と通知する）。
		"unknown_types": UnknownVocabTypes(safeHTML),
	})
}

// SaveBlockRequest は1ブロックだけを更新する保存リクエストです。
type SaveBlockRequest struct {
	PageID  string `json:"page_id"`
	BlockID string `json:"block_id"`
	HTML    string `json:"html"`  // 当該ブロックのHTML（そのブロック1つ分）
	Token   string `json:"token"` // 編集ロックのトークン
}

// SaveBlockAPIHandler は本文のうち1ブロックだけを差し替えて保存します。
//
// ブロックは `data-id` で識別しますが、**この属性は任意**です。IDが無い本文
// （手書きHTML等）や、追加・削除・並べ替えのような構造変更では使えないため、
// その場合はクライアントが従来の全文保存（SaveAPIHandler）へフォールバックします。
// 対象が見つからない・重複する場合は **409** を返し、同じくフォールバックさせます。
//
// 正本はファイル単位で書き直し、SyncIndex もページ単位の洗い替えのままなので、
// これで減るのは送信量とエコーバックの粒度です。
func SaveBlockAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SaveBlockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.PageID == "" || req.BlockID == "" {
		http.Error(w, "page_id と block_id が必要です", http.StatusBadRequest)
		return
	}

	// 全文保存と同じ認可・ロック検証を通す（権限とロックの扱いを分岐させない）。
	if !page.RequirePageWrite(w, r, req.PageID) {
		return
	}
	idInt, err := strconv.Atoi(req.PageID)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	if u := auth.CurrentUser(r); u != nil && !editlock.Locks.Validate(idInt, u.Username, req.Token) {
		http.Error(w, "編集権がありません（他の人に移ったか期限切れです）。変更を退避して再読込してください。", http.StatusConflict)
		return
	}

	htmlPath := filepath.Join(page.GetPageDir(req.PageID), req.PageID+".html")
	current, err := os.ReadFile(htmlPath)
	if err != nil {
		http.Error(w, "本文を読み込めませんでした", http.StatusNotFound)
		return
	}

	// ブロック単体をサニタイズしてから差し込む（全文と同じ許可リスト）。
	safeBlock, sanitized := SanitizeReport(req.HTML)

	merged, err := ReplaceBlock(string(current), req.BlockID, safeBlock)
	if err != nil {
		// 見つからない／重複 → クライアントは全文保存へフォールバックする
		if errors.Is(err, ErrBlockNotFound) || errors.Is(err, ErrBlockAmbiguous) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, "本文の更新に失敗しました", http.StatusInternalServerError)
		return
	}

	updatedAt, err := page.BumpUpdatedAt(req.PageID)
	if err != nil {
		http.Error(w, "Failed to update metadata", http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(htmlPath, []byte(merged), 0644); err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}
	if err := SyncIndex(req.PageID, merged); err != nil {
		log.Printf("SyncIndex failed for page %s: %v\n", req.PageID, err)
		http.Error(w, "Failed to sync database: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if u := auth.CurrentUser(r); u != nil {
		auth.Audit(u.Username, "save-block", req.PageID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"page_id":    req.PageID,
		"block_id":   req.BlockID,
		"updated_at": updatedAt,
		// html は当該ブロックのサニタイズ後HTML（エコーバックはブロック単位になる）。
		"html":      safeBlock,
		"sanitized": sanitized,
		// 告知の対象は送られてきたブロックだけ（エコーバックと同じ粒度）。
		"unknown_types": UnknownVocabTypes(safeBlock),
	})
}
