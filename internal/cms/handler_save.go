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
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// maxJSONBodyBytes はJSONを受けるAPIの本文サイズ上限です。
// 上限が無いと認証済み利用者が巨大なボディでメモリを圧迫できる（2026-08-05 監査の指摘）。
// 本文HTMLはテキストなので 8MiB あれば実用上十分（添付の32MiBとは別系統の上限）。
const maxJSONBodyBytes = 8 << 20

// DecodeJSONBody は本文サイズを制限したうえでJSONを読み取ります。
// 上限超過は 413、それ以外の不正は 400 を書いて false を返します。
func DecodeJSONBody(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "リクエスト本文が大きすぎます（上限8MiB）", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return false
	}
	return true
}

// JSONFail は JSON で答えるAPIの失敗応答 {"success": false, "message": …} を書きます。
//
// ⚠ **status に 0 を渡さないこと**（2026-09-14 に受け付けるのをやめました）。
// もとは「状態行は 200 のまま・フロントが本文の `success` を見て分岐する」という
// 旧来の口（PDF解析）との互換のために在りましたが、**13箇所へ広がり**、コアにも
// 入っていました。`res.ok` を見る受け手（app.js に8箇所）からは**失敗が成功に見えます**。
//
// 0 を渡すと Go の `net/http` が 200 を書くので、ここでは弾かずに 500 へ倒します
// ——「黙って成功に見える」より「明らかに壊れている」ほうが直せます。
func JSONFail(w http.ResponseWriter, status int, message string) {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"success": false, "message": message})
}

// PageIDOrFail はページIDを6桁へ畳みます。畳めなければ 400 の JSON を返して false。
//
// 口の前口上の1段目です（2026-09-23 に寄せた——同じ4行がコアと拡張に散っていた）。
// ⚠ **空白は落としません**（`NormalizeID` と同じ）。落として受けたい口は呼ぶ側で。
func PageIDOrFail(w http.ResponseWriter, raw string) (string, bool) {
	pageID, ok := page.NormalizeID(raw)
	if !ok {
		JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return "", false
	}
	return pageID, true
}

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
	if !DecodeJSONBody(w, r, &req) {
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

	// パスとサイドカーに使う前にゼロ詰め6桁へ正規化する（"0012" のような表記揺れで
	// 正本が別ディレクトリへ書かれるのを防ぐ。page.NormalizeID のコメント参照）。
	// 認可・編集ロックはブロック保存と同じ関門を通す（権限とロックの扱いを分岐させない）。
	id, ok := gateSave(w, r, id, req.Token)
	if !ok {
		return
	}

	// 本文は許可リスト方式でサニタイズしてから保存する（docs/本文サニタイズ設計.md）。
	// 正本ファイルを清書された状態に保ち、結果はレスポンスでエディタへ返す。
	// 編集者は画面上の変化で「何が除去されたか」を知る（エコーバック方式）。
	safeHTML, sanitized := SanitizeReport(req.HTML)

	updatedAt, ok := writeBody(w, id, safeHTML)
	if !ok {
		return
	}

	// 版として残す（コアレッシングが効くので、オートセーブの連打では増えない。
	// version.go 参照）。**保存そのものは止めない**——履歴が取れないことを理由に
	// 書けなくするほうが害が大きい（監査記録と同じ判断）。
	if err := RecordVersion(id, auth.UsernameOf(r), safeHTML, false); err != nil {
		log.Printf("版の記録に失敗しました page=%s: %v", id, err)
	}

	if !syncBody(w, id, safeHTML) {
		return
	}
	auth.AuditRequest(r, "save", id)

	WriteJSON(w, saveEcho(id, updatedAt, safeHTML, req.HTML, sanitized))
}

// gateSave は保存APIの入口です: ページIDの正規化 → write 権限 → 編集ロックの検証。
// 全文保存とブロック保存が同じ関門を通ります（権限とロックの扱いを分岐させない）。
//
// 編集ロックのトークン検証は、他者が保持中／自分のトークン失効なら拒否します
// （明け渡し後の古いクライアントが新しい保持者の編集を上書きしないため）。
// ロックが無い場合は許可（無競合。フロント未対応でも従来どおり保存できる）。
func gateSave(w http.ResponseWriter, r *http.Request, rawID, token string) (id string, ok bool) {
	id, _, ok = normalizedPageID(w, rawID)
	if !ok {
		return "", false
	}
	if !page.RequirePageWrite(w, r, id) {
		return "", false
	}
	if !editlock.RequireLockToken(w, r, id, token) {
		return "", false
	}
	return id, true
}

// writeBody は更新日時を進めて本文を正本ファイルへ書きます（失敗は応答に書いて ok=false）。
// 更新日時は保存のたびにサーバーが「今」を刻みます（サイドカーが正本）。
func writeBody(w http.ResponseWriter, id, htmlBody string) (updatedAt string, ok bool) {
	// ⚠ **置き場の題は、トップ直下に1枚だけ**（2026-09-21 ユーザー:「トップ直下に
	//    テンプレートページは**一つだけしかないようにすべき**です」）。
	//    ⚠ **ここで止めます**——保存してから知らせる形だと、**2枚目が既にできています**。
	if why, bad := refuseDuplicateBoxTitle(id, htmlBody); bad {
		http.Error(w, why, http.StatusConflict)
		return "", false
	}
	updatedAt, err := page.BumpUpdatedAt(id)
	if err != nil {
		http.Error(w, "Failed to update metadata", http.StatusInternalServerError)
		return "", false
	}
	pageDir := page.GetPageDir(id)
	os.MkdirAll(pageDir, 0755)
	if err := page.WriteFileAtomic(filepath.Join(pageDir, id+".html"), []byte(htmlBody), 0644); err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return "", false
	}
	return updatedAt, true
}

// refuseDuplicateBoxTitle は「**トップ直下の置き場の題は1枚だけ**」を守ります。
//
// ユーザー（2026-09-21）:「トップ直下にテンプレートページは**一つだけしかないように
// すべき**です」。
//
// ⚠ **それまでは知らせるだけでした**（管理画面の「置き場」が ⚠ を出す）。知らせる形の
// 問題は、**気づいたときには既に2枚ある**ことです——そして**使われるのはいちばん古い
// 1枚だけ**なので、**新しいほうへ書いた内容は誰からも見えないまま残ります**。
//
// ⚠ **止める範囲は「トップ直下」だけ**です。深いところに同じ題のページがあっても
// 構いません（`受注／2026年／09月` の下に「テンプレート」があっても誰も困らない）
// ——特別な意味を持つのは**トップ直下の1枚**だからです。
//
// ⚠ **題は名簿から採ります**（`RequiredPage` の登録）。コアに業務語を書かないための
// 口で、拡張が足した置き場（受注・発注・取引先）にも同じ規則が効きます。
func refuseDuplicateBoxTitle(id, htmlBody string) (string, bool) {
	meta, ok := page.ReadSidecar(id)
	if !ok || meta.ParentID != TopPageID {
		return "", false // トップ直下でなければ関係ない
	}
	root, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		return "", false
	}
	title := strings.TrimSpace(PageTitle(root))
	if !IsRequiredPageTitle(title) {
		return "", false
	}
	for _, other := range TopLevelPagesByTitle(title) {
		if other != id {
			return "「" + title + "」はトップ直下に1枚だけです（既に /" + other +
				" があります）。別の題にするか、そちらを使ってください。", true
		}
	}
	return "", false
}

// syncBody は書いた本文で索引を同期します（失敗は応答に書いて false）。
func syncBody(w http.ResponseWriter, id, html string) bool {
	if err := SyncIndex(id, html); err != nil {
		log.Printf("SyncIndex failed for page %s: %v\n", id, err)
		http.Error(w, "Failed to sync database: "+err.Error(), http.StatusInternalServerError)
		return false
	}
	return true
}

// saveEcho は保存の応答のうち、全文保存とブロック保存で共通の鍵です。
//
//   - html はサニタイズ後の本文。sanitized が true のときエディタは差分ブロックを
//     置き換えて、除去が起きたことを編集者へ通知する。
//   - unknown_types はレジストリ未定義の data-type の**告知**（拒否ではない。語彙モデル §9 の
//     決定: 未知の data-type は通し、保存時に「未定義の種別」と通知する）。
//   - unresolved_fields は見出しの改名で③計算プラグインが読めなくなった列の告知
//     （同じくエコーバックの流儀）。鍵は見出しの表示文字なので、改名は同期を**黙って**止めてしまう。
//   - stripped_ids は殻が独占する接頭辞つきの id を剥がして保存したことの告知
//     （走査は**サニタイズ前**の入力 rawHTML。後では接頭辞が消えていて分からない）。
func saveEcho(id, updatedAt, safeHTML, rawHTML string, sanitized bool) map[string]any {
	return map[string]any{
		"success":           true,
		"page_id":           id,
		"updated_at":        updatedAt,
		"html":              safeHTML,
		"sanitized":         sanitized,
		"unknown_types":     UnknownVocabTypes(safeHTML),
		"unresolved_fields": UnresolvedVocabFields(safeHTML),
		"stripped_ids":      ShellPrefixedIDs(rawHTML),
	}
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
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	if req.PageID == "" || req.BlockID == "" {
		http.Error(w, "page_id と block_id が必要です", http.StatusBadRequest)
		return
	}
	// 全文保存と同じ関門（正規化・認可・ロック検証）を通す。
	id, ok := gateSave(w, r, req.PageID, req.Token)
	if !ok {
		return
	}

	current, err := os.ReadFile(page.BodyPath(id))
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

	updatedAt, ok := writeBody(w, id, merged)
	if !ok {
		return
	}
	if !syncBody(w, id, merged) {
		return
	}
	auth.AuditRequest(r, "save-block", id)

	// html は当該ブロックのサニタイズ後HTML（エコーバックはブロック単位になる）。
	// 告知の対象も送られてきたブロックだけ（エコーバックと同じ粒度）。
	resp := saveEcho(id, updatedAt, safeBlock, req.HTML, sanitized)
	resp["block_id"] = req.BlockID
	WriteJSON(w, resp)
}
