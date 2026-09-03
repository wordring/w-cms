package cms

// ─────────────────────────────────────────────────────────────────────────
// 未処理の受信——まだ手を付けていない通信記録の一覧（2026-09-03）
//
// ユーザー:「未処理のメールやFAXを一覧できる方法が必要かも」。
//
// **未処理の判定に新しい入力を求めません。** ユーザー:「メールを読んだ時点で
// 受注ページや部品ページが作られるので問題ない」——つまり**子ページが在ること
// 自体が「手を付けた」の印**です。人に状態を打たせると、打ち忘れたものが
// 未処理として残り続け、一覧が信用されなくなります。
//
//	未処理 = 通信記録（チャネルのタグを持つ）で、子ページが無く、
//	         かつ「対応：不要」の印が付いていないもの
//
// **「対応：不要」だけは人が打ちます。** 案内やお礼のように何も作らなくてよい
// メールは、放っておくと永久に未処理として残るためです。判定の材料を1つに
// できなかったのはここだけで、しかも打つのは例外のときだけで済みます。
//
// 置き場所はどこでも構いません（受信箱の中でも、トップでも）——見るページの
// 子ではなく、**受信箱の下の全部**を横断して並べます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"fmt"
	stdhtml "html"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// HandledTag は「対応」の印です。値が `不要` なら未処理の一覧から外れます。
const HandledTag = "対応"

// HandledNotNeeded は「対応：不要」の値です。
const HandledNotNeeded = "不要"

// unhandledLimit は一覧に出す上限です。**全部は出しません**——過去メールを
// 取り込むと数百件が一度に「未処理」になり、作業待ちの列としては読めなくなります。
// 件数は総数として別に見せるので、多いこと自体は分かります。
const unhandledLimit = 100

// unhandledRow は一覧の1行です。
type unhandledRow struct {
	PageID   string
	Title    string
	Channel  string
	Received string
	From     string
}

// UnhandledIntakes は未処理の通信記録を新しい順に返します（総数も返す）。
func UnhandledIntakes(user *auth.User, limit int) (rows []unhandledRow, total int, err error) {
	inboxID, ok := InboxPageID()
	if !ok {
		return nil, 0, nil // 受信箱が無ければ何も出さない（エラーにはしない）
	}
	inboxInt, err := strconv.Atoi(inboxID)
	if err != nil {
		return nil, 0, err
	}

	// 通信記録＝`チャネル` のタグを持つページ。**子ページが無いもの**だけを拾い、
	// 「対応：不要」が付いたものは外します。受信日時は同じ索引から読めます。
	//
	// 受信箱の下にあるかどうかは Go 側で先祖を辿って確かめます（SQLで再帰させると
	// 読みにくく、件数も高が知れているため）。
	const q = `
		SELECT c.page_id,
		       COALESCE(p.title, ''),
		       COALESCE(c.value, ''),
		       COALESCE((SELECT r.value FROM vocab_index r
		                  WHERE r.page_id = c.page_id AND r.field = '受信日時' LIMIT 1), ''),
		       COALESCE((SELECT f.value FROM vocab_index f
		                  WHERE f.page_id = c.page_id AND f.field = '差出人' LIMIT 1), '')
		  FROM vocab_index c
		  JOIN pages p ON p.id = c.page_id
		 WHERE c.field = ?
		   AND NOT EXISTS (SELECT 1 FROM pages k WHERE k.parent_id = c.page_id)
		   AND NOT EXISTS (SELECT 1 FROM vocab_index h
		                    WHERE h.page_id = c.page_id AND h.field = ? AND h.value = ?)
		 GROUP BY c.page_id
		 ORDER BY 4 DESC, c.page_id DESC`

	dbRows, err := database.DB.Query(q, ChannelTag, HandledTag, HandledNotNeeded)
	if err != nil {
		return nil, 0, err
	}
	// **先に読み切ってから絞ります。** 行を読みながら中で別のクエリを投げると、
	// カーソルが接続を握ったままなので別の接続が使われます——`:memory:` では
	// それが「空の別のDB」になり、絞り込みが静かに全部落とします（実際に踏んだ）。
	type candidate struct {
		id  int
		row unhandledRow
	}
	var found []candidate
	for dbRows.Next() {
		var c candidate
		if err := dbRows.Scan(&c.id, &c.row.Title, &c.row.Channel,
			&c.row.Received, &c.row.From); err != nil {
			dbRows.Close()
			return nil, 0, err
		}
		found = append(found, c)
	}
	err = dbRows.Err()
	dbRows.Close()
	if err != nil {
		return nil, 0, err
	}

	for _, c := range found {
		// **読めない相手には見せない**（見せ分けC案——黙って落ちる）。
		if !page.CanView(user, c.id) {
			continue
		}
		if !isDescendantOf(c.id, inboxInt) {
			continue // 受信箱の外の記録は対象外
		}
		total++
		if limit <= 0 || len(rows) < limit {
			c.row.PageID = fmt.Sprintf("%0*d", page.IDLength, c.id)
			rows = append(rows, c.row)
		}
	}
	return rows, total, nil
}

// isDescendantOf は child が root の子孫かを返します（自分自身は含めない）。
// 壊れたデータで無限に辿らないよう回数に上限を置きます（parentCreatesCycle と同じ用心）。
func isDescendantOf(childID, rootID int) bool {
	cur := childID
	for i := 0; i < 10000; i++ {
		var parent int
		err := database.DB.QueryRow(
			`SELECT COALESCE(parent_id, 0) FROM pages WHERE id = ?`, cur).Scan(&parent)
		if err != nil || parent == 0 {
			return false
		}
		if parent == rootID {
			return true
		}
		cur = parent
	}
	return false
}

// unhandledViewHTML は「未処理の受信」ビューの中身を描きます。
func unhandledViewHTML(user *auth.User, pageIDInt int) string {
	rows, total, err := UnhandledIntakes(user, unhandledLimit)
	if err != nil {
		return `<p class="view-error">未処理の一覧を作れませんでした。</p>`
	}
	if total == 0 {
		return `<p class="child-list-empty">未処理の受信はありません</p>`
	}

	var sb strings.Builder
	sb.WriteString(`<h3 class="materials-title">📥 未処理の受信（` +
		strconv.Itoa(total) + `件）</h3>`)
	if total > len(rows) {
		sb.WriteString(`<p class="unhandled-note">新しい` + strconv.Itoa(len(rows)) +
			`件を表示しています。手を付ける（受注ページや部品ページを作る）か、` +
			`「` + HandledTag + `：` + HandledNotNeeded + `」のタグを付けると消えます。</p>`)
	}
	sb.WriteString(`<table class="materials-table"><thead><tr>` +
		`<th>受信日時</th><th>チャネル</th><th>差出人</th><th>件名</th>` +
		`</tr></thead><tbody>`)
	for _, r := range rows {
		sb.WriteString(`<tr><td>` + stdhtml.EscapeString(r.Received) + `</td>` +
			`<td>` + stdhtml.EscapeString(r.Channel) + `</td>` +
			`<td>` + stdhtml.EscapeString(r.From) + `</td>` +
			`<td><a href="/` + stdhtml.EscapeString(r.PageID) + `">` +
			stdhtml.EscapeString(r.Title) + `</a></td></tr>`)
	}
	sb.WriteString(`</tbody></table>`)
	return sb.String()
}
