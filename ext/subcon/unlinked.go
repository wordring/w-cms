package subcon

// ─────────────────────────────────────────────────────────────────────────
// 「メールは来ているのに、連絡帳と結びついていない相手」（2026-09-16・§5.3）
//
// 2026-09-10 の事故がこの形でした——`南北スポーツ機械` のページはあり、
// `example-sports.co.jp` からメールも来ていたのに、**両者がつながっていません
// でした**。顧客名の推奨値が静かに空振りし、解析が読んだ誤記入りの名前がそのまま
// 出ました。
//
// ── 出すのは「直せるのに直っていないもの」だけ（2026-09-15 ユーザー決定）──
//
// **全部を警告にすると狼少年になります**。FAXだけ・電話だけの客先は**正当に
// 連絡先ゼロ**で、それは異常ではありません。直しようのないものを並べると、
// 人は一覧ごと信用しなくなります（未処理一覧で一度学んだこと）。
//
// ── ⚠ 設計文書の条件は、そのままでは発火しませんでした ──
//
// §5.3 は「索引のアドレスから題に**解決できる**のに `メールアドレス` タグが無い」
// と書き、さらに「解決の規則は `PartnerTitleForAddress` と同じものを使うこと」と
// 但し書きしていました。ところが `PartnerTitleForAddress` は**連絡先のタグを引いて
// 解決します**——タグが無ければ解決できず、解決できないなら警告も出ません。
// **条件が自分を打ち消しており、あの事故は検出できませんでした**（2026-09-16 に発見）。
//
// 代わりに使うのは、**整理が既に辿っている鎖**です:
//
//	取引先／社名 ─(子孫)→ 部品ページ ─受信元→ 通信記録 ─差出人→ メールアドレス
//
// この鎖が通るのに、その社名ページが**連絡帳の組織を指していない**（`相手` の
// 参照タグが無い）なら、**メールは来ているのに結びついていない**と言えます。
//
// **FAXだけの客先は静かなまま**です——FAXの記録に `差出人` は無いので、鎖の
// 最後で止まります。つまり「直せるのに直っていないもの」だけが光ります。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"strconv"
	"strings"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// UnlinkedViewType は「連絡帳と結びついていない相手」の形式名です。
const UnlinkedViewType = "unlinked-customers"

// unlinkedLimit は一度に出す上限です。**選べない長さの一覧は選択肢ではありません**
// ——多ければ、片付けながら何度も開く形になります。
const unlinkedLimit = 50

func init() {
	cms.RegisterVocab(cms.VocabDef{
		Type:        UnlinkedViewType,
		DisplayName: "連絡帳と未接続の相手",
		Category:    "ビュー",
		Icon:        "🔗",
		Element:     "section",
		View:        true,
	})
	cms.RegisterView(UnlinkedViewType, unlinkedViewHTML)
}

// UnlinkedCustomer は「メールは来ているのに連絡帳と結びついていない」社名ページ1件です。
type UnlinkedCustomer struct {
	PageID string // 取引先の下の社名ページ
	Title  string
	// Address はその相手から届いたメールの差出人です（**証拠**——これがあるから
	// 「直せる」と言えます）。画面はこれを出して、人が連絡帳で登録できるようにします。
	Address string
}

// UnlinkedCustomers は結びついていない社名ページを返します。
//
// **読むだけ**です（何も作りません）。直すのは人が連絡帳で登録したときで、
// そのあと整理を押せば `相手` の参照が書かれます。
func UnlinkedCustomers(user *auth.User) []UnlinkedCustomer {
	boxID, ok := CustomerBoxPageID()
	if !ok {
		return nil
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return nil
	}
	rows, err := database.DB.Query(
		`SELECT id, COALESCE(title, '') FROM pages WHERE parent_id = ? ORDER BY title`, boxInt)
	if err != nil {
		return nil
	}
	type hit struct {
		id    int
		title string
	}
	// **先に読み切ってから絞ります**（この先で子孫を辿るクエリを投げるため）。
	var found []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.title); err != nil {
			rows.Close()
			return nil
		}
		found = append(found, h)
	}
	rows.Close()

	var out []UnlinkedCustomer
	for _, h := range found {
		if len(out) >= unlinkedLimit {
			break
		}
		if !page.CanView(user, h.id) {
			continue
		}
		if hasCounterpartRef(h.id) {
			continue // 結びついている
		}
		// **メールが来ている証拠**を1つ探します（無ければ黙る）。
		addr := anySenderUnder(h.id, 0)
		if addr == "" {
			continue // FAXだけ・電話だけの客先。**正当に連絡先ゼロ**
		}
		out = append(out, UnlinkedCustomer{
			PageID: page.FormatID(h.id), Title: h.title, Address: addr,
		})
	}
	return out
}

// hasCounterpartRef は `相手` の参照タグを持つかを返します。
func hasCounterpartRef(pageIDInt int) bool {
	var n int
	database.DB.QueryRow(
		`SELECT COUNT(*) FROM page_tags WHERE page_id = ? AND name = ?`,
		pageIDInt, comm.CounterpartTag).Scan(&n)
	return n > 0
}

// anySenderUnder は、その配下の部品ページから `受信元` を辿って差出人を1つ探します。
//
// **深さは3段**（社名／段／装置名称／図面名称）。⚠ 全部を辿ると社名の数×部品の数に
// なるので、**1つ見つけたら止めます**——要るのは「メールが来ている」という事実だけで、
// 何通来たかではありません。
func anySenderUnder(pageIDInt, depth int) string {
	if depth > 3 {
		return ""
	}
	if addr := senderAddressOf(pageIDInt); addr != "" {
		return addr
	}
	rows, err := database.DB.Query(
		`SELECT id FROM pages WHERE parent_id = ?`, pageIDInt)
	if err != nil {
		return ""
	}
	var kids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return ""
		}
		kids = append(kids, id)
	}
	rows.Close()
	for _, k := range kids {
		if addr := anySenderUnder(k, depth+1); addr != "" {
			return addr
		}
	}
	return ""
}

// unlinkedViewHTML は `取引先` ページの作業面です。
//
// ⚠ **0件のときは静かにします**（「ありません」とだけ出す）。ここが常に何か言って
// いると、人は読まなくなります。
func unlinkedViewHTML(user *auth.User, pageIDInt int) string {
	list := UnlinkedCustomers(user)
	var sb strings.Builder
	sb.WriteString(`<h3 class="materials-title">🔗 連絡帳と未接続の相手（` +
		strconv.Itoa(len(list)) + `件）</h3>`)
	if len(list) == 0 {
		sb.WriteString(`<p class="child-list-empty">` +
			`メールが届いている相手は、すべて連絡帳と結びついています</p>`)
		return sb.String()
	}
	sb.WriteString(`<p class="unhandled-note">` +
		`<strong>メールは来ているのに、連絡帳の相手と結びついていません。</strong>` +
		`連絡帳でその差出人を登録し、もう一度この相手の部品を整理すると結びつきます` +
		`——結びつくと、顧客名の推奨値が正しく出るようになります。` +
		`（FAXや電話だけの相手はここに出ません。連絡先が無いのが正常だからです）</p>`)
	sb.WriteString(`<table class="materials-table unhandled-table"><tbody>`)
	for _, c := range list {
		sb.WriteString(`<tr><td><a href="/` + stdhtml.EscapeString(c.PageID) + `">` +
			stdhtml.EscapeString(c.Title) + `</a></td>`)
		sb.WriteString(`<td class="contact-addr">` + stdhtml.EscapeString(c.Address) + `</td></tr>`)
	}
	sb.WriteString(`</tbody></table>`)
	return sb.String()
}
