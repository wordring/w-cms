package cms

// ページの木構造に関わるハンドラ。新規作成・子ページ一覧・親の付け替えと、
// その妥当性検証（実在・循環・権限）をまとめています。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// PageSummary は一覧表示用の簡素化されたメタデータ構造体です。
type PageSummary struct {
	ID       string
	Title    string
	FilePath string
	SortKey  string `json:",omitempty"` // 兄弟の中での並び順（サイドカーが正本）
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
	return fmt.Sprintf("%0*d", page.IDLength, idInt), nil
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
	if !page.RequirePageReadOrPublic(w, r, parentID) {
		return
	}
	pages, err := visibleChildren(auth.CurrentUser(r), parentIDInt)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pages)
}

// visibleChildren は親 parentIDInt の子ページのうち、閲覧者 user が見られるものだけを
// 返します。認証済みは read 権限、匿名（user==nil）は実効公開（page.EffectivePublic）で
// 判定する。/api/children と計算ビューのサーバー事前描画（view_render.go）が共用する。
func visibleChildren(user *auth.User, parentIDInt int) ([]PageSummary, error) {
	rows, err := database.DB.Query("SELECT id, title FROM pages WHERE parent_id = ? ORDER BY id ASC", parentIDInt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pages := make([]PageSummary, 0)
	for rows.Next() {
		var p PageSummary
		var idInt int
		if err := rows.Scan(&idInt, &p.Title); err == nil {
			if page.CanView(user, idInt) {
				p.ID = fmt.Sprintf("%0*d", page.IDLength, idInt)
				// 並び順キーはサイドカーが正本です（派生のDBへは持たせない）。
				// 子の数は多くても数百なので、ここで読んで並べれば足ります。
				if meta, ok := page.ReadSidecar(p.ID); ok {
					p.SortKey = meta.SortKey
				}
				pages = append(pages, p)
			}
		}
	}
	sortChildren(pages)
	return pages, nil
}

// sortChildren は子ページを **並び順キー → 題 → ID** で並べます。
//
// ユーザー:「子ページの一覧が良い順番に並んでいないという問題があります」
// （2026-09-03）。それまでは作成順（ID順）で、これは「いつ作ったか」であって
// 「どう並んでほしいか」ではありませんでした——実際、メールを2回に分けて
// 取り込んだだけで `2026年 → 2024年 → 2025年` という並びになりました。
//
// **キーが空でも題で正しく並びます**（`2024年 < 2025年`・`01月 < 09月`）。
// キーを持つものと持たないものが混ざったときは、**キーを持つほうが先**です
// ——人が明示的に位置を決めたものを、既定の並びより後ろへ追いやらないため。
func sortChildren(pages []PageSummary) {
	sort.SliceStable(pages, func(i, j int) bool {
		a, b := pages[i], pages[j]
		if (a.SortKey == "") != (b.SortKey == "") {
			return a.SortKey != "" // キーのあるほうが先
		}
		if a.SortKey != b.SortKey {
			return a.SortKey < b.SortKey
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.ID < b.ID
	})
}

// NewPageAPIHandler はサーバー側で新しいページを作成し、そのページへリダイレクトします。
//
// POST 限定です。CSRFProtect は GET を検証しない（middleware.go）ので、GET のままだと
// 本文へ <img src="/api/new-page?parent=..."> を保存するだけで、そのページを開いた
// 全ログインユーザーにページを作らせられます（同一オリジンなので SameSite も CSP も
// 止めない）。フロントは form の POST ＋ target=_blank で従来どおり別タブに開きます。
// 引数は r.FormValue で読むので、POSTボディでもクエリでも受け取れます。
func NewPageAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// 1. 親ページIDの取得
	parentIDStr := r.FormValue("parent")
	var parentID sql.NullInt64
	if parentIDStr != "" {
		pid, err := strconv.Atoi(parentIDStr)
		if err != nil {
			http.Error(w, "Invalid parent ID", http.StatusBadRequest)
			return
		}
		parentID = sql.NullInt64{Int64: int64(pid), Valid: true}
		// 子ページの作成には親ページの write 権限を要求する
		if !page.RequirePageWrite(w, r, parentIDStr) {
			return
		}
	} else {
		// 親なし（トップレベル）のページが許されるのはトップページ（000000）のみで、
		// それは初回起動時に自動生成済み。新規作成は常に親が必要。
		http.Error(w, "親ページを指定してください（トップページは作成済みのため新規作成できません）", http.StatusBadRequest)
		return
	}
	creator := auth.CurrentUser(r)

	// 1-2. テンプレート指定があれば**IDを採番する前に**検証して本文を読む
	//      （docs/【考察】ページテンプレート.md §4）。採番の後に失敗すると、
	//      ファイルの無いページ行が pages に残ってしまうため順序が重要。
	templateBody, ok := loadTemplateBody(w, r, r.FormValue("template"))
	if !ok {
		return // loadTemplateBody が応答済み
	}

	// 2. ページレコードを原子的にINSERTしてIDを採番する（reserveNewPageID）。
	newID, err := reserveNewPageID(parentID)
	if err != nil {
		http.Error(w, "Failed to create page: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 親ページIDはゼロ詰め文字列に正規化（サイドカーへ記録する）。空＝トップレベル。
	parentStr := ""
	if parentID.Valid {
		parentStr = fmt.Sprintf("%0*d", page.IDLength, parentID.Int64)
	}

	// 3. デフォルトHTMLを構築。HTMLは「内容」のみ（属性はサイドカーが正本）。
	//    子ページ一覧は左サイドパネル（クローム）が担うため、本文には埋め込まない
	//    （必要なら子ページ一覧のビュー section[data-type="child-list"] を後から挿せる）。
	//
	//    テンプレート指定があれば、読んだ本文の空欄を列型の既定値で埋める（新規化パス）。
	//    採番済みの newID を発注書番号に使うので、ここまで来てから行う。
	html := "<h1>新しいページ</h1>\n<p>ここから編集を始めてください。</p>"
	if templateBody != "" {
		html = FreshenTemplateBody(templateBody, newID)
	}

	// 4. HTMLファイルを物理保存
	pageDir := page.GetPageDir(newID)
	os.MkdirAll(pageDir, 0755)
	htmlPath := filepath.Join(pageDir, newID+".html")
	if err := page.WriteFileAtomic(htmlPath, []byte(html), 0644); err != nil {
		http.Error(w, "Failed to write file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 4-2. 属性サイドカーを作成（作成者が所有者＝created_by。親ページIDも記録）。
	//      作成日時・更新日時はサイドカーが刻む。SyncIndexより前に作る。
	//      group / mode は親ページから継承する（setgid 相当・認証認可設計.md 10.4）。
	//      owner は作成者、public は常に false（page.EnsureSidecar は public を設定しない）。
	owner := page.DefaultOwner
	if creator != nil {
		owner = creator.Username
	}
	inheritGroup, inheritMode := "", ""
	if parentID.Valid {
		pp := page.GetPerms(int(parentID.Int64))
		inheritGroup, inheritMode = pp.Group, pp.Mode
	}
	// サイドカーは権限・親の正本。失敗すると作成者が自分のページを触れなくなるため
	// （page.GetPerms が admin 所有の既定へフェイルクローズする）、原因追跡用にログへ残す。
	if err := page.EnsureSidecar(newID, owner, inheritGroup, inheritMode, parentStr); err != nil {
		log.Printf("サイドカーの作成に失敗しました page=%s: %v", newID, err)
	}

	// 5. DB同期（タグなどのインデックス更新。親・作成情報はサイドカーから読まれる）
	if err := SyncIndex(newID, html); err != nil {
		log.Printf("SyncIndex failed for new page %s: %v\n", newID, err)
	}

	// 監査記録（要件定義書 §2.3）。作成は「増えた」ことしか残らない操作なので、
	// 誰がどこへ足したのかをここで残す。テンプレート由来かどうかも手掛かりになる。
	if creator != nil {
		target := newID + " under " + parentStr
		if templateBody != "" {
			target += " (template " + r.FormValue("template") + ")"
		}
		auth.Audit(creator.Username, "new-page", target)
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
	if !page.GetPerms(parentID).CanWrite(user) {
		return "新しい親ページへの書き込み権限がありません", http.StatusForbidden
	}
	return "", 0
}

// ValidateParentAPIHandler は、編集中ページの親ページ変更が妥当かを返します（クライアントの即時検証用）。
// 権威的な検証は保存API側でも行われます。対象ページのwrite権限を前提とします。
func ValidateParentAPIHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !page.RequirePageWrite(w, r, id) {
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
// resyncSubtree はページとその配下すべての索引を本文から同期し直します
// （親の付け替えでテンプレート領域へ出入りしたときに使う）。
func resyncSubtree(rootID string) {
	queue := []string{rootID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if content, err := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html")); err == nil {
			if err := SyncIndex(id, string(content)); err != nil {
				log.Printf("親の付け替え後の再同期に失敗 page=%s: %v", id, err)
			}
		}
		idInt, err := strconv.Atoi(id)
		if err != nil {
			continue
		}
		rows, err := database.DB.Query(`SELECT id FROM pages WHERE parent_id = ?`, idInt)
		if err != nil {
			continue
		}
		for rows.Next() {
			var child int
			if rows.Scan(&child) == nil {
				queue = append(queue, fmt.Sprintf("%0*d", page.IDLength, child))
			}
		}
		rows.Close()
	}
}

func SetParentAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// サイドカーのパスに使うためゼロ詰め6桁へ正規化する（page.NormalizeID 参照）。
	id, okID := page.NormalizeID(r.URL.Query().Get("id"))
	if !okID {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	if !page.RequirePageWrite(w, r, id) {
		return
	}
	// エディタ内の変更操作は本文編集と同じ編集ロックで直列化する（他者保持中なら409）。
	if !editlock.RequireEditLock(w, r, id) {
		return
	}
	newParent := strings.TrimSpace(r.URL.Query().Get("parent"))

	// 付け替えの作法は芯（SetPageParent）が1箇所で持つ——部品ページの整理
	// （ext/sheetmetal/filing.go）も同じ芯を通るので、片方だけ古くなることがない。
	parentStore, updatedAt, err := SetPageParent(auth.CurrentUser(r), id, newParent)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "parent_id": parentStore, "updated_at": updatedAt})
}

// SetPageParent は親の付け替えの**芯**です（検証・サイドカー・索引・配下の同期）。
//
// HTTPの口（SetParentAPIHandler）と、部品ページの整理（ext/sheetmetal/filing.go）が
// 共有します——整理は「解析で生まれたページを顧客名／装置名称の下へ移す」ために
// 同じことをする必要があり、**作法を2箇所に持つと必ず片方が古くなる**ため。
//
// 権限と編集ロックの確認は**呼ぶ側の責任**です（AppendToPageBody と同じ線引き。
// エディタからの操作はロックが要り、整理は作った直後のページを動かすので要らない）。
func SetPageParent(user *auth.User, id, newParent string) (parentStore, updatedAt string, err error) {
	childID, err := strconv.Atoi(id)
	if err != nil {
		return "", "", errors.New("ページIDが不正です")
	}
	var oldParent sql.NullInt64
	database.DB.QueryRow("SELECT parent_id FROM pages WHERE id = ?", childID).Scan(&oldParent)
	if parentChanged(oldParent, newParent) {
		if msg, code := validateParentChange(user, childID, newParent); code != 0 {
			return "", "", errors.New(msg)
		}
	}

	if newParent != "" {
		if pid, e := strconv.Atoi(newParent); e == nil {
			parentStore = fmt.Sprintf("%0*d", page.IDLength, pid)
		}
	}
	updatedAt, err = page.SetSidecarParent(id, parentStore)
	if err != nil {
		return "", "", errors.New("親ページの保存に失敗しました: " + err.Error())
	}
	var parentDB sql.NullInt64
	if parentStore != "" {
		if pid, e := strconv.Atoi(parentStore); e == nil {
			parentDB = sql.NullInt64{Int64: int64(pid), Valid: true}
		}
	}
	if _, err := database.DB.Exec("UPDATE pages SET parent_id = ?, updated_at = ? WHERE id = ?",
		parentDB, updatedAt, childID); err != nil {
		return "", "", errors.New("インデックスの更新に失敗しました: " + err.Error())
	}
	// 親が変わると、テンプレート領域への出入り（②索引・③計算に載るか）も変わる。
	// 判定は先祖辿り（サイドカー）なので、動かしたページとその配下を同期し直す。
	if parentChanged(oldParent, newParent) {
		resyncSubtree(id)
	}
	if user != nil {
		auth.Audit(user.Username, "set-parent", id+"->"+parentStore)
	}
	return parentStore, updatedAt, nil
}
