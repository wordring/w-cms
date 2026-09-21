package subcon

// ─────────────────────────────────────────────────────────────────────────
// 材質・形状・寸法で材料を探す（2026-09-21）
//
// ユーザー:「あとで**材質形状寸法で検索**したいときがあります」。
//
// 探しているのは**材料そのもの**ではなく、たいてい**いくらだったか**です
// （「この前の t3.2 の角パイプ、いくらで買ったっけ」）。だから結果には
// **単価・時点・仕入先**を添えます——[material_price.go] と同じ引き方です。
//
// ⚠ **読めないページは混ぜません**（`page.CanView`）。材料の表には誰でも行を
// 書けるので、絞らないと**読めない発注書の単価と仕入先が引けてしまいます**。
// ─────────────────────────────────────────────────────────────────────────

import (
	"sort"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// MaterialQuery は探し方です。**指定した条件をすべて満たす**行が出ます（AND）。
type MaterialQuery struct {
	Material  string    // 材質（畳んだ部分一致）
	Shape     string    // 形状（畳んだ部分一致）
	Thickness float64   // 厚み（一致・0 は指定なし）
	Diameter  float64   // 径（一致・0 は指定なし）
	Values    []float64 // 寸法値（**すべて含む**こと。役割は問わない）
}

// Empty は条件が1つも無いかを返します。
//
// ⚠ **条件ゼロで全件は返しません。** 空の鍵で引き当てない、と同じ判断です
// ——「何も打たずに押した」と「全部見たい」は別物で、前者のほうが桁違いに多い。
func (q MaterialQuery) Empty() bool {
	return strings.TrimSpace(q.Material) == "" && strings.TrimSpace(q.Shape) == "" &&
		q.Thickness == 0 && q.Diameter == 0 && len(q.Values) == 0
}

// MaterialHit は見つかった材料の1行です。
type MaterialHit struct {
	PageID    int    `json:"page_id"`
	PageTitle string `json:"page_title"`
	Kind      string `json:"kind"`     // 材料 / 発注明細
	Material  string `json:"material"` // 材質
	Shape     string `json:"shape"`    // 形状
	Size      string `json:"size"`     // 寸法（書かれたまま）
	Cost      int    `json:"cost"`     // 最新単価（引けなければ 0）
	At        string `json:"at"`       // その単価の時点
	Supplier  string `json:"supplier"` // その単価の仕入先
	Migrating bool   `json:"migrating"`
}

// dimKey は分解結果を束ねる鍵（1つの材料行）です。
type dimKey struct {
	page                  int
	dataType              string
	block, row            int
	material, shape, size string
}

// dimRow は分解の1片（行の鍵つき）です。
type dimRow struct {
	key  dimKey
	role string
	num  float64
}

// SearchMaterials は条件に合う材料の行を返します。
//
// ⚠ **`移行中` のページも返します**（印を付けて）。探しているのは**書いてあるもの**で、
// 確認前でも「在ること」は本当です。⚠ **単価は引きません**——そちらは表引きなので、
// `material_price.go` と同じ関門に従います。
func SearchMaterials(viewer *auth.User, q MaterialQuery) ([]MaterialHit, error) {
	if q.Empty() {
		return nil, nil
	}
	db := database.DB
	rows, err := db.Query(
		`SELECT page_id, data_type, block_no, row_no, material, shape, size, role, num, raw
		   FROM ` + dimensionTable + `
		  ORDER BY page_id, data_type, block_no, row_no, seq`)
	if err != nil {
		return nil, err
	}
	// ⚠ **先に読み切ってから絞ります**——行を読みながら `page.CanView` を投げると、
	//    `:memory:` DBでは空の別DBに当たり、絞り込みが静かに全部落ちます。
	var all []dimRow
	for rows.Next() {
		var p dimRow
		var raw string
		var num *float64
		if err := rows.Scan(&p.key.page, &p.key.dataType, &p.key.block, &p.key.row,
			&p.key.material, &p.key.shape, &p.key.size, &p.role, &num, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if num != nil {
			p.num = *num
		}
		all = append(all, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 行ごとに束ねる
	grouped := map[dimKey][]dimRow{}
	var order []dimKey
	for _, p := range all {
		if _, seen := grouped[p.key]; !seen {
			order = append(order, p.key)
		}
		grouped[p.key] = append(grouped[p.key], p)
	}

	visible := map[int]bool{}
	canView := func(id int) bool {
		if v, ok := visible[id]; ok {
			return v
		}
		v := page.CanView(viewer, id)
		visible[id] = v
		return v
	}
	prices, err := latestMaterialPrices(db, viewer)
	if err != nil {
		prices = map[string]materialPrice{} // ⚠ 単価が引けなくても検索は返す
	}

	var out []MaterialHit
	for _, k := range order {
		if !canView(k.page) {
			continue
		}
		if !matchesQuery(k, grouped[k], q) {
			continue
		}
		hit := MaterialHit{
			PageID: k.page, PageTitle: cms.PageTitleByID(k.page),
			Kind: displayNameOf(k.dataType), Material: k.material,
			Shape: k.shape, Size: k.size,
			Migrating: isMigrating(db, k.page),
		}
		if p, ok := prices[materialKeyOf(k.material, k.shape, k.size)]; ok {
			hit.Cost, hit.At, hit.Supplier = p.Cost, p.Date, p.Supplier
		}
		out = append(out, hit)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].PageID < out[j].PageID })
	return out, nil
}

// matchesQuery は1つの材料行が条件を満たすかを返します（**すべて AND**）。
//
// ⚠ **材質と形状は畳んだ部分一致**です——`鉄` で `鉄STPG370EG` に当てたい。
// ⚠ **寸法値は「含む」**（役割は問わない）——`□75*75*t3.2*1090` の 1090 が
// 長さなのか幅なのかを**機械は知らない**ので、順不同の集合として扱います。
func matchesQuery(k dimKey, parts []dimRow, q MaterialQuery) bool {
	if s := strings.TrimSpace(q.Material); s != "" &&
		!strings.Contains(k.material, cms.NormalizeText(s)) {
		return false
	}
	if s := strings.TrimSpace(q.Shape); s != "" &&
		!strings.Contains(k.shape, cms.NormalizeText(s)) {
		return false
	}
	has := func(role string, want float64) bool {
		for _, p := range parts {
			if p.role == role && sameDim(p.num, want) {
				return true
			}
		}
		return false
	}
	if q.Thickness != 0 && !has(RoleThickness, q.Thickness) {
		return false
	}
	if q.Diameter != 0 && !has(RoleDiameter, q.Diameter) {
		return false
	}
	for _, v := range q.Values {
		// ⚠ **役割を問いません**——`角`・`径`・`寸法値` のどれでも、その数が
		//    書かれていれば当てます（`□75` の 75 を「75 で探す」人は普通です）。
		found := false
		for _, p := range parts {
			if p.role != RoleMark && sameDim(p.num, v) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// sameDim は寸法の数が同じかを返します。
//
// ⚠ **浮動小数の直接比較をしません**（`3.2` は2進数で割り切れません）。
// 実務の寸法は 0.1 刻みなので、0.001 まで同じなら同じとみなします。
func sameDim(a, b float64) bool {
	d := a - b
	return d < 0.001 && d > -0.001
}
