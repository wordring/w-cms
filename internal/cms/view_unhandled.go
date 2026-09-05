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
//	未処理 = 向き：受信 の記録で、子ページが無く、
//	         かつ「対応：不要」の印が付いていないもの
//
// **判定の軸は「向き」です**（2026-09-05）。受信箱と送信箱を1つの通信箱へ統合した
// ので、「受信箱の下にあるか」では受信と送信を見分けられなくなりました——送った
// 控えが作業待ちに混じります。向きが見える文字になったことで、そのまま条件になります。
//
// **「対応：不要」だけは人が打ちます。** 案内やお礼のように何も作らなくてよい
// メールは、放っておくと永久に未処理として残るためです。判定の材料を1つに
// できなかったのはここだけで、しかも打つのは例外のときだけで済みます。
//
// 置き場所はどこでも構いません（通信箱の中でも、トップでも）——見るページの
// 子ではなく、**通信箱の下の全部**を横断して並べます。
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
	PageID      string
	Title       string
	Channel     string
	Received    string
	From        string
	Attachments string // 添付の数（無ければ空）
}

// UnhandledIntakes は未処理の通信記録を新しい順に返します（総数も返す）。
func UnhandledIntakes(user *auth.User, limit int) (rows []unhandledRow, total int, err error) {
	inboxID, ok := MailBoxPageID()
	if !ok {
		return nil, 0, nil // 通信箱が無ければ何も出さない（エラーにはしない）
	}
	inboxInt, err := strconv.Atoi(inboxID)
	if err != nil {
		return nil, 0, err
	}

	// 未処理＝`向き：受信` のページ。**子ページが無いもの**だけを拾い、
	// 「対応：不要」が付いたものは外します。チャネル・受信日時・差出人・添付数は
	// 同じ索引から読めます。
	//
	// 通信箱の下にあるかどうかは Go 側で先祖を辿って確かめます（SQLで再帰させると
	// 読みにくく、件数も高が知れているため）。
	const q = `
		SELECT d.page_id,
		       COALESCE(p.title, ''),
		       COALESCE((SELECT c.value FROM vocab_index c
		                  WHERE c.page_id = d.page_id AND c.field = ? LIMIT 1), ''),
		       COALESCE((SELECT r.value FROM vocab_index r
		                  WHERE r.page_id = d.page_id AND r.field = '受信日時' LIMIT 1), ''),
		       COALESCE((SELECT f.value FROM vocab_index f
		                  WHERE f.page_id = d.page_id AND f.field = '差出人' LIMIT 1), ''),
		       COALESCE((SELECT a.value FROM vocab_index a
		                  WHERE a.page_id = d.page_id AND a.field = ? LIMIT 1), '')
		  FROM vocab_index d
		  JOIN pages p ON p.id = d.page_id
		 WHERE d.field = ? AND d.value = ?
		   AND NOT EXISTS (SELECT 1 FROM pages k WHERE k.parent_id = d.page_id)
		   AND NOT EXISTS (SELECT 1 FROM vocab_index h
		                    WHERE h.page_id = d.page_id AND h.field = ? AND h.value = ?)
		 GROUP BY d.page_id
		 ORDER BY 4 DESC, d.page_id DESC`

	dbRows, err := database.DB.Query(q, ChannelTag, AttachmentCountTag,
		DirectionTag, DirectionIn, HandledTag, HandledNotNeeded)
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
			&c.row.Received, &c.row.From, &c.row.Attachments); err != nil {
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
			continue // 通信箱の外の記録は対象外
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
// channelIcon はチャネルを1文字の記号にします（列を細くするため）。
// **知らないチャネルは文字のまま**出します——記号に化けて意味が消えるより、
// 見慣れない語がそのまま出るほうが直せます。
func channelIcon(channel string) string {
	switch channel {
	case "メール":
		return "✉"
	case "FAX":
		return "📠"
	case "電話":
		return "☎"
	case "":
		return "・"
	}
	return stdhtml.EscapeString(channel)
}

// shortTime は ISO 8601 を一覧用に詰めます（`09-05 09:12`）。
// 読めない値はそのまま返します（捏造しない）。
func shortTime(iso string) string {
	if len(iso) >= 16 && iso[4] == '-' && iso[10] == 'T' {
		return iso[5:10] + " " + iso[11:16]
	}
	return iso
}

// unhandledViewHTML は「未処理の受信」の作業面を組み立てます。
//
// **1行1件・新しい順**で、チャネルは記号、添付の数は 📎n。行の右端に「不要」の
// ボタン、先頭にチェックボックスを置いて**まとめて片付けられる**ようにしてあります
// ——過去分を取り込むと数百件が一度に未処理になるので、1件ずつでは終わりません
// （2026-09-05）。
//
// **ボタンの配線は app.js が持ちます。** CSP strict なのでインラインの `onclick` は
// 書けず、ここが出せるのは印（`.vocab-chrome` と `data-*`）だけです——添付の
// 「🤖 解析」ボタンと同じ作り。
//
// **本文の書き出しは出しません。** 100件ぶんの本文を毎回読むことになり、ビューは
// ページを開くたびに描かれるので釣り合いません（2026-09-05 の判断）。
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
			`「不要」を押すと消えます。</p>`)
	}
	sb.WriteString(`<table class="materials-table unhandled-table"><tbody>`)
	for _, r := range rows {
		id := stdhtml.EscapeString(r.PageID)
		sb.WriteString(`<tr data-page-id="` + id + `">`)
		// 選択の升目とボタンは**クローム**（本文ではない）。
		sb.WriteString(`<td class="vocab-chrome unhandled-pick">` +
			`<input type="checkbox" class="unhandled-check" data-page-id="` + id + `"></td>`)
		sb.WriteString(`<td class="unhandled-channel">` + channelIcon(r.Channel) + `</td>`)
		sb.WriteString(`<td class="unhandled-when">` +
			stdhtml.EscapeString(shortTime(r.Received)) + `</td>`)
		sb.WriteString(`<td class="unhandled-from">` + stdhtml.EscapeString(r.From) + `</td>`)
		sb.WriteString(`<td class="unhandled-subject"><a href="/` + id + `">` +
			stdhtml.EscapeString(r.Title) + `</a></td>`)
		clip := ""
		if r.Attachments != "" {
			clip = "📎" + stdhtml.EscapeString(r.Attachments)
		}
		sb.WriteString(`<td class="unhandled-clip">` + clip + `</td>`)
		sb.WriteString(`<td class="vocab-chrome unhandled-act">` +
			`<button type="button" class="unhandled-skip" data-page-id="` + id +
			`" title="この記録に「対応：不要」を付けて一覧から外します">不要</button></td>`)
		sb.WriteString(`</tr>`)
	}
	sb.WriteString(`</tbody></table>`)
	sb.WriteString(`<p class="vocab-chrome unhandled-bulk">` +
		`<button type="button" id="w-unhandled-bulk">選んだものを「不要」にする</button></p>`)
	return sb.String()
}
