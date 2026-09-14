package cms

// ─────────────────────────────────────────────────────────────────────────
// 未分類の連絡先——メールから相手を拾い、社名を推し量る（contacts.go から分離）
//
// **正本は contacts.go の冒頭**（なぜ相手のページが1枚なのか、なぜ箱を役割で
// 割らないのか）。ここが持つのは「まだページになっていないアドレスを集めて、
// 行き先の当たりを付ける」ところだけです。
//
// **推し量るのは社名だけで、決めません**——ドメインから引いた名前は推薦として
// 出し、人が確かめます（2026-09-11 ユーザー:「ドメインから社名が推測できる場合は、
// 社名を推薦する程度にします」）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"fmt"
	"sort"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// addressFields は索引からアドレスを拾う項目です。取り込みが書いている名前
// （intake_eml.go の writeAddressTags）とそろえること。
//
// **1人1タグになりました**（2026-09-13）。値は `名前 <アドレス>` で、畳んだ値
// （`norm_value`）がアドレスだけ——引くのはそちらです。もとは `差出人` と
// `差出人アドレス` の2つで、**位置で対応づけて**いました（`CCアドレス` の持ち主は
// 1つ手前の `CC`）。その仕掛けはまるごと要らなくなりました。
var addressFields = []string{"差出人", "宛先", "CC", "返信先"}

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

	// **位置合わせは要らなくなりました**（2026-09-13）。1タグが1人を表し、
	// 畳んだ値（`norm_value`）がアドレス、生の値（`value`）が `名前 <アドレス>` です
	// ——同じ行に両方あるので、対応づけそのものが消えました。
	//
	// もとは `差出人` と `差出人アドレス` を**隣接で対応づけて**いて、CCが3人いると
	// 名前がずれていました（3人とも先頭の名前になる。同日に実データで発見）。
	rows, err := database.DB.Query(
		`SELECT page_id, value, COALESCE(norm_value, '') FROM page_tags
		  WHERE name IN (`+sqlPlaceholders(len(addressFields))+`)`,
		toAnySlice(addressFields)...)
	if err != nil {
		return nil, err
	}
	// **先に読み切ってから解釈します**（行を読みながら別のクエリを投げない）。
	type hit struct {
		pageID int
		value  string
		norm   string
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.pageID, &h.value, &h.norm); err != nil {
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
		addr := normalizeEmail(h.norm)
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
		// 表示名は**同じ行の生の値**から取れます（`名前 <アドレス>` の前半）。
		if n := displayNameOf(h.value); n != "" {
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
		`SELECT v.page_id, v.value FROM page_tags v WHERE v.name = ?`, EmailTag)
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
		`SELECT page_id, value FROM page_tags WHERE name = ?`, EmailTag)
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

// displayNameOf は `名前 <アドレス>` の前半（表示名）を返します（無ければ空）。
//
// **飾りのほうです**——引くのに使うのは畳んだ値（アドレス）で、これは人が
// 「誰のことか」を見分けるためだけに出します。名前しか書かれていないヘッダ
// （アドレス無し）では、その名前がそのまま返ります。
func displayNameOf(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.LastIndex(s, "<"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	// 取り込みが引用符ごと拾うことがある（`'南 公一'`）。表示のためだけなので落とす。
	s = strings.Trim(s, `'"`)
	if strings.Contains(s, "@") {
		return "" // アドレスしか無い＝表示名は無い
	}
	return s
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
