package cms

// ページの木構造に関わるハンドラ。新規作成・子ページ一覧・親の付け替えと、
// その妥当性検証（実在・循環・権限）をまとめています。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
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
// IDが得られ、ID衝突（[docs/考察/【考察】同時編集の競合対策.md] シナリオE）を防ぎます。
// parent は親ページID（トップレベルは無効値 sql.NullInt64{}）。属性の正本はサイドカーです。
func reserveNewPageID(parent sql.NullInt64) (string, error) {
	// DB の作り直しが表を消しているあいだは待つ（2026-10-30。保存の索引づけと同じ関門・その一瞬に作ると 500 だった）。
	rebuildGate.RLock()
	defer rebuildGate.RUnlock()
	// 番号は、いまのページとごみ箱のページのどちらよりも大きくする（page_id_floor.go——消したページの番号を使い回さない）。
	// 1つの文の中で数えて入れるので、同時に作っても重ならない（書き込みは SQLite が1つずつ通す）。
	floor := trashHighWater()
	for try := 0; try < 3; try++ {
		result, err := database.DB.Exec(
			`INSERT INTO pages (id, title, parent_id, file_path) VALUES ((SELECT MAX(COALESCE((SELECT MAX(id) FROM pages), 0), ?) + 1), ?, ?, '')`,
			floor, "新しいページ", parent,
		)
		if err != nil {
			if try == 0 && strings.Contains(err.Error(), "UNIQUE") {
				floor = masterHighWater() // DB の作り直しが同じ番号を入れた——ディスクの番号の上から取り直す
				continue
			}
			return "", err
		}
		idInt64, _ := result.LastInsertId()
		id := page.FormatID(int(idInt64))
		// ⚠ 索引（cms.db）は作り直しの途中や、索引に入れられなかったページがあると、ディスクのページより少ない。
		// その番号のフォルダがもうディスクに在れば、いまあるページなので使わない——使うと、そのページの本文を
		// 新しいページで上書きしていた（2026-10-26）。入れた行を戻し、ディスクのいちばん大きい番号の上から取り直す。
		if _, statErr := os.Stat(page.GetPageDir(id)); statErr == nil {
			database.DB.Exec(`DELETE FROM pages WHERE id = ? AND file_path = '' AND title = ?`, idInt64, "新しいページ")
			if m := masterHighWater(); m > floor {
				floor = m
			} else {
				floor = int(idInt64)
			}
			continue
		}
		return id, nil
	}
	return "", errors.New("新しいページの番号を決められません（ディスクのページと重なります）")
}

// forgetReservedPage は、番号を取ったのに本文を書けなかったページの行を消します（本文の無い行を残さない・2026-10-30）。
func forgetReservedPage(id string) {
	if n, err := strconv.Atoi(id); err == nil {
		database.DB.Exec(`DELETE FROM pages WHERE id = ? AND file_path = ''`, n)
	}
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
				p.ID = page.FormatID(idInt)
				// 並び順キーはサイドカーが正本です（派生のDBへは持たせない）。
				// 子の数は多くても数百なので、ここで読んで並べれば足ります。
				if meta, ok := page.ReadSidecar(p.ID); ok {
					p.SortKey = meta.SortKey
				}
				pages = append(pages, p)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
	//      （docs/考察/【考察】ページテンプレート.md §4）。採番の後に失敗すると、
	//      ファイルの無いページ行が pages に残ってしまうため順序が重要。
	templateBody, ok := loadTemplateBody(w, r, r.FormValue("template"))
	if !ok {
		return // loadTemplateBody が応答済み
	}

	// 3. デフォルトHTMLを構築。HTMLは「内容」のみ（属性はサイドカーが正本）。
	//    子ページ一覧は左サイドパネル（クローム）が担うため、本文には埋め込まない
	//    （必要なら子ページ一覧のビュー <section><h2>子ページ一覧</h2></section> を後から挿せる）。
	//
	//    テンプレート指定があれば、読んだ本文を**そのまま写す**（ブロックIDだけ外す）。
	//    ⚠ 2026-09-25 まで空欄を列型の既定値で埋めていた（新規化）——いまは純粋なコピー
	//    （template_new.go に経緯）。
	//
	//    ⚠ 採番より前に組みます（2026-10-27）——作るときに決める語（書類の種類など・fixed_tag.go）の値をここで入れて確かめ、
	//    断るときにファイルの無いページ行を残さないため（上の 1-2 と同じ理由）。
	html := "<h1>新しいページ</h1>\n<p>ここから編集を始めてください。</p>"
	if templateBody != "" {
		html = CopyTemplateBody(templateBody)
	}
	html, why, ok := fixedTagsOfNewPage(creator, html, r.FormValue)
	if !ok {
		http.Error(w, why, http.StatusForbidden)
		return
	}

	// トップ直下の置き場の題は1枚だけ（2026-10-30）。作り終えるまで、置き場を作るほかの口を待たせる。
	if parentID.Int64 == 0 {
		folderMu.Lock()
		defer folderMu.Unlock()
		if why, bad := boxTitleRefusal("", html); bad {
			http.Error(w, why, http.StatusConflict)
			return
		}
	}

	// 2. ページレコードを原子的にINSERTしてIDを採番する（reserveNewPageID）。
	// 親が在って消されかけていないことを確かめてから、行を足す（tree_lock.go・2026-10-26）。
	var newID string
	if err := withLiveParent(int(parentID.Int64), func() error {
		var err error
		newID, err = reserveNewPageID(parentID)
		return err
	}); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrParentGone) {
			code = http.StatusConflict
		}
		http.Error(w, "Failed to create page: "+err.Error(), code)
		return
	}

	// 親ページIDはゼロ詰め文字列に正規化（サイドカーへ記録する）。空＝トップレベル。
	parentStr := ""
	if parentID.Valid {
		parentStr = page.FormatID(int(parentID.Int64))
	}

	// 4. HTMLファイルを物理保存
	os.MkdirAll(page.GetPageDir(newID), 0755)
	if err := page.WriteFileAtomic(page.BodyPath(newID), []byte(html), 0644); err != nil {
		forgetReservedPage(newID)
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
		inheritGroup, inheritMode = page.InheritablePerms(int(parentID.Int64))
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
//   - 親を空（トップレベル）にできるのはトップページだけ（admin でも、ほかのページは不可）
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
	if childID == 0 {
		return "トップページは動かせません", http.StatusForbidden
	}
	if parentCreatesCycle(childID, parentID) {
		return "自分の子孫ページを親に指定できません（循環します）", http.StatusBadRequest
	}
	if !page.GetPerms(parentID).CanWrite(user) {
		return "新しい親ページへの書き込み権限がありません", http.StatusForbidden
	}
	// 公開の印のあるページを公開の親の下へ動かすと、実効公開になる。公開を決められるのは持ち主か管理者だけ（2026-10-30。それまでは
	// 権限の口だけが見ていて、書ける人が動かすだけで公開できた）。
	if cur := page.GetPerms(childID); cur.Public && !user.IsAdmin && !cur.OwnedBy(user) && !page.EffectivePublic(childID) && page.EffectivePublic(parentID) {
		return "公開の印のあるページを公開のページの下へ動かせるのは、持ち主か管理者だけです", http.StatusForbidden
	}
	// いまの親から外すのも、いまの親の中身を変えること（2026-10-26。それまでは新しい親だけを見ていて、書けない置き場から
	// ページを持ち出せた——【考察】コードの矛盾 §4.2）。親の無いページ（トップ）は見ない。
	var oldParent sql.NullInt64
	database.DB.QueryRow("SELECT parent_id FROM pages WHERE id = ?", childID).Scan(&oldParent)
	if oldParent.Valid && int(oldParent.Int64) != parentID && !page.GetPerms(int(oldParent.Int64)).CanWrite(user) {
		return "いまの親ページへの書き込み権限がありません（書けない置き場からは動かせません）", http.StatusForbidden
	}
	// 拡張が動かさせないページ（届いた記録を置き場の外へ・removal_guard.go・2026-10-28）。いまの親のままなら見ない。
	if !oldParent.Valid || int(oldParent.Int64) != parentID {
		if why := removalRefusal(user, childID, parentID); why != "" {
			return why, http.StatusForbidden
		}
	}
	return "", 0
}

// resyncSubtree はページとその配下すべての索引を本文から同期し直します
// （親の付け替えでテンプレート領域へ出入りしたときに使う）。
func resyncSubtree(rootID string) {
	queue := []string{rootID}
	seen := map[string]bool{} // 木が壊れて輪になっていても終わるように（2026-10-30）
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		if content, err := os.ReadFile(page.BodyPath(id)); err == nil {
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
				queue = append(queue, page.FormatID(child))
			}
		}
		rows.Close()
	}
}

// SetParentAPIHandler は編集中ページの親ページを付け替えます（親はサイドカーが正本）。
// 対象ページの write 権限に加え、変更先の妥当性（実在・循環・新しい親への write。
// トップレベル化はトップページだけ——validateParentChange）を検証してからサイドカーへ反映し、pages インデックスを更新します。
func SetParentAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, _, ok := queryPageID(w, r)
	if !ok {
		return
	}
	if !page.RequirePageWrite(w, r, id) {
		return
	}
	// エディタ内の変更操作は本文編集と同じ編集ロックで直列化する（他者保持中なら409）。
	// 2026-10-12 から画面は編集ロックを取らないので、道具が握るとき以外は素通り（親は本文と別なのですぐ効く）。
	if !editlock.RequireEditLock(w, r, id) {
		return
	}
	newParent := strings.TrimSpace(r.URL.Query().Get("parent"))

	// 付け替えの作法は芯（SetPageParent）が1箇所で持つ——加工製品ページの整理
	// （ext/toho/filing.go）も同じ芯を通るので、片方だけ古くなることがない。
	parentStore, updatedAt, err := SetPageParent(auth.CurrentUser(r), id, newParent)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	WriteJSON(w, map[string]any{"ok": true, "parent_id": parentStore, "updated_at": updatedAt})
}

// SetPageParent は親の付け替えの**芯**です（検証・サイドカー・索引・配下の同期）。
//
// HTTPの口（SetParentAPIHandler）と、加工製品ページの整理（ext/toho/filing.go）が
// 共有します——整理は「解析で生まれたページを顧客名／装置名称の下へ移す」ために
// 同じことをする必要があり、**作法を2箇所に持つと必ず片方が古くなる**ため。
//
// ここが見るのは新旧の親への write・拡張の確かめ・公開・輪です。**動かすページ自身の write と編集ロックの確認は呼ぶ側の責任**です
// （AppendToPageBody と同じ線引き。エディタからの操作はロックが要り、整理は作った直後のページを動かすので要らない）。
// （AppendToPageBody はいまは無く、同じ線引きは append_page.go の冒頭にあります。編集ロックは
// 2026-10-12 から画面が取らないので、道具が握るとき以外は素通りです。）
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
		// 新しい親が在って消されかけていないことを、木の見張りの中で確かめてから付け替える（tree_lock.go・2026-10-26）。
		if pid, e := strconv.Atoi(newParent); e == nil {
			// トップ直下へ動かすときは、置き場の題が2枚にならないこと（2026-10-30）。置き場を作るほかの口を待たせて確かめる。
			if pid == 0 {
				folderMu.Lock()
				defer folderMu.Unlock()
				if body, err := ReadPageBody(id); err == nil {
					if why, bad := boxTitleRefusal(id, body); bad {
						return "", "", errors.New(why)
					}
				}
			}
			var res struct{ store, at string }
			err := withLiveParent(pid, func() error {
				// 輪の確かめは見張りの中でもう一度（2026-10-30。A を B の下へ・B を A の下へ、が同時に来ると、どちらも上の確かめを通って輪ができた）。
				if parentCreatesCycle(childID, pid) {
					return errors.New("自分の子孫ページを親に指定できません（循環します）")
				}
				var err error
				res.store, res.at, err = setPageParentRows(childID, id, newParent)
				return err
			})
			if err != nil {
				return "", "", err
			}
			parentStore, updatedAt = res.store, res.at
			resyncSubtree(id)
			if user != nil {
				auth.Audit(user.Username, "set-parent", id+"->"+parentStore)
			}
			return parentStore, updatedAt, nil
		}
	}
	parentStore, updatedAt, err = setPageParentRows(childID, id, newParent)
	if err != nil {
		return "", "", err
	}
	if parentChanged(oldParent, newParent) {
		resyncSubtree(id)
	}
	if user != nil {
		auth.Audit(user.Username, "set-parent", id+"->"+parentStore)
	}
	return parentStore, updatedAt, nil
}

// setPageParentRows は、ページ id（数は childID）の親をサイドカーと pages に書きます（SetPageParent の中身）。
func setPageParentRows(childID int, id, newParent string) (parentStore, updatedAt string, err error) {
	if newParent != "" {
		if pid, e := strconv.Atoi(newParent); e == nil {
			parentStore = page.FormatID(pid)
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
	// 親が変わると、テンプレート領域への出入り（②索引・③計算に載るか）も変わる。判定は先祖辿り（サイドカー）なので、
	// 動かしたページとその配下を同期し直す——呼ぶ側（SetPageParent）が、木の見張りの外で行う。
	return parentStore, updatedAt, nil
}
