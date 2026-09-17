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
		`1行が1アドレスです。<strong>組織</strong>と<strong>担当者</strong>を入れて「登録」を押します` +
		`——組織は候補（同じドメインを持つ組織・「` + PersonalOrgTitle + `」）から選ぶか、新しい社名を打ちます。` +
		`担当者を空にすると、そのアドレスは組織の口（受注窓口など）として組織のページに入ります。` +
		`個人のお客様は組織を「` + PersonalOrgTitle + `」にして担当者に名前を入れます。</p>`)

	// **表は固定幅の割り付け**（`contacts-table`）。操作の欄には長い文（ドメインの説明）が
	// 入るので、自動割り付けだと操作の列が幅を食い、名前の列が1文字ずつ折れます（2026-09-17 に実際にそうなった）。
	sb.WriteString(`<table class="materials-table unhandled-table contacts-table"><tbody>`)
	for i, c := range list {
		sb.WriteString(`<tr data-domain="` + stdhtml.EscapeString(c.Domain) + `"` +
			` data-address="` + stdhtml.EscapeString(c.Address) + `">`)

		// 表示名とアドレス（**1行1アドレス**なので並べても崩れません）。
		sb.WriteString(`<td class="contact-who"><span class="contact-display">` +
			stdhtml.EscapeString(c.Name) + `</span><br>` +
			`<span class="contact-addr">` + stdhtml.EscapeString(c.Address) + `</span></td>`)
		sb.WriteString(`<td class="unhandled-clip">` + fmt.Sprint(c.Count) + `件</td>`)

		addrAttr := stdhtml.EscapeString(c.Address)
		rowKey := fmt.Sprintf("r%d", i)
		cands := orgCandidates(user, c)
		orgInit, personInit := contactRowInitials(c, cands)

		// ── 2つのコンボボックス（2026-09-17 ユーザー:「入力欄は『組織コンボボックス』と
		// 『担当者名コンボボックス』になると思います」）──
		//
		// それまでは行き先ごとに別の道具（推薦のボタン・選ぶ `select`・畳んだ新規登録）が
		// 横に並んでいました。**問いは2つだけ**です——どの組織か、誰か。候補は `datalist`
		// （素のHTMLの編集できるコンボボックス。JS無しでも打てる・CSP strict の下で動く）。
		// 候補に無い名前を打てば新しい組織になり、**同じ題の組織が既にあれば口が寄せます**
		// （`RegisterContactAPIHandler`）。
		// ⚠ セルそのものを flex にしない（表のセルとして働かなくなる）。中の div を flex にする。
		sb.WriteString(`<td class="vocab-chrome contact-act"><div class="contact-form"` +
			` data-personal-title="` + stdhtml.EscapeString(PersonalOrgTitle) + `">`)
		sb.WriteString(`<label class="contact-field">組織` +
			`<input type="text" class="contact-org" list="w-orgs-` + rowKey + `" maxlength="120"` +
			` value="` + stdhtml.EscapeString(orgInit) + `"` +
			` placeholder="社名か「` + stdhtml.EscapeString(PersonalOrgTitle) + `」" aria-label="組織"></label>`)
		// 候補は**同じドメインを持つ組織**（`ドメイン` タグ＝宣言、次に登録済みアドレスの
		// ドメイン＝推測）と「個人」。⚠ **件数に上限があります**（`domainOwnerLimit`）——
		// ユーザー:「ヤフーのようなどメインでは、候補が1万件ということもあり得ます」。
		sb.WriteString(`<datalist id="w-orgs-` + rowKey + `">`)
		for _, o := range cands {
			sb.WriteString(`<option value="` + stdhtml.EscapeString(o.Title) + `" data-id="` +
				stdhtml.EscapeString(o.ID) + `"></option>`)
		}
		sb.WriteString(`</datalist>`)
		sb.WriteString(`<label class="contact-field">担当者` +
			`<input type="text" class="contact-person" list="w-persons-` + rowKey + `" maxlength="120"` +
			` value="` + stdhtml.EscapeString(personInit) + `"` +
			` placeholder="空なら組織の口" aria-label="担当者"></label>`)
		// 担当者の候補は**候補の組織の下に既にいる人**（label に組織名。同名の別人を
		// 見分けるため）。
		sb.WriteString(`<datalist id="w-persons-` + rowKey + `">`)
		for _, o := range cands {
			for _, p := range o.Persons {
				sb.WriteString(`<option value="` + stdhtml.EscapeString(p.Title) + `" label="` +
					stdhtml.EscapeString(o.Title) + `"></option>`)
			}
		}
		sb.WriteString(`</datalist>`)
		// 取引は**新しい組織のときだけ**要ります（既にある組織には付いている）。
		// 画面は組織欄が候補に一致したら隠します（`contact-form--existing`・app.js）。
		sb.WriteString(`<span class="contact-rel">`)
		for k, rel := range Relations() {
			checked := ""
			if k == 0 {
				checked = " checked"
			}
			sb.WriteString(`<label><input type="radio" name="w-rel-` + rowKey + `" value="` +
				stdhtml.EscapeString(rel) + `"` + checked + `>` + stdhtml.EscapeString(rel) + `</label>`)
		}
		sb.WriteString(`</span>`)

		// ⚠ **「このドメインもその組織のものにする」のチェックは置きません**（2026-09-17
		// ユーザー:「ドメイン yahoo.co.jp もその組織のものにするというチェックボックスは
		// 必要ないと思います」）。理由は、**役に立つ場面がほとんど無いのに、既定で入って
		// いるのが危ない**ことです:
		//
		//   - 候補から既にある組織を選んだ行 … その組織は既に `ドメイン` を持つ（だから
		//     候補に出た）ので、**何もしない**
		//   - 新しい組織＋共有のドメイン（yahoo・gmail）… 付けると**そのドメインの他人**が
		//     以後その組織に化ける。2026-09-16 に暗黙の切り出しをやめた理由そのもの
		//   - 新しい組織＋専用のドメイン … ここだけ役に立つ
		//
		// 1つの場面のために、危ない側を既定にしていました。**ドメインは組織の性質**なので、
		// 宣言する場所は組織のページです（`ドメイン` のタグを1行書く。連絡帳の本文がその
		// 手順を書いています）。口（`/api/contacts/register` の `domains`）は残してあります。
		if c.DomainTruncated {
			sb.WriteString(`<span class="contact-hint">このドメインを持つ組織が多すぎるので、候補は一部です。` +
				`候補に無ければ社名を打ってください（同じ題の組織があればそこへ入ります）。</span>`)
		}
		sb.WriteString(`<button type="button" class="chip-btn chip-primary contact-go"` +
			` data-addresses="` + addrAttr + `" title="組織（と担当者）のページへ、このアドレスを入れます">登録</button>`)
		sb.WriteString(`</div>`)
		sb.WriteString(`</td></tr>`)
	}
	sb.WriteString(`</tbody></table>`)
	return sb.String()
}

// orgCandidate は組織のコンボボックスの候補1つです（ID が空なら「まだ無い組織」）。
type orgCandidate struct {
	ID      string
	Title   string
	Persons []PartnerRef // その組織の下に既にいる人（担当者の候補）
}

// orgCandidates は組織の候補を並べます——①`ドメイン` タグで宣言している組織
// ②登録済みアドレスのドメインから推測した組織 ③「個人」（無ければ新規として）。
//
// **同じドメインで登録された社名が候補になります**（2026-09-17 ユーザー）。連絡帳の
// 組織を全部並べることはしません——候補の数は制限されるべきで（同上）、候補に無い
// 社名は打てば足りるからです（同じ題があれば口が寄せる）。
func orgCandidates(user *auth.User, c UnknownContact) []orgCandidate {
	var out []orgCandidate
	seen := map[string]bool{}
	add := func(id, title string) {
		key := id
		if key == "" {
			key = "title:" + title
		}
		if title == "" || seen[key] {
			return
		}
		seen[key] = true
		o := orgCandidate{ID: id, Title: title}
		if id != "" {
			o.Persons = PersonsOf(user, id)
		}
		out = append(out, o)
	}
	for _, p := range c.DomainOwners {
		add(p.ID, p.Title)
	}
	if c.SuggestPageID != "" {
		add(c.SuggestPageID, c.SuggestTitle)
	}
	if id, ok := PartnerByTitle(user, PersonalOrgTitle); ok {
		add(id, PersonalOrgTitle)
	} else {
		add("", PersonalOrgTitle)
	}
	return out
}

// contactRowInitials は2つの欄の初期値を決めます。
//
//   - 担当者: 表示名が人らしければそれ（`山田 太郎`）。社名らしいもの・アドレスだけの
//     ものは空（＝組織の口）。
//   - 組織: 「個人」以外の候補が**ちょうど1つ**ならそれ。0なら社名らしい表示名
//     （`SuggestName`）、2つ以上なら空（**機械は選びません**——共有ドメイン）。
func contactRowInitials(c UnknownContact, cands []orgCandidate) (org, person string) {
	if !looksLikeCompany(c.Name) && c.Name != c.Address {
		person = c.Name
	}
	var real []orgCandidate
	for _, o := range cands {
		if o.Title != PersonalOrgTitle {
			real = append(real, o)
		}
	}
	switch len(real) {
	case 1:
		org = real[0].Title
	case 0:
		if looksLikeCompany(c.SuggestName) {
			org = c.SuggestName
		}
	}
	return org, person
}
