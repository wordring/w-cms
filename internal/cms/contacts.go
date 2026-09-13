package cms

// ─────────────────────────────────────────────────────────────────────────
// アドレス帳——メールから相手を拾い、ページにする（2026-09-05）
//
// ユーザー:「アドレス帳のようなものを作って、メールから人物や電話番号、
// メールアドレスを収集しましょう」。
//
// **材料はもう索引に入っています。** 取り込みが `差出人アドレス`・`宛先アドレス`・
// `CCアドレス`・`返信先アドレス` を書いているので、新しく集める仕掛けは要りません
// ——足りないのは「**まだページになっていないアドレス**」を並べて、人が確定する口だけです。
// 未処理の受信とまったく同じ形（見つかっているものを並べ、決めるのは人）。
//
// ── 実データで分かった3つのこと（60通・11アドレス）──
//
//  1. **アドレスは揺れない。** 11アドレス中、表示名が2通りになったものはゼロ。
//     突き合わせの鍵はアドレスで足り、名寄せは要らない（機械が発行した文字列だから）。
//  2. **表示名は「人」と「会社」が混ざる**（`山田 太郎` / `株式会社緑川製作所` /
//     `レーザマックス大阪支店`）。メールは「誰か」を1つしか教えないので、
//     人と会社を分けるのは人の仕事。
//  3. **会社はドメインでまとまる——が、例外がある。** `example-sports.co.jp` に
//     4人。しかし自社の工場長だけ別プロバイダのアドレスを使っていた。
//     **ドメインは手掛かりであって決定ではない**（今日から何度も出てきた形）。
//
// ── 決めたこと ──
//
// **取引の相手は1種類。役割はタグで表します**（`取引：顧客` / `仕入先` / `自社`）。
// ユーザー:「顧客であり、仕入れ先である場合もあります」——置き場所で分けると、
// そのとき1枚に保てません。同じページに両方書けます（タグは同じ名前を何度でも置ける）。
//
// **相手ページは「取引先」の下**です（2026-09-05 ユーザー:「トップページの直接の子が
// 個人名や社名はちょっと具合が悪い」）。もとは 2026-09-03 の決定でトップ直下でしたが、
// 相手が増えるほどトップが名簿になってしまうため、**箱を1枚かませました**。
//
// **箱は1枚だけ**です。`顧客` `仕入先` `自社` で箱を分けないのは、ユーザーの
// 「顧客であり、仕入れ先である場合もあります」がそのまま効くため——置き場所で
// 分けると、その相手を**どちらか一方にしか置けません**。役割はタグの仕事です。
//
// **顧客名／装置名称／図面名称の「顧客名」も同じページ**です（同日ユーザー決定）。
// 会社ページを別に作ると「株式会社南北スポーツ機械」が2枚になり、
// 連絡先を見るページと部品を見るページが分かれてしまいます。
//
// **個人のお客様なら、そのページが本人**です。人物ページを別に作りません
// ——本人の名前のページを2枚重ねる意味がないためです。
//
// **窓口の人は `社名／担当者／名前`**（2026-09-05 ユーザー決定）。社名ページの子には
// 既に**装置名称**が並ぶので、人を直接ぶら下げると**人と装置が兄弟になり**、装置が
// 増えるほど人が埋もれます。`担当者` を1枚かませて分けます:
//
//	取引先／株式会社南北スポーツ機械／担当者／山田 太郎
//	取引先／株式会社南北スポーツ機械／標準2輪／取付ベース
//
// **人のページは要るときだけ**です。連絡先そのもの（メールアドレス・電話番号）は
// 社名ページにタグとして何個でも置けるので、**その人に添付や記録を紐づけたく
// なったとき**に初めてページにします。**作る操作は未実装**。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ContactPersonBoxTitle は社名ページの下の、窓口の人を集める箱の名前です
// （`取引先／社名／担当者／名前`）。**装置名称と人を兄弟にしない**ための1枚。
// 語を1箇所に閉じておくのは「担当」と「担当者」が混ざるのを防ぐためです。
const ContactPersonBoxTitle = "担当者"

// PartnerBoxTitle は相手ページの置き場（トップ直下）の名前です。通信箱
// （MailBoxTitle）・テンプレート置き場と同じく **h1（ページ名）が正**。
const PartnerBoxTitle = "取引先"

// PartnerBoxPageID はトップ直下の取引先ページを返します（無ければ ok=false）。
func PartnerBoxPageID() (string, bool) { return topLevelPageByTitle(PartnerBoxTitle) }

// EnsurePartnerBox は取引先ページを返し、**無ければ作ります**。
//
// **通信箱と違って自動で作ります。** 通信箱は「そこへ落とすと取り込みが走る」という
// 機能の入口なので、人が意図して置くものです。取引先はただの置き場——無いからと
// 登録ボタンを行き止まりにする理由がありません。名前を変えられたら次の登録で
// また作られますが、**取り込みのように静かに壊れることはありません**。
//
// 権限は呼ぶ側が見ます（作るときはトップへの書き込み、あるときは箱への書き込み）。
func EnsurePartnerBox(user *auth.User) (string, error) {
	if id, ok := PartnerBoxPageID(); ok {
		return id, nil
	}
	return CreateChildPage(TopPageID, user.Username, partnerBoxBody())
}

// partnerBoxBody は取引先ページの初期の本文です。
//
// **「未登録の連絡先」の作業面を最初から載せます**（2026-09-11）。ここを空の見出し
// だけで作っていたために、**アドレス帳の作業面がどこにも存在しませんでした**
// ——誰も一覧を見たことがないまま実メール100通が過ぎ、11ドメインのうち登録済みは
// 1件だけ（しかもそれは整理が作った側で、識別子を持っていなかった）。
//
// 通信箱は**人が意図して置くページ**なので、作業面も人が入れます。取引先は
// **機械が作る**ので、**行き止まりのページを作らない責任はこちらにあります**。
func partnerBoxBody() string {
	return "<h1>" + stdhtml.EscapeString(PartnerBoxTitle) + "</h1>" +
		"<p>取引の相手（会社・個人）を集めます。製造部品の階層もこの下です" +
		"（社名／段／装置名称／図面名称）。</p>" +
		`<section data-type="unknown-contacts"></section>`
}

// EmailTag は連絡先のメールアドレスです。**1ページに何個でも置けます**
// （会社の窓口が複数、同じ人が複数のアドレスを持つ、どちらも起きる）。
const EmailTag = "メールアドレス"

// RelationTag は取引の役割です（値は RelationCustomer / RelationSupplier / RelationSelf）。
// **同じページに複数書けます**——顧客でも仕入先でもある相手が実際にいるためです。
const RelationTag = "取引"

// 取引の値。**表引きで閉じます**——`仕入先` と `仕入れ先` が混ざると絞り込みが
// 静かに取りこぼします。
const (
	RelationCustomer = "顧客"
	RelationSupplier = "仕入先"
	RelationSelf     = "自社"
)

// Relations は選択肢を並び順つきで返します（画面が使います）。
func Relations() []string { return []string{RelationCustomer, RelationSupplier, RelationSelf} }

// addressFields は索引からアドレスを拾う項目です。取り込みが書いている名前
// （intake_eml.go の writeAddressTags）とそろえること。
var addressFields = []string{"差出人アドレス", "宛先アドレス", "CCアドレス", "返信先アドレス"}

// nameOfAddressField は、そのアドレス項目と対になる表示名の項目です。
var nameOfAddressField = map[string]string{
	"差出人アドレス": "差出人",
	"宛先アドレス":  "宛先",
	"CCアドレス":  "CC",
	"返信先アドレス": "返信先",
}

// UnknownContact は「まだページになっていない相手」**1アドレス1件**です。
//
// **もとはドメインでまとめていました**（2026-09-05〜09-13）。会社が1枚に収まるように、
// という意図でしたが、実データで**使えないことが分かりました**（2026-09-13 ユーザー:
// 「ドメイン別に分けるのはやめて、ドメインから社名が推測できる場合は、社名を推薦する
// 程度にします」）:
//
//   - **3人が1行に入り、表示名は多数決で1つ**になりました。`example-sports.co.jp` の
//     行は平井・山田・吉原の3人ぶんで、登録の初期値が**人名の「平井　成志」**。
//     そのまま押すと、人の名前の会社ページができます。
//   - **1人ずつ扱えません**。担当者を1人だけページにしたいときに行を割れない。
//   - 同じ人が2つのドメインを使うと**別の行に散ります**（実データに実例あり）。
//
// 会社を1枚に保つ仕事は、**まとめることではなく推薦がやります**（`Suggest`）——
// 既にある取引先のドメインと一致すれば、**その相手へ足す**のが初期の提案になります。
type UnknownContact struct {
	Address string // sato@example-sports.co.jp
	Domain  string // example-sports.co.jp（推薦の手掛かり・表示にも使う）
	Name    string // このアドレスの表示名（無ければアドレス）
	Count   int    // 索引に出てきた回数（多い順に並べるため）

	// SuggestPageID は「既にある相手へ足す」の初期選択です（空なら新規登録）。
	// **同じドメインのアドレスが既に取引先ページに載っているとき**に埋まります。
	SuggestPageID string
	// SuggestTitle はその相手の題（画面が「○○へ足す」と書けるように）。
	SuggestTitle string
	// SuggestName は新規登録するときの社名の推薦です。**同じドメインの表示名のうち
	// 社名らしいもの**を採り、無ければこのアドレスの表示名。
	SuggestName string
}

// UnknownContacts は、索引にあってページになっていない相手を**1アドレス1件**で返します。
func UnknownContacts(user *auth.User) ([]UnknownContact, error) {
	known, err := knownEmails()
	if err != nil {
		return nil, err
	}

	// アドレスごとに数え、**推薦の材料としてドメインも見ます**（まとめはしません）。
	type acc struct {
		names map[string]int // このアドレスの表示名（揺れることがあるので多数決）
		count int
	}
	byAddr := map[string]*acc{}
	namesByDomain := map[string]map[string]int{} // 社名の推薦用

	// アドレスと表示名を**位置で対応づけます**（2026-09-13）。
	//
	// もとはページと項目名だけで引いていました（`LIMIT 1`）。1通に CC が2人いると
	// **2人とも先頭の名前になります**——実データで `yamada@example-sports.co.jp`
	// の表示名が「南 公一」（自社）になっていました。ドメインでまとめていたころは
	// 多数決に埋もれて見えず、1アドレス1行にして初めて表に出たものです。
	//
	// 取り込みは `<dt>CC</dt><dd>名前</dd><dt>CCアドレス</dd><dd>…</dd>` の順に書き、
	// 索引の `row_no` が1つずつ増えます。つまり**名前はアドレスの1つ手前**です。
	names, err := addressDisplayNames()
	if err != nil {
		return nil, err
	}

	rows, err := database.DB.Query(
		`SELECT page_id, field, row_no, value FROM vocab_index
		  WHERE data_type = 'tags' AND field IN (`+sqlPlaceholders(len(addressFields))+`)`,
		toAnySlice(addressFields)...)
	if err != nil {
		return nil, err
	}
	// **先に読み切ってから解釈します**（行を読みながら別のクエリを投げない）。
	type hit struct {
		pageID int
		field  string
		rowNo  int
		value  string
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.pageID, &h.field, &h.rowNo, &h.value); err != nil {
			rows.Close()
			return nil, err
		}
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, h := range hits {
		addr := normalizeEmail(h.value)
		if addr == "" || known[addr] {
			continue
		}
		// **読めないページの相手は数えません**（見せ分けC案——黙って落ちる）。
		if !page.CanView(user, h.pageID) {
			continue
		}
		a := byAddr[addr]
		if a == nil {
			a = &acc{names: map[string]int{}}
			byAddr[addr] = a
		}
		a.count++
		if n := names[nameKey{h.pageID, nameOfAddressField[h.field], h.rowNo - 1}]; n != "" {
			a.names[n]++
			d := domainOf(addr)
			if namesByDomain[d] == nil {
				namesByDomain[d] = map[string]int{}
			}
			namesByDomain[d][n]++
		}
	}

	// **推薦の材料**——既にある取引先のドメイン（そのドメインのアドレスが載っている相手）。
	byPartnerDomain := partnersByDomain(user)

	out := make([]UnknownContact, 0, len(byAddr))
	for addr, a := range byAddr {
		d := domainOf(addr)
		c := UnknownContact{
			Address: addr,
			Domain:  d,
			Count:   a.count,
			Name:    mostCommon(a.names),
		}
		if c.Name == "" {
			c.Name = addr // 表示名が無ければアドレスそのもの
		}
		// ① **既にある相手が最優先**。同じドメインのアドレスが取引先ページに載って
		//    いれば、新しくページを作るのではなく**そこへ足す**のが正しい操作です。
		//    実データで、南北スポーツ機械の3アドレスが「未登録」として並び、
		//    目立つのが新規作成のボタンだったせいで**会社が2枚になりかけました**。
		if p, ok := byPartnerDomain[d]; ok {
			c.SuggestPageID, c.SuggestTitle = p.PageID, p.Title
		}
		// ② 新規のときの社名の推薦——**同じドメインの表示名のうち社名らしいもの**。
		//    人名しか無ければ諦めてこのアドレスの表示名（人の名前で会社ページを作るのは
		//    人が決めることで、機械が勝手に「株式会社」を付けたりはしません）。
		c.SuggestName = companyLikeName(namesByDomain[d])
		if c.SuggestName == "" {
			c.SuggestName = c.Name
		}
		out = append(out, c)
	}
	// 多い順（よく来る相手から片付けられるように）。同数は**ドメイン→アドレス**順で、
	// 同じ会社の人が隣り合うようにします（まとめるのはやめましたが、並べはします）。
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		return out[i].Address < out[j].Address
	})
	return out, nil
}

// partnerRefByDomain は取引先ページ1件の参照です（推薦に使う）。
type partnerRefByDomain struct{ PageID, Title string }

// partnersByDomain は「そのドメインのアドレスが載っている取引先」をドメインごとに返します。
//
// **ドメインは手掛かりであって決定ではありません**——自社の工場長だけ別プロバイダの
// アドレスを使っている実例があるので、ここで返すのは**初期の提案**だけです。
// 人が別の相手を選び直せます。
//
// `取引：自社` は外します（差出人が自社でも「自社へ足す」を勧めない）。
func partnersByDomain(user *auth.User) map[string]partnerRefByDomain {
	out := map[string]partnerRefByDomain{}
	// **木ぜんたいから引き、会社へ丸めます**——アドレスが載っているのは社名ページとは
	// 限りません（`取引先／社名／担当者／氏名`）。2026-09-13 に連絡先を人ごとの
	// ページへ分けたときからの決まりです。
	rows, err := database.DB.Query(
		`SELECT v.page_id, v.value FROM vocab_index v WHERE v.field = ?`, EmailTag)
	if err != nil {
		return out
	}
	type hit struct {
		id    int
		value string
	}
	// **先に読み切ってから絞ります**（行を読みながら別のクエリを投げない）。
	var found []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.value); err != nil {
			rows.Close()
			return out
		}
		found = append(found, h)
	}
	rows.Close()

	for _, h := range found {
		v := normalizeEmail(h.value)
		if v == "" || !page.CanView(user, h.id) {
			continue
		}
		companyID, title, ok := PartnerOfPage(h.id)
		if !ok || title == "" {
			continue
		}
		if isSelfPartner(companyID) {
			continue
		}
		d := domainOf(v)
		if d == "" {
			continue
		}
		if _, dup := out[d]; dup {
			continue // 先に見つかったほうを採る（同じドメインに2社は稀）
		}
		out[d] = partnerRefByDomain{PageID: fmt.Sprintf("%06d", companyID), Title: title}
	}
	return out
}

// companyLikeSuffixes は「社名らしさ」の手掛かりです。
//
// **表引きで閉じます**——機械に社名と人名を見分けさせる一般解はありません。
// 実データに出てくる形だけを並べ、当たらなければ**推薦しない**（人が打つ）。
var companyLikeSuffixes = []string{
	"株式会社", "有限会社", "合同会社", "(株)", "（株）", "(有)", "（有）",
	"製作所", "工業", "工業所", "商会", "商店", "センター", "工房", "鉄工所", "産業",
	"Co.", "Corp", "Inc", "Ltd", "LLC", "GmbH",
}

// companyLikeName は表示名の中から**社名らしいもの**を1つ選びます（無ければ空）。
//
// 同じドメインに「平井　成志」「山田 太郎」「南北スポーツ機械」が混ざるとき、
// 会社ページの題にふさわしいのは最後のものです。**多数決では人名が勝ちます**
// （人は会社より数が多い）——2026-09-13 に実データでそうなりました。
func companyLikeName(names map[string]int) string {
	best, bestN := "", 0
	for n, c := range names {
		if !looksLikeCompany(n) {
			continue
		}
		if c > bestN || (c == bestN && n < best) {
			best, bestN = n, c
		}
	}
	return best
}

func looksLikeCompany(name string) bool {
	for _, s := range companyLikeSuffixes {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}

// knownEmails は既にページに登録済みのアドレスを集めます。
//
// **「取引先の下に在る」ことまで見ます**（2026-09-13）。もとは `メールアドレス` タグを
// 持つページがどこかに在れば登録済みと数えていました。すると**連絡先ページを取引先の
// 外へ動かしたとき、アドレスが行方不明になります**——未登録の一覧には出てこないのに、
// 照合（`PartnerTitleForAddress`）は会社へ丸められないので答えられない。
// **片付いた顔をして効かない**、いちばん気づきにくい壊れ方です（同日に実測）。
//
// 木で判定するので、**動かせば一覧が追従します**——外へ出せば戻ってきて、
// 入れ直せばまた消えます。人が間違えて動かしても、画面がそれを教えます。
func knownEmails() (map[string]bool, error) {
	rows, err := database.DB.Query(
		`SELECT page_id, value FROM vocab_index WHERE field = ?`, EmailTag)
	if err != nil {
		return nil, err
	}
	type hit struct {
		id    int
		value string
	}
	// **先に読み切ってから絞ります**（行を読みながら別のクエリを投げない）。
	var found []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.value); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	known := map[string]bool{}
	for _, h := range found {
		a := normalizeEmail(h.value)
		if a == "" {
			continue
		}
		if _, _, ok := PartnerOfPage(h.id); !ok {
			continue // 取引先の外に書かれたアドレスは「登録済み」ではない
		}
		known[a] = true
	}
	return known, nil
}

// displayNameFor は同じページの表示名タグを1つ読みます（無ければ空）。
// sqlPlaceholders は `?, ?, ?` を作ります（`IN (…)` に使う）。
//
// **項目の数を決め打ちにしないため**です。`?` を4つ書いて配列の添字を直に渡す形は、
// 項目が1つ増えた日に panic します（`Bcc` を足す、など）。
func sqlPlaceholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// toAnySlice は文字列の並びをクエリの引数へ渡せる形にします。
func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// nameKey は表示名を位置で引くための鍵です（ページ・項目名・索引の行番号）。
type nameKey struct {
	pageID int
	field  string
	rowNo  int
}

// addressDisplayNames は `差出人`・`宛先`・`CC`・`返信先` の値を**位置つき**で集めます。
//
// **1通に同じ項目が何度も出ます**（CCが2人なら `CC` が2件）。ページと項目名だけで
// 引くと先頭しか取れず、2人目以降に他人の名前が付きます——だから `row_no` ごと持ちます。
// 対になるアドレスは**次の行**（`row_no + 1`）です。
func addressDisplayNames() (map[nameKey]string, error) {
	out := map[nameKey]string{}
	fields := make([]string, 0, len(nameOfAddressField))
	for _, n := range nameOfAddressField {
		fields = append(fields, n)
	}
	rows, err := database.DB.Query(
		`SELECT page_id, field, row_no, value FROM vocab_index
		  WHERE data_type = 'tags' AND field IN (`+sqlPlaceholders(len(fields))+`)`,
		toAnySlice(fields)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k nameKey
		var v string
		if err := rows.Scan(&k.pageID, &k.field, &k.rowNo, &v); err != nil {
			return nil, err
		}
		// 取り込みが引用符ごと拾うことがある（`'南 公一'`）。表示のためだけなので落とす。
		out[k] = strings.Trim(strings.TrimSpace(v), `'"`)
	}
	return out, rows.Err()
}

// normalizeEmail はアドレスを突き合わせ用に畳みます。
//
// **大小の違いだけ**を畳みます。アドレスのローカル部は理屈のうえでは大小を
// 区別しますが、実際に区別する事業者はまずおらず、**同じ人を2人に見せる**ほうが害が大きい。
func normalizeEmail(v string) string {
	s := strings.ToLower(strings.Trim(strings.TrimSpace(v), `<>'"`))
	if !strings.Contains(s, "@") || strings.HasPrefix(s, "@") || strings.HasSuffix(s, "@") {
		return ""
	}
	return s
}

// domainOf はアドレスのドメイン部を返します。
func domainOf(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return addr[i+1:]
	}
	return addr
}

// mostCommon はいちばん多く出た文字列を返します（同数なら辞書順で安定させる）。
func mostCommon(counts map[string]int) string {
	best, bestN := "", 0
	for s, n := range counts {
		if n > bestN || (n == bestN && s < best) {
			best, bestN = s, n
		}
	}
	return best
}

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
		// 南北スポーツ機械の3アドレスがこの形で並び、**押せば会社が2枚**に
		// なるところでした（2026-09-13）。
		if c.SuggestPageID != "" {
			// **人だと分かるなら、担当者ページを作るのが既定**（2026-09-13 ユーザー:
			// 「連絡先にはメールアドレス、電話番号、名前など様々なタグが必要なので、
			// ページに分割する必要があります」）。1人にタグが何個もぶら下がるので、
			// 社名ページに平らに積むと**誰のものか分からなくなります**。
			//
			// 人か会社の口かは**表示名で見分けます**（`order@…` は「コニック金型センター」、
			// 人は「山田 太郎」）。機械には決め切れないので、**両方出して人が選びます**。
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

// RegisterContactAPIHandler は POST /api/contacts/register です。
// 入力: {"name":"株式会社緑川製作所", "relation":"仕入先", "addresses":["…"]}
func RegisterContactAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	var req struct {
		Name      string   `json:"name"`
		Relation  string   `json:"relation"`
		Addresses []string `json:"addresses"`
		// PageID があれば**既存の相手ページへ足します**（2026-09-06）。同じ会社が
		// 2つ目のドメインから送ってくると、アドレス帳には新しい行として現れます
		// ——そこで新しいページを作ると、会社ページが2枚になります。
		PageID string `json:"page_id"`
		// PersonName があれば、その会社の**担当者ページ**へ入れます
		// （`取引先／社名／担当者／氏名`・2026-09-13）。連絡先は1人にタグが何個も
		// ぶら下がるので（メール・電話・役職）、**人ごとの器**が要ります。
		// 空なら会社の口として社名ページへ（`order@…` のような人でないもの）。
		PersonName string `json:"person_name"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	var addrs []string
	for _, a := range req.Addresses {
		if v := normalizeEmail(a); v != "" {
			addrs = append(addrs, v)
		}
	}
	if len(addrs) == 0 {
		JSONFail(w, http.StatusBadRequest, "メールアドレスがありません")
		return
	}

	// ── 既存の相手へ足す ──
	if raw := strings.TrimSpace(req.PageID); raw != "" {
		target, ok := page.NormalizeID(raw)
		if !ok {
			JSONFail(w, http.StatusBadRequest, "相手ページのIDが不正です")
			return
		}
		idInt, err := strconv.Atoi(target)
		if err != nil || !isPartnerPage(idInt) {
			// **箱の外へは足しません**（ドメインの逆引きが別物を拾うため）。
			JSONFail(w, http.StatusBadRequest, "「"+PartnerBoxTitle+"」の下のページを選んでください")
			return
		}
		if !page.RequirePageWrite(w, r, target) {
			return
		}
		// **人の名前が来たら、その人のページへ入れます**（2026-09-13）。
		// 社名ページにアドレスを平らに積むと、6つ並んだ `メールアドレス` が誰のものか
		// 分からなくなり、電話番号を足す先もありません。
		dest, destTitle := target, partnerTitleOf(idInt)
		if person := strings.TrimSpace(req.PersonName); person != "" {
			pid, err := EnsureContactPerson(user, target, person)
			if err != nil {
				JSONFail(w, http.StatusInternalServerError, "担当者ページを作れません: "+err.Error())
				return
			}
			dest = pid
			destTitle = destTitle + "／" + NormalizeNameForIngest(person)
		}
		added, err := AddContactAddresses(dest, user.Username, addrs)
		if err != nil {
			JSONFail(w, http.StatusInternalServerError, "相手ページへ足せません: "+err.Error())
			return
		}
		auth.Audit(user.Username, "contact.add-addresses",
			dest+" +"+strconv.Itoa(added)+" "+strings.Join(addrs, ","))
		json.NewEncoder(w).Encode(map[string]any{
			"success": true, "page_id": dest, "title": destTitle,
			"added": added, "merged": true,
		})
		return
	}

	// ── 新しい相手ページを作る ──
	// **相手ページの題も早期に正規化します**（2026-09-06）。部品階層の顧客名と
	// **同じページ**なので、こちらだけ畳まないと題が食い違って2枚に分かれます。
	name := NormalizeNameForIngest(req.Name)
	if name == "" {
		JSONFail(w, http.StatusBadRequest, "名前を入れてください")
		return
	}
	if !validRelation(req.Relation) {
		JSONFail(w, http.StatusBadRequest, "取引の種類が不正です")
		return
	}

	// **相手ページは「取引先」の下**（顧客名ページと同じ場所——会社を2枚にしない）。
	// 箱がまだ無いときはトップへ1枚足すので、**トップへの書き込み**が要ります。
	needParent := TopPageID
	if id, ok := PartnerBoxPageID(); ok {
		needParent = id
	}
	if !page.RequirePageWrite(w, r, needParent) {
		return
	}
	boxID, err := EnsurePartnerBox(user)
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, "「"+PartnerBoxTitle+"」ページを作れません: "+err.Error())
		return
	}

	var b strings.Builder
	b.WriteString("<h1>" + stdhtml.EscapeString(name) + "</h1>")
	b.WriteString(`<dl data-type="tags">`)
	WriteTag(&b, RelationTag, req.Relation)
	for _, a := range addrs {
		WriteTag(&b, EmailTag, a)
	}
	b.WriteString("</dl>")
	// 電話番号は空で置きます——**書く場所が見えていれば、人は書きます**
	// （タグがあれば ☎ 発信のボタンも出ます・app.js）。
	b.WriteString(`<p><br/></p>`)

	pageID, err := CreateChildPage(boxID, user.Username, b.String())
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, "相手ページを作れません: "+err.Error())
		return
	}
	auth.Audit(user.Username, "contact.register", pageID+" ("+req.Relation+") "+strings.Join(addrs, ","))
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "page_id": pageID, "title": name,
	})
}

// UnfileContactAPIHandler は POST /api/contacts/unfile です。
// 入力: {"page_id":"010274", "address":"ms-noreply@microsoft.com"}
//
// **分類を取り消して未分類へ戻します**（2026-09-13 ユーザー:「間違えてアドレスを
// 分類した場合、どうやって未分類に戻しますか？」）。
//
// やることは**タグを1つ外すだけ**です。未登録の一覧は「どこにも
// `メールアドレス` タグが無いアドレス」という**索引からの派生**なので、
// 外せば自動的に戻ります（解析済みの印と同じ形——状態を別に持たない）。
//
// **ページは消しません。** 間違えて作った相手ページが空になることはありますが、
// 消すかどうかは人が決めます——押し間違いの取り消しが、**別の押し間違いで
// ページを消す**ことになっては割に合いません。空になったことは返り値で伝えます。
func UnfileContactAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	var req struct {
		PageID  string `json:"page_id"`
		Address string `json:"address"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, ok := page.NormalizeID(strings.TrimSpace(req.PageID))
	if !ok {
		JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	addr := normalizeEmail(req.Address)
	if addr == "" {
		JSONFail(w, http.StatusBadRequest, "メールアドレスがありません")
		return
	}
	idInt, err := strconv.Atoi(pageID)
	if err != nil {
		JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	// **取引先の下だけ**（ここは連絡先の分類を取り消す口で、本文の一般的な編集口では
	// ありません。よそのページのタグを消せる道を増やさない）。
	if _, _, inPartner := PartnerOfPage(idInt); !inPartner {
		JSONFail(w, http.StatusBadRequest, "「"+PartnerBoxTitle+"」の下のページではありません")
		return
	}
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}

	removed := 0
	if err := RewriteBody(pageID, user.Username, func(current string) string {
		removed = 0
		return removeEmailTag(current, addr, &removed)
	}); err != nil {
		JSONFail(w, http.StatusInternalServerError, "外せません: "+err.Error())
		return
	}
	if removed == 0 {
		JSONFail(w, http.StatusNotFound, "そのアドレスはこのページにありません")
		return
	}
	auth.Audit(user.Username, "contact.unfile", pageID+" -"+addr)

	// 残りを数えて、**空になったことだけ伝えます**（消すのは人の判断）。
	var left int
	database.DB.QueryRow(
		`SELECT COUNT(*) FROM vocab_index WHERE page_id = ? AND field = ?`,
		idInt, EmailTag).Scan(&left)
	var children int
	database.DB.QueryRow(`SELECT COUNT(*) FROM pages WHERE parent_id = ?`, idInt).Scan(&children)

	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "page_id": pageID, "address": addr,
		"remaining": left, "children": children,
		"empty": left == 0 && children == 0,
	})
}

// removeEmailTag は本文から `<dt>メールアドレス</dt><dd>そのアドレス</dd>` を外します。
//
// **畳んだ一致で探します**——本文には大文字混じりで書かれていることがあるためです
// （`normalizeEmail` は小文字化と前後の空白落としだけ）。
// 空になった可変タグの `dl` も畳みます（空の箱を残さない）。
func removeEmailTag(body, addr string, removed *int) string {
	re := regexp.MustCompile(`<dt>` + regexp.QuoteMeta(EmailTag) + `</dt><dd>([^<]*)</dd>`)
	out := re.ReplaceAllStringFunc(body, func(m string) string {
		sub := re.FindStringSubmatch(m)
		if sub != nil && normalizeEmail(sub[1]) == addr {
			*removed++
			return ""
		}
		return m
	})
	// `<dl data-type="tags"></dl>` が残ったら外す。
	return regexp.MustCompile(`<dl data-type="tags">\s*</dl>`).ReplaceAllString(out, "")
}

// validRelation は取引の値を表引きで確かめます。
func validRelation(v string) bool {
	for _, r := range Relations() {
		if r == v {
			return true
		}
	}
	return false
}

// PartnerTitleForAddress は差出人アドレスから、取引先ページの**題**を引きます。
//
// **社名の揺れを消すための鍵**です（2026-09-06 ユーザー:「社名の揺れは、エイリアスの
// 表かAIでなくせませんか？」）。実データの初回で「南北スポーツ機械」と
// 「株式会社南北スポーツ機械」が同じ会社で2枚になりました——機械が読んだ名前を
// 人が打ち写したためです。**推測をやめて、既にあるページを名指しで引きます。**
//
// 引き方は2段:
//
//  1. **アドレスの完全一致**。ドメインより先に見ます——実データに「自社の工場長だけ
//     別プロバイダのアドレス」という例があり、ドメインを先に見ると取り違えます。
//  2. **ドメインの一致**。会社の窓口は増えるので、新しい人からのメールでも当たります。
//
// **`取引：自社` のページは返しません。** 顧客名の推奨値として自社が出ることは
// ありえず、出ると人がそのまま押してしまいます。
//
// 見つからなければ ok=false——**呼ぶ側は読めた名前へ戻ります**。新しい顧客の1通目は
// ここに無いのが正常で、そのときは人が打ちます。
func PartnerTitleForAddress(user *auth.User, addr string) (string, bool) {
	mail := normalizeEmail(addr)
	if mail == "" {
		return "", false
	}
	domain := mail[strings.LastIndex(mail, "@"):] // "@example.co.jp"

	// **取引先の木ぜんたいから引きます**（2026-09-13）。連絡先を人ごとのページへ
	// 分けたので、アドジスが載っているのは社名ページとは限りません
	// （`取引先／社名／担当者／氏名`）。見つけた先を `PartnerOfPage` で**会社へ丸めて**
	// から返します——整理が欲しいのは会社の名前だからです。
	rows, err := database.DB.Query(
		`SELECT v.page_id, v.value FROM vocab_index v WHERE v.field = ?`, EmailTag)
	if err != nil {
		return "", false
	}
	type hit struct {
		id    int
		value string
	}
	// **先に読み切ってから絞ります**（行を読みながら別のクエリを投げない）。
	var found []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.value); err != nil {
			rows.Close()
			return "", false
		}
		found = append(found, h)
	}
	rows.Close()

	byDomain := ""
	for _, h := range found {
		v := normalizeEmail(h.value)
		if v == "" || !page.CanView(user, h.id) {
			continue
		}
		// 載っているのが担当者ページでも、**答えるのは会社の名前**です。
		companyID, title, ok := PartnerOfPage(h.id)
		if !ok || title == "" {
			continue // 取引先の外のページに書かれたアドレスは相手ではない
		}
		// `取引：自社` は推奨値にしません（社名ページに付きます）。
		if isSelfPartner(companyID) {
			continue
		}
		if v == mail {
			return title, true // 完全一致が最優先
		}
		if byDomain == "" && strings.HasSuffix(v, domain) {
			byDomain = title
		}
	}
	if byDomain != "" {
		return byDomain, true
	}
	return "", false
}

// isSelfPartner はそのページが `取引：自社` かを返します。
func isSelfPartner(pageIDInt int) bool {
	var n int
	database.DB.QueryRow(
		`SELECT COUNT(*) FROM vocab_index WHERE page_id = ? AND field = ? AND value = ?`,
		pageIDInt, RelationTag, RelationSelf).Scan(&n)
	return n > 0
}

// AddContactAddresses は既存の相手ページへ `メールアドレス` のタグを足します。
//
// **2つ目のドメインのため**です（2026-09-06）。同じ会社が別のドメインからも
// メールを送ってくると、アドレス帳には新しい行として現れます。押すたびに新しい
// ページができると、**会社ページが2枚**になり、社名の揺れを消した意味が無くなります。
//
// 既に載っているアドレスは足しません——二度押しでタグが並ぶのを防ぐだけの判定なので、
// `MarkHandled` と同じく**文字列で見ます**（取りこぼしても害は同じタグが2つ）。
func AddContactAddresses(pageID, author string, addrs []string) (int, error) {
	added := 0
	err := RewriteBody(pageID, author, func(current string) string {
		added = 0 // 呼び直されても数が増えないように
		var pairs strings.Builder
		for _, a := range addrs {
			dd := `<dd>` + stdhtml.EscapeString(a) + `</dd>`
			if strings.Contains(current, dd) {
				continue
			}
			pairs.WriteString(`<dt>` + stdhtml.EscapeString(EmailTag) + `</dt>` + dd)
			added++
		}
		if pairs.Len() == 0 {
			return current
		}
		if at := endOfFirstTagList(current); at >= 0 {
			return current[:at] + pairs.String() + current[at:]
		}
		return insertAfterH1(current, `<dl data-type="tags">`+pairs.String()+`</dl>`)
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

// partnerTitleOf は索引からページの題を引きます（引けなければ空）。
func partnerTitleOf(pageIDInt int) string {
	var t string
	database.DB.QueryRow(`SELECT COALESCE(title, '') FROM pages WHERE id = ?`, pageIDInt).Scan(&t)
	return t
}

// isPartnerPage はそのページが「取引先」の直下にあるかを返します。
//
// **足す先を箱の中に限ります。** 画面から来たIDをそのまま信じると、通信記録や
// 図面ページに `メールアドレス` のタグが付き、ドメインの逆引きが別物を拾います。
func isPartnerPage(pageIDInt int) bool {
	boxID, ok := PartnerBoxPageID()
	if !ok {
		return false
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return false
	}
	var parent int
	if err := database.DB.QueryRow(
		`SELECT COALESCE(parent_id, 0) FROM pages WHERE id = ?`, pageIDInt).Scan(&parent); err != nil {
		return false
	}
	return parent == boxInt
}

// PartnerOfPage は、そのページが属する**相手（社名ページ）**を返します。
//
// 担当者ページ（`取引先／社名／担当者／氏名`）や、その下のページから
// **会社へ戻る**ための道です。連絡先を人ごとのページに分けた結果、
// 「このアドレスは誰の会社のものか」を答えるのに祖先を辿る必要が出ました
// （2026-09-13 ユーザー:「連絡先にはメールアドレス、電話番号、名前など様々なタグが
// 必要なので、ページに分割する必要があります」）。
//
// 社名ページ自身を渡せばそれ自身が返ります。取引先の外なら ok=false。
// 壊れたデータで無限に辿らないよう回数に上限を置きます（`isDescendantOf` と同じ用心）。
func PartnerOfPage(pageIDInt int) (id int, title string, ok bool) {
	boxID, found := PartnerBoxPageID()
	if !found {
		return 0, "", false
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return 0, "", false
	}
	cur := pageIDInt
	for i := 0; i < 100; i++ {
		var parent int
		var t string
		if err := database.DB.QueryRow(
			`SELECT COALESCE(parent_id, 0), COALESCE(title, '') FROM pages WHERE id = ?`,
			cur).Scan(&parent, &t); err != nil {
			return 0, "", false
		}
		if parent == boxInt {
			return cur, t, true
		}
		if parent == 0 {
			return 0, "", false
		}
		cur = parent
	}
	return 0, "", false
}

// EnsureContactPerson は `取引先／社名／担当者／氏名` のページを返し、無ければ作ります。
//
// **なぜページに分けるのか**（2026-09-13 ユーザー決定）。連絡先は
// メールアドレス・電話番号・氏名・役職…と**複数のタグが1人にぶら下がります**。
// 社名ページにタグを平らに積むと、6つ並んだ `メールアドレス` が**誰のものか
// 分からなくなり**（実データでそうなりました）、電話番号を足すこともできません。
// **まとめる器が要る**——w-cms の器はページです。
//
// **会社の口は社名ページのまま**です（`order@…`・`info@…` のように人ではないもの）。
// 人だと分かったものだけを分けます——分からないものを人のページにすると、
// 存在しない担当者が名簿に並びます。
func EnsureContactPerson(user *auth.User, companyID, name string) (string, error) {
	name = NormalizeNameForIngest(name)
	if name == "" {
		return "", errors.New("担当者の名前が空です")
	}
	boxID, err := ensureChildByTitle(user, companyID, ContactPersonBoxTitle)
	if err != nil {
		return "", err
	}
	return ensureChildByTitle(user, boxID, name)
}

// ensureChildByTitle は題の一致する子を返し、無ければ作ります。
//
// **完全一致だけ**です（`findChildByTitle` と同じ規律）——揺れを機械が吸収すると
// 別人が1人に潰れます。
func ensureChildByTitle(user *auth.User, parentID, title string) (string, error) {
	parentInt, err := strconv.Atoi(parentID)
	if err != nil {
		return "", err
	}
	var id int
	err = database.DB.QueryRow(
		`SELECT id FROM pages WHERE parent_id = ? AND title = ? ORDER BY id ASC LIMIT 1`,
		parentInt, title).Scan(&id)
	if err == nil {
		return fmt.Sprintf("%06d", id), nil
	}
	if !page.GetPerms(parentInt).CanWrite(user) {
		return "", errors.New("親ページへ書き込む権限がありません")
	}
	return CreateChildPage(parentID, user.Username,
		"<h1>"+stdhtml.EscapeString(title)+"</h1><p><br/></p>")
}

// PartnerRef は既にある相手ページ1枚（画面の選択肢に使います）。
type PartnerRef struct {
	ID    string
	Title string
}

// existingPartners は「取引先」の下にある相手ページを題の順で並べます（読めるものだけ）。
func existingPartners(user *auth.User) []PartnerRef {
	boxID, ok := PartnerBoxPageID()
	if !ok {
		return nil
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return nil
	}
	rows, err := database.DB.Query(
		`SELECT id, COALESCE(title, '') FROM pages WHERE parent_id = ? ORDER BY title ASC`, boxInt)
	if err != nil {
		return nil
	}
	type hit struct {
		id    int
		title string
	}
	// **先に読み切ってから絞ります**（page.CanView が別のクエリを投げるため）。
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

	var out []PartnerRef
	for _, h := range found {
		if h.title == "" || !page.CanView(user, h.id) {
			continue
		}
		out = append(out, PartnerRef{ID: fmt.Sprintf("%0*d", page.IDLength, h.id), Title: h.title})
	}
	return out
}
