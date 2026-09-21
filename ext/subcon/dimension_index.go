package subcon

// ─────────────────────────────────────────────────────────────────────────
// 分解した寸法を、**別の表**に入れる（2026-09-21）
//
// ユーザー:「これは**汎用の表検索とは別のDB**に入れることになると思います」。
// ⚠ その見立ては正しく、理由は速さではありません——`vocab_index` の `field` は
// **見出しの表示文字が正本**です。そこへ `厚み` を足すと「**本文に無い列が索引に
// ある**」ことになり、「見た儘が全て」が崩れます。別表なら
// **「`寸法` という1つのセルから導いた値」**と役割がはっきりします。
//
// ⚠ **導出なので、本文が変われば作り直します**（洗い替え）。
// ⚠ **自分で立てる表です**——`vocab_index` の `block_no` とは**揃いません**。
// 揃える設計にすると、コアが数え方を変えた日に**黙ってずれます**。だから
// 材質・形状・寸法を**この表の中に持ち**、外部への結合を要らなくしてあります。
// ─────────────────────────────────────────────────────────────────────────

import (
	"database/sql"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
)

func init() { cms.Register(dimensionPlugin{}) }

// dimensionTable はこのプラグインが所有する表です。
const dimensionTable = "material_dimensions"

// 分解の対象にする形式です。⚠ **`寸法` の列を持つものだけ**——外注加工には
// 寸法の列がありません（`加工内容` で指す）。
var dimensionTypes = []string{partMaterialsType, ourOrderItemsType}

type dimensionPlugin struct{}

func (dimensionPlugin) Name() string { return "material-dimensions" }

func (dimensionPlugin) Tables() []string { return []string{dimensionTable} }

// Schema は表と索引を作ります。
//
// ⚠ **`num` に宣言型を置きません**（`page_tags.norm_value` と同じ判断・2026-09-15）。
// SQLite は宣言の無い列で**値ごとの格納クラス**を保つので、整数は整数のまま入ります
// （`REAL` だと `3` が `3.0` になる）。並びも比較も数として効きます。
func (dimensionPlugin) Schema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS ` + dimensionTable + ` (
			page_id   INTEGER NOT NULL,
			data_type TEXT    NOT NULL,
			block_no  INTEGER NOT NULL,
			row_no    INTEGER NOT NULL,
			seq       INTEGER NOT NULL,
			material  TEXT    NOT NULL,
			shape     TEXT    NOT NULL,
			size      TEXT    NOT NULL,
			role      TEXT    NOT NULL,
			num,
			raw       TEXT    NOT NULL,
			PRIMARY KEY (page_id, data_type, block_no, row_no, seq)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_dim_role_num ON ` + dimensionTable + `(role, num)`,
	}
}

// Triggers は担当する形式です。
func (dimensionPlugin) Triggers() []string { return dimensionTypes }

// OnPageStart は洗い替えの前半（このページぶんを消す）です。
func (dimensionPlugin) OnPageStart(ctx *cms.ObserveContext) error {
	_, err := ctx.Tx.Exec(`DELETE FROM `+dimensionTable+` WHERE page_id = ?`, ctx.PageID)
	return err
}

// OnElement は表を1つ読んで、寸法の列を分解して入れます。
//
// ⚠ **節と表の両方が引き金になります。** 表が `<caption>` や `data-type` で自分から
// 名乗っていれば**配送係が表そのものを届ける**ので、節のときは**素の表だけ**を拾います
// ——判定は `cms.VocabTypeOf` で、**コアと同じ口**を通します。
// ⚠ 揃えないと**同じ行が二重に入ります**（同日、コア自身がその不具合を踏みました）。
func (p dimensionPlugin) OnElement(ctx *cms.ObserveContext, el *html.Node) (bool, error) {
	dataType := cms.VocabTypeOf(el)
	switch el.Data {
	case "table":
		return false, p.indexTable(ctx, dataType, el)
	case "section":
		var firstErr error
		for _, t := range plainTablesIn(el) {
			if firstErr != nil {
				break
			}
			firstErr = p.indexTable(ctx, dataType, t)
		}
		return true, firstErr
	}
	return true, nil
}

// plainTablesIn は節の中の**素の表**（配送係が自分では届けない表）を返します。
func plainTablesIn(section *html.Node) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if c.Data == "section" {
				continue // 入れ子の節は独立した業務ブロック
			}
			if c.Data == "table" {
				if cms.VocabTypeOf(c) == "" {
					out = append(out, c)
				}
				continue
			}
			walk(c)
		}
	}
	walk(section)
	return out
}

// indexTable は1つの表の寸法を分解して書き込みます。
func (dimensionPlugin) indexTable(ctx *cms.ObserveContext, dataType string, table *html.Node) error {
	rows := rowsOf(table)
	if len(rows) < 2 {
		return nil // 見出しだけの表には分解するものがない
	}
	mi := headerIndexOf(rows[0], "材質")
	si := headerIndexOf(rows[0], "形状")
	zi := headerIndexOf(rows[0], "寸法")
	if zi < 0 {
		return nil // ⚠ **`寸法` の列が無ければ何もしません**（分解する元がない）
	}
	blockNo := ctx.Counter(dimensionTable + ":" + dataType)

	for rowNo, tr := range rows[1:] {
		size := strings.TrimSpace(cellText(tr, zi))
		parts := ParseDimension(size)
		if len(parts) == 0 {
			continue
		}
		material := cms.NormalizeText(strings.TrimSpace(cellText(tr, mi)))
		shape := cms.NormalizeText(strings.TrimSpace(cellText(tr, si)))
		for _, p := range parts {
			var num any
			if p.HasNum() {
				// ⚠ **整数は整数の格納クラスへ**（`3` を `3.0` にしない）。
				if p.Num == float64(int64(p.Num)) {
					num = int64(p.Num)
				} else {
					num = p.Num
				}
			}
			if _, err := ctx.Tx.Exec(
				`INSERT OR REPLACE INTO `+dimensionTable+
					` (page_id, data_type, block_no, row_no, seq, material, shape, size, role, num, raw)
				  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				ctx.PageID, dataType, blockNo, rowNo, p.Seq,
				material, shape, size, p.Role, num, p.Raw); err != nil {
				return err
			}
		}
	}
	return nil
}

// dimensionRowsOf はそのページの分解結果を読みます（試験と診断のため）。
func dimensionRowsOf(db *sql.DB, pageID int) ([]DimPart, error) {
	rows, err := db.Query(
		`SELECT seq, role, COALESCE(num, 0), raw FROM `+dimensionTable+
			` WHERE page_id = ? ORDER BY data_type, block_no, row_no, seq`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DimPart
	for rows.Next() {
		var p DimPart
		if err := rows.Scan(&p.Seq, &p.Role, &p.Num, &p.Raw); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
