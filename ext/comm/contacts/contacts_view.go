package contacts

// ─────────────────────────────────────────────────────────────────────────
// 未登録の連絡先の作業面（contacts.go から分離）
//
// `取引先` ページに出る計算ビューです。**1アドレス1行**で、行ごとに行き先を選べます
// （2026-09-11 ユーザー:「ドメイン別に分けるのはやめて…」）。
//
// ⚠ **機械が作るページは行き止まりにしないこと。** `取引先` を見出しだけで作って
// いた時期があり、この作業面がどこにも出ず、**100通のあいだ誰もアドレス帳を
// 見ていませんでした**（11ドメイン中の登録は1件）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"fmt"
	stdhtml "html"
	"strings"

	"w-cms/internal/auth"
)

// contactsViewHTML は「未登録の連絡先」の作業面です。
func contactsViewHTML(user *auth.User, pageIDInt int) string {
	list, err := UnknownContacts(user)
	if err != nil {
		return `<p class="view-error">連絡先の一覧を作れませんでした。</p>`
	}

	var sb strings.Builder
	sb.WriteString(`<h3 class="materials-title">📇 未登録の連絡先（` +
		fmt.Sprint(len(list)) + `件）</h3>`)
	if len(list) == 0 {
		sb.WriteString(`<p class="child-list-empty">未登録の連絡先はありません</p>`)
		return sb.String()
	}
	sb.WriteString(`<p class="unhandled-note">` +
		`1行が1アドレスです。` +
		`<strong>既にある相手が見つかった行は、そこへ足すのが初期の提案</strong>です` +
		`——押せば相手ページにアドレスが増えます（会社ページは1枚のまま）。` +
		`新しい相手なら、名前を直してから取引の種類を押してください` +
		`（顧客と仕入先はあとから足せます）。</p>`)

	partners := existingPartners(user)

	sb.WriteString(`<table class="materials-table unhandled-table"><tbody>`)
	for _, c := range list {
		sb.WriteString(`<tr data-domain="` + stdhtml.EscapeString(c.Domain) + `"` +
			` data-address="` + stdhtml.EscapeString(c.Address) + `">`)

		// 表示名とアドレス（**1行1アドレス**なので並べても崩れません）。
		sb.WriteString(`<td class="contact-who"><span class="contact-display">` +
			stdhtml.EscapeString(c.Name) + `</span><br>` +
			`<span class="contact-addr">` + stdhtml.EscapeString(c.Address) + `</span></td>`)
		sb.WriteString(`<td class="unhandled-clip">` + fmt.Sprint(c.Count) + `件</td>`)

		addrAttr := stdhtml.EscapeString(c.Address)
		sb.WriteString(`<td class="vocab-chrome unhandled-act">`)

		// ── ① 既にある相手が見つかったなら、それを先に出します ──
		//
		// **順序が効きます**。以前は新規作成の［顧客］［仕入先］［自社］が先頭にあり、
		// 「既にある相手へ足す…」は選んでいない状態でその後ろでした。実データで
		// トーアスポーツマシーンの3アドレスがこの形で並び、**押せば会社が2枚**に
		// なるところでした（2026-09-13）。
		if c.SuggestPageID != "" {
			// **人だと分かるなら、担当者ページを作るのが既定**（2026-09-13 ユーザー:
			// 「連絡先にはメールアドレス、電話番号、名前など様々なタグが必要なので、
			// ページに分割する必要があります」）。1人にタグが何個もぶら下がるので、
			// 社名ページに平らに積むと**誰のものか分からなくなります**。
			//
			// 人か会社の口かは**表示名で見分けます**（`order@…` は「コニック金型センター」、
			// 人は「潮崎 光俊」）。機械には決め切れないので、**両方出して人が選びます**。
			if !looksLikeCompany(c.Name) && c.Name != c.Address {
				sb.WriteString(`<button type="button" class="chip-btn chip-primary contact-merge"` +
					` data-addresses="` + addrAttr + `"` +
					` data-target="` + stdhtml.EscapeString(c.SuggestPageID) + `"` +
					` data-person="` + stdhtml.EscapeString(c.Name) + `"` +
					` title="「` + stdhtml.EscapeString(c.SuggestTitle) + `／` +
					ContactPersonBoxTitle + `／` + stdhtml.EscapeString(c.Name) +
					`」のページを作り、このアドレスをそこへ入れます">` +
					stdhtml.EscapeString(c.Name) + ` を担当者にする</button>`)
			}
			sb.WriteString(`<button type="button" class="chip-btn contact-merge"` +
				` data-addresses="` + addrAttr + `"` +
				` data-target="` + stdhtml.EscapeString(c.SuggestPageID) + `"` +
				` title="会社の口として「` + stdhtml.EscapeString(c.SuggestTitle) +
				`」へ足します（受注窓口など、人ではないアドレス）">` +
				`「` + stdhtml.EscapeString(c.SuggestTitle) + `」へ足す</button>`)
		}

		// ── ②③ その他の行き先 ──
		//
		// **推薦がある行では畳みます**（`details`）。推薦どおりで済むのがほとんどなので、
		// 全部を横に並べると幅が足りず、実際に［顧客］が画面外へ出ていました
		// （2026-09-13 に実データで確認）。**JSは使いません**——`details` は素のHTMLで
		// 開閉でき、CSP strict の下でも動きます。
		//
		// 推薦が無い行では畳みません。その行の主役は新規登録だからです。
		folded := c.SuggestPageID != ""
		if folded {
			sb.WriteString(`<details class="contact-more"><summary>ほかの行き先…</summary>`)
		}

		// ② 新しい相手として登録する。名前の初期値は**社名らしい表示名**
		//    （`companyLikeName`）。人名しか無ければそのアドレスの表示名が入るので、
		//    **人が直してから押す**のは変わりません。
		sb.WriteString(`<span class="contact-new">`)
		sb.WriteString(`<input type="text" class="contact-name-input" maxlength="120" value="` +
			stdhtml.EscapeString(c.SuggestName) + `" aria-label="新しい相手の名前">`)
		for _, rel := range Relations() {
			sb.WriteString(`<button type="button" class="chip-btn contact-register"` +
				` data-relation="` + stdhtml.EscapeString(rel) + `"` +
				` data-addresses="` + addrAttr + `"` +
				` title="この相手を「` + PartnerBoxTitle + `」の下のページにします（取引：` +
				stdhtml.EscapeString(rel) + `）">` + stdhtml.EscapeString(rel) + `</button>`)
		}
		sb.WriteString(`</span>`)

		// ③ 別の相手を選んで足す。推薦が外れるとき（同じ会社が2つ目のドメインから
		//    送ってくる、逆に同じドメインに別の会社が居る）に人が選び直す口です。
		//    **同じ会社かどうかは機械には決められません**。
		if len(partners) > 0 {
			sb.WriteString(`<span class="contact-other">`)
			sb.WriteString(`<select class="contact-merge-target" aria-label="別の相手へ足す">`)
			sb.WriteString(`<option value="">別の相手へ足す…</option>`)
			for _, p := range partners {
				sb.WriteString(`<option value="` + stdhtml.EscapeString(p.ID) + `">` +
					stdhtml.EscapeString(p.Title) + `</option>`)
			}
			sb.WriteString(`</select>`)
			sb.WriteString(`<button type="button" class="chip-btn contact-merge"` +
				` data-addresses="` + addrAttr + `"` +
				` title="選んだ相手ページへ、このアドレスを足します">足す</button>`)
			sb.WriteString(`</span>`)
		}
		if folded {
			sb.WriteString(`</details>`)
		}
		sb.WriteString(`</td></tr>`)
	}
	sb.WriteString(`</tbody></table>`)
	return sb.String()
}
