package cms

// ─────────────────────────────────────────────────────────────────────────
// 未処理——まだ何も生まれていない記録の一覧（2026-09-03。規則は 2026-09-05 に拡張）
//
// ユーザー:「未処理のメールやFAXを一覧できる方法が必要かも」。
//
//	未処理 = 通信箱の下のページで、`対応` の印が付いていないもの（年月フォルダを除く）
//
// **決めるのは人の印だけです**（2026-09-05 に2度改めた末の形）。機械が推し量る材料を
// 2つとも捨てました:
//
//   - **向きでは決めない**——「**かけた電話で受注することもあるので、処理はすんで
//     いません。やはり、人間がチェックするべきでは**」（ユーザー）。向きは
//     「どちら向きの出来事か」しか語りません。
//   - **子ページの有無でも決めない**——「**一つのメールに複数の案件が含まれることも
//     あるので、処理済みの判断は作業者自身がした方が良いと思います**」（ユーザー）。
//     1通に3件あれば、受注ページを1枚作っても終わっていません。
//
// もともとは「子ページが在ること自体が手を付けた印」でした（同じ方の 2026-09-03 の
// 判断）。**実データで運用の形が見えたことで、本人が撤回した**——推し量りは
// 「たいてい合っている」ので、外れたときに気づけません。
//
// 送信メールの控えだけは、作った側（`recordSentMail`）が `対応：不要` を書きます
// ——**送るという仕事はその場で終わっている**ので、人に押させる意味がありません。
//
// **判定に新しい入力を求めません。** ユーザー:「メールを読んだ時点で受注ページや
// 部品ページが作られるので問題ない」——つまり**子ページが在ること自体が「手を付けた」の印**
// です。人に状態を打たせると、打ち忘れたものが未処理として残り続け、一覧が信用されなく
// なります。年フォルダ・月フォルダが並ばないのも同じ理由で済みます（子を持つため）。
//
// **通信以外の案件もここに入ります**（2026-09-05 ユーザー:「通信以外の処理すべき案件も
// ここにいれたらどうなりますか？　たとえば、自分が使う作業台の製造などは、どことも
// 通信しません」）。**通信箱の下にページを作れば、それだけで作業待ちに並びます**
// ——タグも要りません。社内発意の仕事を別の場所に置くと、「1件の案件が2か所に散る」
// という、受信箱と送信箱を統合した理由がそのまま再発します。
//
// > **社内案件も完全に通信と無縁ではありません。** 作業台を作れば材料は発注するので、
// > その控えが `向き：送信` として同じ箱に立ちます。案件の頭だけが箱の外にあるのは歪な形でした。
//
// **「対応：不要」だけは人が打ちます。** 案内やお礼のように何も作らなくてよい
// メールは、放っておくと永久に未処理として残るためです。判定の材料を1つに
// できなかったのはここだけで、しかも打つのは例外のときだけで済みます。
//
// ビューの置き場所はどこでも構いません（通信箱の中でも、トップでも）——見るページの
// 子ではなく、**通信箱の下の全部**を横断して並べます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"fmt"
	stdhtml "html"
	"strconv"
	"strings"
	"time"

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
	Direction   string // 向き（受信／送信。メモや社内案件では空）
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

	// **駆動するのは `pages` です**（索引ではありません）。タグを1つも持たない
	// 社内案件のページも拾うためで、条件は「`対応` の印が無い」の1つだけ。
	//
	// `対応` は**値を見ません**——`不要` でも `済` でも、人が印を付けたなら外します。
	// 値で分岐すると、書き方が増えるたびに一覧が静かに取りこぼします。
	//
	// 並びの鍵は `受信日時`、無ければページの更新日時——社内案件には受信日時が
	// ありません。**時刻の無いものを最後尾へ落とすと、作ったばかりの案件が
	// 見えなくなります**。
	//
	// 通信箱の下にあるかどうかは Go 側で先祖を辿って確かめます（SQLで再帰させると
	// 読みにくく、件数も高が知れているため）。
	const q = `
		SELECT p.id,
		       COALESCE(p.title, ''),
		       COALESCE((SELECT c.value FROM vocab_index c
		                  WHERE c.page_id = p.id AND c.field = ? LIMIT 1), ''),
		       COALESCE((SELECT r.value FROM vocab_index r
		                  WHERE r.page_id = p.id AND r.field = '受信日時' LIMIT 1),
		                COALESCE(p.updated_at, '')),
		       COALESCE((SELECT f.value FROM vocab_index f
		                  WHERE f.page_id = p.id AND f.field = '差出人' LIMIT 1), ''),
		       COALESCE((SELECT a.value FROM vocab_index a
		                  WHERE a.page_id = p.id AND a.field = ? LIMIT 1), ''),
		       COALESCE((SELECT d.value FROM vocab_index d
		                  WHERE d.page_id = p.id AND d.field = ? LIMIT 1), '')
		  FROM pages p
		 WHERE NOT EXISTS (SELECT 1 FROM vocab_index h
		                    WHERE h.page_id = p.id AND h.field = ?)
		 ORDER BY 4 DESC, p.id DESC`

	dbRows, err := database.DB.Query(q, ChannelTag, AttachmentCountTag, DirectionTag, HandledTag)
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
			&c.row.Received, &c.row.From, &c.row.Attachments, &c.row.Direction); err != nil {
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
		if IsDateFolderTitle(c.row.Title) {
			continue // 年・月フォルダは仕事ではない（入れ物）
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
// newRecordChromeHTML は「手で記録を作る」一行を組み立てます。
//
// **チャネルは人が選びます**（2026-09-05 ユーザー:「受信箱にメモを作るボタンを付けて、
// チャネルを選択してはどうでしょうか。いずれFAXサーバーを接続したら、自動的に
// チャネルがFAXになります」）。手で作る道と機械が作る道が**同じ形のページ**に落ちるので、
// あとで FAX サーバーが繋がっても一覧も集計も変わりません。
//
// 動きは app.js が与えます（CSP strict なのでインラインの `onclick` は書けない）。
func newRecordChromeHTML() string {
	var sb strings.Builder
	sb.WriteString(`<p class="vocab-chrome unhandled-new">`)
	sb.WriteString(`<select id="w-memo-channel" title="どの経路の記録か">`)
	for _, c := range MemoChannels() {
		sb.WriteString(`<option value="` + stdhtml.EscapeString(c) + `">` +
			channelIcon(c) + ` ` + stdhtml.EscapeString(c) + `</option>`)
	}
	sb.WriteString(`</select>`)
	// **電話にもFAXにも向きがあります**（2026-09-05 ユーザー指摘）——かかってきた
	// 電話と、かけた電話は別の出来事。メモには向きが無いので、画面側で伏せます。
	sb.WriteString(`<select id="w-memo-direction" title="受けたのか、出したのか">`)
	for _, d := range MemoDirections() {
		sb.WriteString(`<option value="` + stdhtml.EscapeString(d) + `">` +
			directionIcon(d) + ` ` + stdhtml.EscapeString(d) + `</option>`)
	}
	sb.WriteString(`</select>`)
	sb.WriteString(`<input type="text" id="w-memo-title" placeholder="用件（省略可）" maxlength="120">`)
	sb.WriteString(`<button type="button" id="w-memo-create">＋ 記録する</button>`)
	sb.WriteString(`</p>`)
	return sb.String()
}

// directionIcon は向きを矢印で表します（受けた／出したが一目で分かる）。
func directionIcon(direction string) string {
	switch direction {
	case DirectionIn:
		return "←"
	case DirectionOut:
		return "→"
	}
	return ""
}

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
	case "メモ":
		return "📝"
	case "":
		return "・"
	}
	return stdhtml.EscapeString(channel)
}

// shortTime は ISO 8601 を一覧用に詰めます（`09-05 09:12`）。
//
// **必ず地方時へ直します。** 保存は UTC（`…Z`）で正しいのですが、切り出すだけだと
// 9時間ずれた時刻が並びます——受信日時のタグは `+09:00` を持っているので気づかず、
// **タグの無い記録（メモ・社内案件）だけが UTC で出ていました**（2026-09-05 ユーザー指摘）。
//
// 読めない値はそのまま返します（捏造しない）。
func shortTime(iso string) string {
	iso = strings.TrimSpace(iso)
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, iso); err == nil {
			return t.In(time.Local).Format("01-02 15:04")
		}
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

	var sb strings.Builder
	sb.WriteString(`<h3 class="materials-title">📥 未処理（` +
		strconv.Itoa(total) + `件）</h3>`)
	// **記録を作る口は、一覧が空でも出します。** 片付いた日にボタンごと消えると、
	// 電話が鳴ったときに置き場所が見つかりません。
	sb.WriteString(newRecordChromeHTML())
	if total == 0 {
		sb.WriteString(`<p class="child-list-empty">未処理はありません</p>`)
		return sb.String()
	}
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
		sb.WriteString(`<td class="unhandled-channel">` +
			directionIcon(r.Direction) + channelIcon(r.Channel) + `</td>`)
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
