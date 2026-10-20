package toho

// 整理の候補を引く（2026-10-20 に filing.go から分けた）——同じ図面の加工製品ページ・推奨の社名（差出人の連絡帳から）・社名と装置名の候補。

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// revSuffix は図面番号の末尾の版の印です（`rev1`・`_rev0`・`Rev.2`）——改定の図面は番号にこれが付くので外して比べる。
var revSuffix = regexp.MustCompile(`(?i)[\s_\-]*rev\.?\s*\d+$`)

// sameDrawingKey は図面番号を比べる形にします（版の印を外し、区切り・大小を畳む）。
func sameDrawingKey(no string) string {
	no = strings.TrimSpace(cms.NormalizeNameForIngest(no))
	return cms.NormalizeCode(revSuffix.ReplaceAllString(no, ""))
}

// productCandidates は取引先の社名のフォルダの下（2026-10-16 までは社名の下の「加工製品」の箱の下）から、番号（図面番号・品番——版の印は除く）と名前（題・品名・図面名称）の両方が
// 合うページを探し、無ければどちらかが合うページを返します（5件まで・読めるものだけ・旧版の子ページは除く）。
func productCandidates(user *auth.User, customer, drawingNo, name string) []productCandidate {
	out := []productCandidate{}
	boxID, ok := CustomerBoxPageID()
	if customer == "" || !ok {
		return out
	}
	partnerID, found := findFolderChild(boxID, customer)
	if !found {
		return out
	}
	// 社名の下を全部探す（2026-10-16 に「加工製品」の箱をなくした——装置の下にも、社名の直下にも加工製品がある）。
	hostID, err := strconv.Atoi(partnerID)
	if err != nil {
		return out
	}
	rows, err := productListRows(user, hostID)
	if err != nil {
		return out
	}
	want := sameDrawingKey(drawingNo)
	wantName := cms.NormalizeText(name)
	// **まず番号と名前の両方が合うページ、無ければどちらかが合うページ**（2026-10-01 利用者:「検索や結びの照合は、
	// 最初に品番と品名で行うべきです。一致しない場合に、ほかの方法を試せば良いと思います」）。番号は図面番号と品番、
	// 名前は題と、ページの品名・図面名称（改定で品名が変わると品名が2つ残る）。
	// 旧版の子ページは `productListRows` が既に外している（加工製品ページの子は一覧に入らない）。
	var both, either []productCandidate
	for _, r := range rows {
		nameHit := wantName != "" && cms.NormalizeText(r.Title) == wantName
		for _, n := range r.Names {
			if !nameHit && wantName != "" && cms.NormalizeText(n) == wantName {
				nameHit = true
			}
		}
		noHit := false
		if want != "" {
			for _, no := range strings.Split(r.DrawingNo+"・"+r.PartNo, "・") {
				if no != "" && sameDrawingKey(no) == want {
					noHit = true
					break
				}
			}
		}
		c := productCandidate{PageID: page.FormatID(r.PageID), Title: r.Title, Machine: r.Machine, DrawingNos: r.DrawingNo}
		if nameHit && noHit {
			both = append(both, c)
		} else if nameHit || noHit {
			either = append(either, c)
		}
	}
	if len(both) == 0 {
		both = either
	}
	if len(both) > 5 {
		both = both[:5]
	}
	return append(out, both...)
}

// drawingNosOf は本文に載っている図面番号を並べます（図面ブロックごとに1つ）。
func drawingNosOf(body string) []string {
	out := []string{}
	for _, sec := range drawingSectionsOf(body) {
		if no := strings.TrimSpace(drawingNoOf(sec)); no != "" {
			out = append(out, no)
		}
	}
	return out
}

// suggestCustomer は顧客名の推奨値を返します。
//
// **読んだ名前ではなく、既にある取引先ページの題を第一にします**（2026-09-06
// ユーザー:「社名の揺れは、エイリアスの表かAIでなくせませんか？」）。揺れは
// 「機械が読んだ名前を人が打ち写す」ところで生まれるので、**打ち写す元を変えます**:
//
//	加工製品ページ → `受信元` タグ → 通信記録 → `差出人アドレス` → 取引先ページ → その題
//
// この鎖は**全部が完全一致**で、推測が1つも入りません。エイリアスの表もAIも
// 要らないのは、**同一性を名前で決めていない**からです。
//
// 引けなければ読めた名前（Geminiが図面から読んだ客先）へ戻ります——新しい顧客の
// 1通目はまだ取引先に居ないのが正常で、そのときは人が打ちます。
func suggestCustomer(user *auth.User, productPageID int, read string) string {
	if addr := senderAddressOf(productPageID); addr != "" {
		if title, ok := contacts.PartnerTitleForAddress(user, addr); ok {
			return title
		}
	}
	// **鎖が切れたら、名前で連絡帳を当たります**（2026-09-20 ユーザー:「株式会社や
	// 有限会社、（株）などを無くした社名が連絡帳にあれば、それを提案するような形で、
	// 編集者の承認を得てはどうでしょう？」）。
	//
	// 鎖が切れるのは珍しくありません——新しい客先の1通目・社内からの転送・
	// FAXだけの相手。そこで生の `株式会社南北…` へ落ちると、連絡帳に
	// `南北…` が居るのに**2枚目の会社ページができます**。
	//
	// ⚠ **これは提案で、決めるのは人**です。欄は編集できるコンボボックスで、
	// 読んだ名前も候補の一覧に並びます（`partners`）。候補が2つ以上に割れたときは
	// `OrgNameForPage` が黙って読んだ名前を返します——`株式会社あさひ` と
	// `有限会社あさひ` は**実在しうる別会社**なので、機械が選んではいけません。
	// 連絡帳にまだ居なければ**法人格を落とした形**を提案します（2026-09-21）。
	return contacts.OrgNameForPage(user, read)
}

// senderAddressOf は加工製品ページの由来（`受信元`）をたどり、通信記録の差出人アドレスを返します。
//
// **由来のタグを見ます**（親ではなく）——加工製品ページは整理で動きますが、`受信元` は
// 動きません。値は「ページID-添付ID」なので、ハイフンの前だけ使います。
func senderAddressOf(productPageID int) string {
	ref := strings.TrimSpace(cms.PageTagValue(database.DB, productPageID, SourceRefTag))
	if i := strings.Index(ref, "-"); i > 0 {
		ref = ref[:i]
	}
	srcID, err := strconv.Atoi(ref)
	if err != nil {
		return ""
	}
	return recordSenderAddress(srcID)
}

// recordSenderAddress は通信記録のページの差出人のアドレスです（無ければ空）。
func recordSenderAddress(srcID int) string {
	var addr string
	database.DB.QueryRow(
		// **畳んだ値がアドレス**（生の値は `名前 <アドレス>`）。2026-09-13 に1人1タグへ。
		// 名前は**書き手の定数**を通します（2026-09-16）——下請けは通信記録の
		// 中身を直接読んでいるので、通信側で欄の名前が変わると**顧客名の推奨が
		// 黙って空になります**（エラーにはならない）。
		`SELECT COALESCE(norm_value, value) FROM page_tags
		  WHERE page_id = ? AND name = ? LIMIT 1`,
		srcID, comm.FromTag).Scan(&addr)
	return strings.TrimSpace(addr)
}

// partnerNames は「製造台帳」の下にある相手ページの題を並べます（読めるものだけ）。
//
// 整理の画面の入力補助です。**選ばせるのではなく、候補として見せる**だけ——
// 新しい顧客の1枚目はここに無いので、打てなくしてはいけません。
func partnerNames(user *auth.User) []string {
	out := []string{}
	for _, c := range visibleCustomerChildren(user) {
		out = append(out, c.Title)
	}
	sort.Strings(out)
	return out
}

// visibleCustomerChildren は `製造台帳` の直下（社名ページ）のうち、読めて題のあるものを
// ID順で返します（整理の候補と、連絡帳と未接続の相手の一覧が共有します）。
func visibleCustomerChildren(user *auth.User) []cms.ChildPage {
	boxID, ok := CustomerBoxPageID()
	if !ok {
		return nil
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return nil
	}
	kids, err := cms.ChildPages(database.DB, boxInt)
	if err != nil {
		return nil
	}
	var out []cms.ChildPage
	for _, k := range kids {
		if k.Title != "" && page.CanView(user, k.ID) {
			out = append(out, k)
		}
	}
	return out
}

// machineNames は、既にある装置名称を**顧客ごと**に返します（画面の候補用）。
//
// 木は `製造台帳／社名／装置名称` なので、装置は顧客の**子**です（2026-10-16 に「加工製品」の箱をなくした。
// それまでは孫・2026-09-29 までは真ん中が段〔現行・旧型・試作〕でした）。
//
// ⚠ **ただし「顧客の子」だけでは広すぎます。** 社名の直下には加工製品ページも並びます（装置名の分からない品物）。
// だから **`ページ種類：装置` のフォルダか、`ページ種類` を持たないページ（人が作ったフォルダ）だけ**にします。
// 加工製品ページ・ほかの種類のページは候補に出しません。かつて社名の子に `担当者`（窓口の人を集める箱）があり、
// 絞らないと人の名前が装置の候補に出ました（2026-09-14 に実データで発見）——名指しで除くやり方は採りません
// （箱が増えるたびに除外が増え、いつか漏れます）。
//
// **候補を出すだけで、合わせるのは人**です。完全一致でしか階層は繋がらないので
// （findFolderChild）、揺れを機械が吸収すると別の装置が1つに潰れます。
func machineNames(user *auth.User) map[string][]string {
	out := map[string][]string{}
	boxID, ok := CustomerBoxPageID()
	if !ok {
		return out
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return out
	}
	// **2世代を1回のクエリで取ります**。行を読みながら別のクエリを投げると
	// `:memory:` DBでカーソルが接続を握ったままになり、**絞り込みが静かに全部落ちます**
	// （2026-09-03 に本番コードで踏んだ罠）。
	rows, err := database.DB.Query(`
		SELECT cust.id, COALESCE(cust.title, ''), mach.id, COALESCE(mach.title, '')
		  FROM pages cust
		  JOIN pages mach ON mach.parent_id = cust.id
		 WHERE cust.parent_id = ?
		   AND NOT EXISTS (SELECT 1 FROM page_tags t WHERE t.page_id = mach.id AND t.name = ? AND t.value <> ?)
		 ORDER BY cust.title ASC, mach.title ASC`, boxInt, PageKindTag, PageKindMachine)
	if err != nil {
		return out
	}
	type hit struct {
		custID int
		cust   string
		machID int
		mach   string
	}
	var found []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.custID, &h.cust, &h.machID, &h.mach); err != nil {
			rows.Close()
			return out
		}
		found = append(found, h)
	}
	rows.Close()

	seen := map[string]map[string]bool{}
	for _, h := range found {
		if h.cust == "" || h.mach == "" {
			continue
		}
		// **読めないページの装置は数えません**（見せ分けC案——黙って落ちる）。
		if !page.CanView(user, h.custID) || !page.CanView(user, h.machID) {
			continue
		}
		if seen[h.cust] == nil {
			seen[h.cust] = map[string]bool{}
		}
		if seen[h.cust][h.mach] {
			continue // 同じ題の社名のフォルダが2枚あると、同じ装置名が2回来る
		}
		seen[h.cust][h.mach] = true
		out[h.cust] = append(out[h.cust], h.mach)
	}
	return out
}
