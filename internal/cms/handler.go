package cms

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/database"
)

// PageSummary は一覧表示用の簡素化されたメタデータ構造体です。
type PageSummary struct {
	ID       string
	Title    string
	FilePath string
}

// SaveRequest はオートセーブで送られてくるJSON構造体です。
type SaveRequest struct {
	PageID string `json:"page_id"`
	HTML   string `json:"html"`
	Token  string `json:"token"` // 編集ロックのトークン（保持者の検証に使う）
}

// reserveNewPageID は pages テーブルへ最小限の行を原子的に INSERT し、SQLite の自動採番で
// 確定した新しいページID（6桁ゼロ埋め）を返します。`MAX(id)+1` と異なり同時実行でも一意な
// IDが得られ、ID衝突（[docs/【考察】同時編集の競合対策.md] シナリオE）を防ぎます。
// parent は親ページID（トップレベルは無効値 sql.NullInt64{}）。属性の正本はサイドカーです。
func reserveNewPageID(parent sql.NullInt64) (string, error) {
	result, err := database.DB.Exec(
		`INSERT INTO pages (title, parent_id, file_path) VALUES (?, ?, '')`,
		"新しいページ", parent,
	)
	if err != nil {
		return "", err
	}
	idInt, _ := result.LastInsertId()
	return fmt.Sprintf("%0*d", IDLength, idInt), nil
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
		// 新規ページ：IDを原子的に採番（同時保存でも衝突しない）。作成者を所有者として
		// サイドカーを作成する（作成日時・作成者・更新日時はサイドカーが刻む）。
		var err error
		id, err = reserveNewPageID(sql.NullInt64{})
		if err != nil {
			http.Error(w, "Failed to create page: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// サイドカーは権限の正本。書けなかった場合、GetPerms は admin 所有の既定値へ
		// フォールバックする（フェイルクローズ）ため作成者が自分のページを触れなくなる。
		// 黙って壊れると原因が追えないのでログに残す。
		var sidecarErr error
		if u := auth.CurrentUser(r); u != nil {
			sidecarErr = EnsureSidecar(id, u.Username, u.PrimaryGroup, "", "")
		} else {
			sidecarErr = EnsureSidecar(id, defaultOwner, "", "", "")
		}
		if sidecarErr != nil {
			log.Printf("サイドカーの作成に失敗しました page=%s: %v", id, sidecarErr)
		}
	} else {
		// 既存ページ：write権限を要求する
		if !RequirePageWrite(w, r, id) {
			return
		}
		// 編集ロックのトークン検証：他者が保持中／自分のトークン失効なら拒否する
		// （明け渡し後の古いクライアントが新しい保持者の編集を上書きしないため）。
		// ロックが無い場合は許可（無競合。フロント未対応でも従来どおり保存できる）。
		if idInt, e := strconv.Atoi(id); e == nil {
			if u := auth.CurrentUser(r); u != nil && !pageLocks.Validate(idInt, u.Username, req.Token) {
				http.Error(w, "編集権がありません（他の人に移ったか期限切れです）。変更を退避して再読込してください。", http.StatusConflict)
				return
			}
		}
	}

	// 更新日時は保存のたびにサーバーが「今」を刻む（サイドカーが正本）。
	updatedAt, err := BumpUpdatedAt(id)
	if err != nil {
		http.Error(w, "Failed to update metadata", http.StatusInternalServerError)
		return
	}

	// 本文は許可リスト方式でサニタイズしてから保存する（docs/本文サニタイズ設計.md）。
	// 正本ファイルを清書された状態に保ち、結果はレスポンスでエディタへ返す。
	// 編集者は画面上の変化で「何が除去されたか」を知る（エコーバック方式）。
	safeHTML, sanitized := SanitizeReport(req.HTML)

	pageDir := GetPageDir(id)
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
	if !RequirePageWrite(w, r, req.PageID) {
		return
	}
	idInt, err := strconv.Atoi(req.PageID)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	if u := auth.CurrentUser(r); u != nil && !pageLocks.Validate(idInt, u.Username, req.Token) {
		http.Error(w, "編集権がありません（他の人に移ったか期限切れです）。変更を退避して再読込してください。", http.StatusConflict)
		return
	}

	htmlPath := filepath.Join(GetPageDir(req.PageID), req.PageID+".html")
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

	updatedAt, err := BumpUpdatedAt(req.PageID)
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
	})
}

// LoadAPIHandler は指定されたpage_idのHTMLファイルを読み込んで返却します。
func LoadAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Missing id", http.StatusBadRequest)
		return
	}
	// ページ本文の取得は read 権限を要求する（匿名でも実効公開なら閲覧可）。
	if !RequirePageReadOrPublic(w, r, id) {
		return
	}

	idInt, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "Invalid id format", http.StatusBadRequest)
		return
	}

	var filePath string
	err = database.DB.QueryRow("SELECT file_path FROM pages WHERE id = ?", idInt).Scan(&filePath)
	if err != nil {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

// TagSchemaAPIHandler は、登録済み全プラグインが宣言したカスタム要素の属性契約を返します。
//
// エディタはこれを使って本文をシリアライズします（要素ごとの手書き分岐を持たないため、
// プラグインを追加しただけで新しい要素が正しく保存されるようになる）。サーバー側の
// サニタイズ許可リストも同じ PluginTags() から作られるので、**保存する属性と許可する属性が
// 食い違いようがない**——過去に属性の宣言漏れで値が静かに消える不具合があったための設計。
//
// プラグイン側に個別の実装は不要で、Tags() を宣言すれば自動的にここへ現れます。
// 語彙は秘密情報ではないため認証不要（匿名の閲覧でもエディタの初期化は走る）。
func TagSchemaAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// {"elements": {"m-item": ["cost", "item-id", ...], ...}}
	elements := make(map[string][]string, 8)
	for _, spec := range PluginTags() {
		attrs := spec.Attributes
		if attrs == nil {
			attrs = []string{}
		}
		elements[spec.Element] = attrs
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"elements": elements})
}

// ChildPagesAPIHandler は指定された親ページIDを持つ子ページの一覧を返します。
func ChildPagesAPIHandler(w http.ResponseWriter, r *http.Request) {
	parentID := r.URL.Query().Get("parent_id")
	if parentID == "" {
		http.Error(w, "Missing parent_id", http.StatusBadRequest)
		return
	}

	parentIDInt, err := strconv.Atoi(parentID)
	if err != nil {
		http.Error(w, "Invalid parent_id format", http.StatusBadRequest)
		return
	}
	// 一覧表示には親ページの read 権限を要求する（Unixの「ディレクトリの読み取り」に相当）。
	// 匿名でも親が実効公開なら許可する（認証認可設計.md 10.5）。
	if !RequirePageReadOrPublic(w, r, parentID) {
		return
	}
	user := auth.CurrentUser(r)

	rows, err := database.DB.Query("SELECT id, title FROM pages WHERE parent_id = ? ORDER BY id ASC", parentIDInt)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// 各子ページのうち、閲覧者が見られるものだけを返す。
	// 認証済みは read 権限、匿名は実効公開（EffectivePublic）で判定する。
	pages := make([]PageSummary, 0)
	for rows.Next() {
		var p PageSummary
		var idInt int
		if err := rows.Scan(&idInt, &p.Title); err == nil {
			visible := false
			if user != nil {
				visible = GetPerms(idInt).CanRead(user)
			} else {
				visible = EffectivePublic(idInt)
			}
			if visible {
				p.ID = fmt.Sprintf("%0*d", IDLength, idInt)
				pages = append(pages, p)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pages)
}

// NewPageAPIHandler はサーバー側で新しいページを作成し、そのページへリダイレクトします。
func NewPageAPIHandler(w http.ResponseWriter, r *http.Request) {
	// 1. 親ページIDの取得
	parentIDStr := r.URL.Query().Get("parent")
	var parentID sql.NullInt64
	if parentIDStr != "" {
		pid, err := strconv.Atoi(parentIDStr)
		if err != nil {
			http.Error(w, "Invalid parent ID", http.StatusBadRequest)
			return
		}
		parentID = sql.NullInt64{Int64: int64(pid), Valid: true}
		// 子ページの作成には親ページの write 権限を要求する
		if !RequirePageWrite(w, r, parentIDStr) {
			return
		}
	} else {
		// 親なし（トップレベル）のページが許されるのはトップページ（000000）のみで、
		// それは初回起動時に自動生成済み。新規作成は常に親が必要。
		http.Error(w, "親ページを指定してください（トップページは作成済みのため新規作成できません）", http.StatusBadRequest)
		return
	}
	creator := auth.CurrentUser(r)

	// 2. ページレコードを原子的にINSERTしてIDを採番する（reserveNewPageID）。
	newID, err := reserveNewPageID(parentID)
	if err != nil {
		http.Error(w, "Failed to create page: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 親ページIDはゼロ詰め文字列に正規化（サイドカーへ記録する）。空＝トップレベル。
	parentStr := ""
	if parentID.Valid {
		parentStr = fmt.Sprintf("%0*d", IDLength, parentID.Int64)
	}

	// 3. デフォルトHTMLを構築。HTMLは「内容」のみ（属性はサイドカーが正本）。
	//    子ページ一覧は左サイドパネル（クローム）が担うため、本文には埋め込まない
	//    （必要なら <m-child-list> を本文に手動で追加できる）。
	var htmlBuilder strings.Builder
	htmlBuilder.WriteString("<h1>新しいページ</h1>\n")
	htmlBuilder.WriteString("<p>ここから編集を始めてください。</p>")
	html := htmlBuilder.String()

	// 4. HTMLファイルを物理保存
	pageDir := GetPageDir(newID)
	os.MkdirAll(pageDir, 0755)
	htmlPath := filepath.Join(pageDir, newID+".html")
	if err := os.WriteFile(htmlPath, []byte(html), 0644); err != nil {
		http.Error(w, "Failed to write file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 4-2. 属性サイドカーを作成（作成者が所有者＝created_by。親ページIDも記録）。
	//      作成日時・更新日時はサイドカーが刻む。SyncIndexより前に作る。
	//      group / mode は親ページから継承する（setgid 相当・認証認可設計.md 10.4）。
	//      owner は作成者、public は常に false（EnsureSidecar は public を設定しない）。
	owner := defaultOwner
	if creator != nil {
		owner = creator.Username
	}
	inheritGroup, inheritMode := "", ""
	if parentID.Valid {
		pp := GetPerms(int(parentID.Int64))
		inheritGroup, inheritMode = pp.Group, pp.Mode
	}
	// サイドカーは権限・親の正本。失敗すると作成者が自分のページを触れなくなるため
	// （GetPerms が admin 所有の既定へフェイルクローズする）、原因追跡用にログへ残す。
	if err := EnsureSidecar(newID, owner, inheritGroup, inheritMode, parentStr); err != nil {
		log.Printf("サイドカーの作成に失敗しました page=%s: %v", newID, err)
	}

	// 5. DB同期（タグなどのインデックス更新。親・作成情報はサイドカーから読まれる）
	if err := SyncIndex(newID, html); err != nil {
		log.Printf("SyncIndex failed for new page %s: %v\n", newID, err)
	}

	// 6. 新しいページへリダイレクト
	http.Redirect(w, r, "/"+newID+"?edit=true", http.StatusFound)
}

// parentChanged は、DB上の現在の親（old）と新しい親文字列（newStr）が異なるかを返します。
// 親が変わらない通常の保存では検証をスキップするための判定です。
func parentChanged(old sql.NullInt64, newStr string) bool {
	newNorm := -1 // 親なし
	if newStr != "" {
		if v, err := strconv.Atoi(newStr); err == nil {
			newNorm = v
		} else {
			newNorm = -2 // 数値でない不正値。必ず検証を走らせて弾く
		}
	}
	oldNorm := -1
	if old.Valid {
		oldNorm = int(old.Int64)
	}
	return newNorm != oldNorm
}

// parentCreatesCycle は、ページ childID の親を newParentID にすると木に循環が生じるかを返します。
// newParentID から parent_id チェーンを上にたどり、childID に到達すれば循環です。
// 既存データの破損による無限ループに備え、探索回数に上限を設けます。
func parentCreatesCycle(childID, newParentID int) bool {
	cur := newParentID
	for i := 0; i < 10000; i++ {
		if cur == childID {
			return true
		}
		var parent sql.NullInt64
		if err := database.DB.QueryRow("SELECT parent_id FROM pages WHERE id = ?", cur).Scan(&parent); err != nil || !parent.Valid {
			return false
		}
		cur = int(parent.Int64)
	}
	return false
}

// validateParentChange は、ページ childID の親を newParentStr に変更する操作の妥当性を検証します。
// 呼び出し側は「親が実際に変わるとき」だけ呼ぶこと（不変の保存では検証しない）。
// 妥当なら ("", 0)、不正なら (ユーザー向けメッセージ, HTTPステータス) を返します。
//
// ルール（子ページ作成 NewPageAPIHandler と同じポリシー）:
//   - 親を空（トップレベル）にするには admin 権限が必要
//   - 親IDは数値かつ実在するページであること
//   - 自分自身や自分の子孫を親に指定できない（循環防止）
//   - 新しい親ページへの write 権限が必要
func validateParentChange(user *auth.User, childID int, newParentStr string) (string, int) {
	if user == nil {
		return "認証が必要です", http.StatusUnauthorized
	}
	if newParentStr == "" {
		// 親なし（トップレベル）が許されるのはトップページ（ID 0 ＝ "000000"）のみ。
		if childID != 0 {
			return "親なし（トップレベル）にできるのはトップページのみです", http.StatusForbidden
		}
		return "", 0
	}
	parentID, err := strconv.Atoi(newParentStr)
	if err != nil {
		return "親ページIDが不正です", http.StatusBadRequest
	}
	var exists bool
	database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM pages WHERE id = ?)", parentID).Scan(&exists)
	if !exists {
		return "指定された親ページが存在しません", http.StatusBadRequest
	}
	if parentID == childID {
		return "自分自身を親ページに指定できません", http.StatusBadRequest
	}
	if parentCreatesCycle(childID, parentID) {
		return "自分の子孫ページを親に指定できません（循環します）", http.StatusBadRequest
	}
	if !GetPerms(parentID).CanWrite(user) {
		return "新しい親ページへの書き込み権限がありません", http.StatusForbidden
	}
	return "", 0
}

// ValidateParentAPIHandler は、編集中ページの親ページ変更が妥当かを返します（クライアントの即時検証用）。
// 権威的な検証は保存API側でも行われます。対象ページのwrite権限を前提とします。
func ValidateParentAPIHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !RequirePageWrite(w, r, id) {
		return
	}
	childID, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	newParent := strings.TrimSpace(r.URL.Query().Get("parent"))

	w.Header().Set("Content-Type", "application/json")
	if msg, code := validateParentChange(auth.CurrentUser(r), childID, newParent); code != 0 {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": msg})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// SetParentAPIHandler は編集中ページの親ページを付け替えます（親はサイドカーが正本）。
// 対象ページの write 権限に加え、変更先の妥当性（実在・循環・新しい親への write、
// トップレベル化は admin）を検証してからサイドカーへ反映し、pages インデックスを更新します。
func SetParentAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	if !RequirePageWrite(w, r, id) {
		return
	}
	// エディタ内の変更操作は本文編集と同じ編集ロックで直列化する（他者保持中なら409）。
	if !RequireEditLock(w, r, id) {
		return
	}
	childID, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	newParent := strings.TrimSpace(r.URL.Query().Get("parent"))

	// 親が実際に変わるときだけ検証する。
	var oldParent sql.NullInt64
	database.DB.QueryRow("SELECT parent_id FROM pages WHERE id = ?", childID).Scan(&oldParent)
	if parentChanged(oldParent, newParent) {
		if msg, code := validateParentChange(auth.CurrentUser(r), childID, newParent); code != 0 {
			http.Error(w, msg, code)
			return
		}
	}

	// 親IDをゼロ詰めに正規化してサイドカーへ反映（更新日時も進む）。
	parentStore := ""
	if newParent != "" {
		if pid, e := strconv.Atoi(newParent); e == nil {
			parentStore = fmt.Sprintf("%0*d", IDLength, pid)
		}
	}
	updatedAt, err := SetSidecarParent(id, parentStore)
	if err != nil {
		http.Error(w, "親ページの保存に失敗しました: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// pages インデックスの親ページ・更新日時を更新する（本文の再同期は不要）。
	var parentDB sql.NullInt64
	if parentStore != "" {
		if pid, e := strconv.Atoi(parentStore); e == nil {
			parentDB = sql.NullInt64{Int64: int64(pid), Valid: true}
		}
	}
	if _, err := database.DB.Exec("UPDATE pages SET parent_id = ?, updated_at = ? WHERE id = ?", parentDB, updatedAt, childID); err != nil {
		http.Error(w, "インデックスの更新に失敗しました: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if u := auth.CurrentUser(r); u != nil {
		auth.Audit(u.Username, "set-parent", id+"->"+parentStore)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "parent_id": parentStore, "updated_at": updatedAt})
}

// PageMetaAPIHandler はサイドパネル表示用に、ページの属性（親・作成/更新情報）を返します。
// 対象ページの read 権限を要求しますが、匿名でも実効公開（EffectivePublic）なら許可します
// （子ナビと同様の扱い。認証認可設計.md 10.5）。
func PageMetaAPIHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !RequirePageReadOrPublic(w, r, id) {
		return
	}
	idInt, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}

	var parent sql.NullInt64
	var createdAt, createdBy, updatedAt sql.NullString
	err = database.DB.QueryRow(
		"SELECT parent_id, created_at, created_by, updated_at FROM pages WHERE id = ?", idInt,
	).Scan(&parent, &createdAt, &createdBy, &updatedAt)
	if err != nil {
		http.Error(w, "ページが見つかりません", http.StatusNotFound)
		return
	}

	parentStr := ""
	if parent.Valid {
		parentStr = fmt.Sprintf("%0*d", IDLength, parent.Int64)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":         id,
		"parent_id":  parentStr,
		"created_at": createdAt.String,
		"created_by": createdBy.String,
		"updated_at": updatedAt.String,
	})
}

// RebuildDBAPIHandler は、HTMLファイルからデータベースを完全に再構築します。
func RebuildDBAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// 全再構築は admin のみ
	if !RequireAdmin(w, r) {
		return
	}

	err := RebuildDatabase()
	if err != nil {
		http.Error(w, "Rebuild error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// RootHandler はWiki型のルーティングを担当します。
func RootHandler(w http.ResponseWriter, r *http.Request) {
	// `/assets/` などの静的ファイルは既に mux で処理されている前提
	id := r.URL.Path[1:] // 先頭の `/` を取り除く

	// トップページの正規URLは /000000 の1つに統一する（同一文書が複数の名前を
	// 持たないように）。`/` や `/index.html` はエイリアスとして /000000 へ
	// リダイレクトする。
	if id == "" || id == "index.html" {
		target := "/000000"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}

	// 初回起動時の 000000 ページ自動生成
	if id == "000000" {
		var exists bool
		database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM pages WHERE id = 0)").Scan(&exists)
		if !exists {
			defaultHTML := `<h1>w-cms Wiki トップページ</h1>
<p>ここはすべての起点となるトップページです。</p>
<p>右上のスイッチで「編集モード」に切り替えると、Notionのようにブロックベースで編集できます。子ページは左のサイドパネルから辿れます。</p>`

			pageDir := GetPageDir("000000")
			os.MkdirAll(pageDir, 0755)
			htmlPath := filepath.Join(pageDir, "000000.html")
			os.WriteFile(htmlPath, []byte(defaultHTML), 0644)
			// トップページは全員が閲覧できるよう other に read を付与（owner rw / other r）。
			// 書き込みは admin（owner）のみ。
			if err := WriteSidecar("000000", PageMeta{Owner: defaultOwner, Mode: "302"}); err != nil {
				log.Printf("トップページのサイドカー作成に失敗しました: %v", err)
			}
			if err := SyncIndex("000000", defaultHTML); err != nil {
				log.Printf("トップページの同期に失敗しました: %v", err)
			}
		}
	}

	// id が英数字ハイフンのみか簡易チェック
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
			http.NotFound(w, r)
			return
		}
	}

	// ページの実体（本文・タイトル）を取り出し、認可のうえで殻へ埋め込んで返す。
	pageID, err := strconv.Atoi(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var filePath, title string
	err = database.DB.QueryRow(
		"SELECT file_path, COALESCE(title, '') FROM pages WHERE id = ?", pageID,
	).Scan(&filePath, &title)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// 画面の認可。API（401を返す RequirePageReadOrPublic）とは扱いを変え、匿名は
	// ログイン画面へ誘導する（RequireAuth の「APIは401・画面は/login」に合わせる）。
	if !requirePageViewable(w, r, pageID) {
		return
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// 保存経路を通っていない本文（既存データ・バックアップ復元・手動配置）に備え、
	// 描画時にもサニタイズする（docs/本文サニタイズ設計.md の二層目）。
	page, err := RenderPageShell(Sanitize(string(content)), title)
	if err != nil {
		http.Error(w, "ページの生成に失敗しました", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 認可結果に依存する内容なのでキャッシュさせない。
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(page))
}

// requirePageViewable は画面表示のための read 認可を行います。
// 認証済みで権限が無ければ403、匿名で実効公開でなければ /login へリダイレクトします。
func requirePageViewable(w http.ResponseWriter, r *http.Request, pageID int) bool {
	if u := auth.CurrentUser(r); u != nil {
		if !GetPerms(pageID).CanRead(u) {
			http.Error(w, "このページを閲覧する権限がありません", http.StatusForbidden)
			return false
		}
		return true
	}
	if EffectivePublic(pageID) {
		return true
	}
	http.Redirect(w, r, "/login", http.StatusFound)
	return false
}
