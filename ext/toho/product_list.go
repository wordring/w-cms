package toho

// ─────────────────────────────────────────────────────────────────────────
// 加工製品の一覧（2026-09-29）
//
// 利用者:「加工製品のトップページにでも、**フィルターして表示する項目**があれば良いと
// 思います」。段のフォルダをやめて区分のタグにした（product_tree.go）ので、試作だけ・
// 見積もりだけを**見る**場所が要ります——フォルダを開けば並んでいた、の代わりです。
//
// **鏡です**（本文には `<section data-mirror="加工製品の一覧"></section>` だけ）。
// 置いたページの下にある加工製品ページを索引から毎回集めるので、整理で増えても・
// 区分を直しても、書き直す手間がありません。置き場は「取引先の加工製品」のテンプレート
// （社名の下の「加工製品」の箱）ですが、**どこに置いてもその下を集めます**（取引先の根に
// 置けば全社の分）。
//
// **絞り込みは画面の中だけ**で行います（`app.js` の「加工製品の一覧」）——1社の加工製品は
// 数百枚なので全部を描いて隠すほうが速く、打つたびにサーバーへ聞く理由がありません。
// ⚠ **欄はサーバーが描きます**（材料を探すと同じ理由——拡張を外したビルドに空の欄を残さない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"sort"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/database"
)

// ProductListViewType は「加工製品の一覧」の形式名です。
const ProductListViewType = "product-list"

// productListDepth は下へ辿る世代の上限です（加工製品／装置名称／品目／旧版 で足りる。
// 取引先の根に置いたときの 社名／加工製品 の2段も入れて余裕を持たせる）。
const productListDepth = 8

// productListRow は一覧の1行（加工製品ページ1枚）です。
type productListRow struct {
	PageID    int
	Title     string
	Machine   string // 親のページの題（装置名称）
	// DrawingNo・PartNo は**全部の値**を「・」で繋いだものです——1ページに図面が何枚もあれば図面番号も
	// その数だけあり（図面1枚ごとに図面ブロック・2026-09-29）、品番も2つ持つページがあります（利用者:
	// 「メーカーが常に品番を間違って送って来るものがあり、正しく送ってきたときに備えて正しい品番と間違った
	// 品番の両方で検索する必要がある」）。先頭だけ見せると、もう一方で絞ったときに当たらない。
	DrawingNo string
	PartNo    string
	Kinds     []string
	Migrating bool // `移行中` のタグがある（ワンノートからの移植で、人の確認待ち）
}

// productListTags は一覧が読むタグです。**加工製品ページかどうか**もこれで決めます
// ——図面・番号・品名のどれかを持つページ。
func productListTags() []string {
	names := []string{DrawingNoTag, DrawingNameTag, "品番", "品名", ProductKindTag, MigratingTag}
	return append(names, ProductCodeTags()...)
}

// productListRows は hostID の下にある加工製品ページを、装置名称・題の順で返します。
//
// ⚠ **改定で子ページへ移った旧版は数えません**——親も加工製品ページなら旧版です
// （`mergeAsRevision`・旧版は最新版の子）。数えると同じ品物が版の数だけ並びます。
func productListRows(user *auth.User, hostID int) ([]productListRow, error) {
	db := database.DB
	const sub = `WITH RECURSIVE sub(id, depth) AS (
		SELECT id, 1 FROM pages WHERE parent_id = ?
		UNION ALL
		SELECT p.id, sub.depth + 1 FROM pages p JOIN sub ON p.parent_id = sub.id
		 WHERE sub.depth < ?)`
	type pg struct {
		id, parent  int
		title, ptit string
	}
	// **先に読み切ってから絞ります**（行を読みながら別のクエリを投げない・:memory: の罠）。
	var pages []pg
	rows, err := db.Query(sub+`
		SELECT p.id, COALESCE(p.parent_id, 0), COALESCE(p.title, ''), COALESCE(par.title, '')
		  FROM sub JOIN pages p ON p.id = sub.id LEFT JOIN pages par ON par.id = p.parent_id`,
		hostID, productListDepth)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x pg
		if err := rows.Scan(&x.id, &x.parent, &x.title, &x.ptit); err != nil {
			rows.Close()
			return nil, err
		}
		pages = append(pages, x)
	}
	rows.Close()

	names := productListTags()
	marks := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	args := []any{hostID, productListDepth}
	for _, n := range names {
		args = append(args, n)
	}
	tags := map[int]map[string][]string{}
	trows, err := db.Query(sub+`
		SELECT t.page_id, t.name, t.value FROM page_tags t JOIN sub ON sub.id = t.page_id
		 WHERE t.name IN (`+marks+`) ORDER BY t.page_id, t.seq`, args...)
	if err != nil {
		return nil, err
	}
	for trows.Next() {
		var id int
		var name, value string
		if err := trows.Scan(&id, &name, &value); err != nil {
			trows.Close()
			return nil, err
		}
		if tags[id] == nil {
			tags[id] = map[string][]string{}
		}
		tags[id][name] = append(tags[id][name], value)
	}
	trows.Close()

	isProduct := func(id int) bool {
		t := tags[id]
		for _, n := range names {
			if n == ProductKindTag || n == MigratingTag {
				continue // 区分・移行中だけでは加工製品とは言えない
			}
			if len(t[n]) > 0 {
				return true
			}
		}
		return false
	}
	canView := viewCheck(user)
	var out []productListRow
	for _, p := range pages {
		if !isProduct(p.id) || isProduct(p.parent) || !canView(p.id) {
			continue
		}
		t := tags[p.id]
		row := productListRow{
			PageID:    p.id,
			Title:     p.title,
			DrawingNo: joinUnique(t[DrawingNoTag]),
			PartNo:    joinUnique(t["品番"]),
			Migrating: len(t[MigratingTag]) > 0,
		}
		if p.parent != hostID && canView(p.parent) {
			row.Machine = p.ptit
		}
		row.Kinds, _ = cleanProductKinds(t[ProductKindTag])
		if row.Kinds == nil && len(t[ProductKindTag]) > 0 {
			// 選択肢に無い値（設定から消した区分など）も、黙って落とさずそのまま見せます。
			row.Kinds = append([]string{}, t[ProductKindTag]...)
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Machine == "") != (b.Machine == "") {
			return b.Machine == "" // 装置の分からないものは最後
		}
		if a.Machine != b.Machine {
			return a.Machine < b.Machine
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.PageID < b.PageID
	})
	return out, nil
}

// productListViewHTML は「加工製品の一覧」を描きます（絞り込みの欄つき）。
func productListViewHTML(user *auth.User, pageIDInt int) string {
	head := `<h3 class="materials-title">🗂 加工製品の一覧</h3>`
	list, err := productListRows(user, pageIDInt)
	if err != nil {
		return head + `<p class="view-error">一覧を作れませんでした。</p>`
	}
	if len(list) == 0 {
		// ⚠ **0件も黙りません**——「無い」と「数えていない」を見分けるため。
		return head + `<p class="materials-empty">この下に加工製品のページはまだありません` +
			`（整理を実行すると、装置名称ごとにここへ並びます）。</p>`
	}
	esc := stdhtml.EscapeString
	var b strings.Builder
	b.WriteString(head)

	// ── 絞り込みの欄 ──
	machines := []string{}
	seen := map[string]bool{}
	for _, r := range list {
		if r.Machine != "" && !seen[r.Machine] {
			seen[r.Machine] = true
			machines = append(machines, r.Machine)
		}
	}
	b.WriteString(`<div class="matsearch-form plist-form" data-plist-form="1">`)
	b.WriteString(`<label class="matsearch-field"><span>文字で絞る</span>` +
		`<input type="search" class="matsearch-input plist-text" data-plist-text="1"` +
		` placeholder="品目・図面番号・品番"/></label>`)
	b.WriteString(`<label class="matsearch-field"><span>装置名称</span>` +
		`<select class="matsearch-input" data-plist-machine="1"><option value="">すべて</option>`)
	for _, m := range machines {
		b.WriteString(`<option value="` + esc(m) + `">` + esc(m) + `</option>`)
	}
	b.WriteString(`</select></label>`)
	// 区分は**印で選びます**（全部に印＝全部）。「通常」は区分の付いていないもの。
	b.WriteString(`<fieldset class="plist-kinds"><legend>区分</legend>`)
	kindBox := func(value, label string) {
		b.WriteString(`<label><input type="checkbox" data-plist-kind="` + esc(value) + `" checked/> ` +
			esc(label) + `</label>`)
	}
	kindBox("", "通常")
	for _, k := range ProductKinds() {
		kindBox(k, k)
	}
	b.WriteString(`</fieldset>`)
	b.WriteString(`<label class="plist-migrating-only"><input type="checkbox" data-plist-migrating="1"/>` +
		` 移行中（確認待ち）だけ</label>`)
	b.WriteString(`<span class="plist-count" data-plist-count="1">` + strconv.Itoa(len(list)) +
		` 件</span></div>`)

	// ── 表 ──
	b.WriteString(`<table class="materials-table plist-table"><thead><tr>` +
		`<th>装置名称</th><th>品目</th><th>図面番号</th><th>品番</th><th>区分</th></tr></thead><tbody>`)
	for _, r := range list {
		id := formatID(r.PageID)
		title := r.Title
		if title == "" {
			title = id
		}
		mig := ""
		if r.Migrating {
			mig = "1"
		}
		text := strings.Join([]string{r.Machine, title, r.DrawingNo, r.PartNo}, " ")
		b.WriteString(`<tr data-plist-row="1" data-machine="` + esc(r.Machine) + `" data-kinds="` +
			esc(strings.Join(r.Kinds, "\t")) + `" data-migrating="` + mig + `" data-text="` + esc(text) + `">`)
		b.WriteString(`<td class="plist-wrap">` + esc(r.Machine) + `</td>`)
		b.WriteString(`<td class="plist-wrap"><a href="/` + id + `">` + esc(title) + `</a>`)
		if r.Migrating {
			b.WriteString(` <span class="matsearch-migrating">（移行中）</span>`)
		}
		b.WriteString(`</td><td>` + esc(r.DrawingNo) + `</td><td>` + esc(r.PartNo) + `</td>` +
			`<td>` + esc(strings.Join(r.Kinds, "・")) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	b.WriteString(`<p class="materials-empty plist-none" data-plist-none="1" hidden>当てはまる加工製品はありません。</p>`)
	return b.String()
}

// joinUnique は値を重ならないように「・」で繋ぎます（空は落とす・並びは元のまま）。
func joinUnique(vals []string) string {
	seen := map[string]bool{}
	var out []string
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return strings.Join(out, "・")
}
