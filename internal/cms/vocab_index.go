package cms

import (
	"database/sql"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// ② 汎用索引（3層モデルの②。docs/【考察】語彙モデル.md §4）
//
// 全 <table data-type> / <dl data-type> を1つの縦持ちテーブル vocab_index へ
// 同期します（可変タグ page_tags テーブルの一般化として生まれ、2026-08-30 に本家を
// 吸収して唯一の行き先になった）。**コアに1実装**であり、形式（data-type）が
// 増えてもこのコードとDBスキーマは変わりません（完全正規化・縦持ちを選んだのは
// 検索の速さより定義変更への強さを優先する決定——同書 §9）。
//
// 駆動は既存のプラグイン機構（Register）へ相乗りし、**観察係**として引き金
// TriggerAll（マーカーのある要素すべて）を受け取ります（walk.go）。走査は自分では
// しません——コアの配送係が1回だけ歩き、当たった要素を届けます。
// これはドメインのユースケースプラグイン（③計算プラグイン）ではありません。
// 本文の語彙も所有しません（読むのはマーカー付きの標準HTMLだけ）。
//
// 鍵と型の決定は文書自身が携帯するスキーマ（見出し行）に従います（同書 §5.1）:
//   - 鍵 = 見出し（th / dt）の表示文字
//   - 型 = th の data-type 明示 > レジストリ宣言 > 語→型推論辞書 > text
// 正規化値（norm_value）は解釈できた値だけ**併記**し、生テキスト（value）が
// 常に正本です。未知の data-type もそのまま索引に載ります（オプトインの規約は
// data-type の有無だけ。素の table / dl は索引しません）。
//
// 「ファイルから再生成できないデータを持たせない」不変条件（アーキテクチャと
// DBスキーマ §8.1）は本テーブルにも適用されます——DELETE→INSERT の洗い替えのみ。
// ─────────────────────────────────────────────────────────────────────────

func init() {
	Register(vocabIndexPlugin{})
}

type vocabIndexPlugin struct{}

func (vocabIndexPlugin) Name() string { return "vocab_index" }

func (vocabIndexPlugin) Schema() []string {
	return []string{
		// 縦持ち1テーブル。行/フィールドの2テーブル分割は未決事項（語彙モデル §10）で、
		// v1 は最小の1テーブルから始める（(page_id, data_type, block_no, row_no, field) で
		// 1セルが1行になる）。
		// norm_num は number 型の値の**数値としての**正規化値。SQLite は TEXT 同士を
		// 文字列比較する（"8000" < "900"）ため、数の大小・範囲で絞る列は数値の
		// 格納クラスに分ける（【一覧】日付形式と数詞.md §5。2026-08-30 決定）。
		// norm_value（TEXT）は date の時系列比較と表示用にそのまま残る。
		`CREATE TABLE IF NOT EXISTS vocab_index (
			page_id INTEGER,
			data_type TEXT,
			block_no INTEGER,
			block_id TEXT,
			row_no INTEGER,
			field TEXT,
			value TEXT,
			norm_value TEXT,
			norm_num REAL,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_vocab_index_page ON vocab_index(page_id);`,
		`CREATE INDEX IF NOT EXISTS idx_vocab_index_type_field ON vocab_index(data_type, field);`,
		// 値の逆引き（「発注書番号が X のページ」「000002-12 を参照しているページ」）。
		// 生テキスト（value）が正本なので索引も生テキストに張る。norm_value 側は
		// date の範囲検索（納期 BETWEEN）用（アーキテクチャとDBスキーマ.md §9.1）。
		`CREATE INDEX IF NOT EXISTS idx_vocab_index_field_value ON vocab_index(field, value);`,
		`CREATE INDEX IF NOT EXISTS idx_vocab_index_field_norm ON vocab_index(field, norm_value);`,
	}
}

func (vocabIndexPlugin) Tables() []string { return []string{"vocab_index"} }

// Triggers はマーカーのある要素**すべて**を受け取ることを宣言します。
// 未知の `data-type` もそのまま索引に載る、という現行仕様がそのままこの1行になります。
func (vocabIndexPlugin) Triggers() []string { return []string{TriggerAll} }

// OnPageStart は当該ページ分を洗い流します（洗い替えの前半）。
func (vocabIndexPlugin) OnPageStart(ctx *ObserveContext) error {
	_, err := ctx.Tx.Exec(`DELETE FROM vocab_index WHERE page_id = ?`, ctx.PageID)
	return err
}

// OnElement はマーカー付きの table / dl と、形式を持つ section の素の中身を索引します。
// `section` の中のマーカー付き table / dl は配送係が別に届けてくれるので、ここでは降ります
// （＝ descend は常に true）。
func (vocabIndexPlugin) OnElement(ctx *ObserveContext, el *html.Node) (bool, error) {
	if el.Data == "section" {
		return true, syncVocabSection(ctx, el)
	}
	if el.Data != "table" && el.Data != "dl" {
		return true, nil // 素の table / dl は配送係が弾く。ここへ来るのは section 等
	}
	dataType := Attr(el, "data-type")
	// block_no は同一 data-type のブロックの文書順連番（同じ形式の表が
	// ページに複数あっても行を区別できるようにする）。
	no := ctx.Counter("vocab_index:" + dataType)
	def, _ := VocabDefByType(dataType) // 未定義でもゼロ値の def で続行（推論辞書だけ効く）

	if el.Data == "table" {
		return true, syncVocabTable(ctx.Tx, ctx.PageID, dataType, no, Attr(el, "data-id"), def, el)
	}
	return true, syncVocabDL(ctx.Tx, ctx.PageID, dataType, no, Attr(el, "data-id"), def, el)
}

// syncVocabSection は形式を持つ section の**素の中身**（data-type を持たない dl と table）を
// その形式で索引します。
//
// **素の dl / table は data-type を持ちません**（役割は包む section が宣言し、鍵は
// 見出しの表示文字。語彙モデル §8.2・§11.5-4）。配送係は引き金のある要素しか届けない
// ので、素の要素は誰の手にも渡りません——section の側から拾うのがここです。
//
// 拾う範囲は2段で広がった経緯があります:
//  1. D-1（2026-08-31）: ヘッダの素の dl。硬いドメイン表があったころは各プラグインが
//     自分で読んでいたため穴が見えず、索引へ一本化すると**発注元・発注日がどこにも
//     残らなくなる**欠落だった（TestSectionHeaderIsIndexed が固定）。
//  2. D-2（同日）: 素の table にも広げ、機能見出しのセクションを全部受けられる形に
//     した——`<section><h2>検査記録</h2><table>…` が属性ゼロで索引に載る。
//     ワンノートの「■見出しの下に表」がそのまま w-cms の形式宣言になる
//     （【考察】ワンノート移行.md §2.5）。
//
// 形式は vocabTypeOf で解決します——data-type 属性が正、無ければ機能見出し。
// マーカー付きの table / dl（可変タグ・明細表）は独立した形式として配送係が別に
// 届けるので、ここでは拾いません（拾うと同じ値が二重に索引されます）。
// 入れ子の section へは降りません（入れ子の業務ブロックは独立して読まれます）。
//
// block_no は形式の連番で、ヘッダ dl と素の表がそれぞれ1つずつ番号を取ります。
// マーカー付き明細表（client-order-items 等）は自分の data-type で別に数えられる
// ため、1つの section がヘッダ1つと明細表1つを持つ限り**同じ番号どうしが対**に
// なります（集計はこれで両者を結ぶ）。
func syncVocabSection(ctx *ObserveContext, section *html.Node) error {
	dataType := vocabTypeOf(section)
	def, _ := VocabDefByType(dataType)
	sectionBlockID := Attr(section, "data-id")

	// 素の表の読み方: 形式が明細（Items）を宣言していれば、素の表は**明細**である
	// ——「顧客の発注書」セクションの素の表は受注明細（2026-08-31 ユーザー:
	// 「発注書などの表はTableで組みましょう。THに表示される文字列が、すなわち
	// 列のデータを表します。人に対しても機械に対しても有効」）。
	// th の表示文字が列を、見出しの言葉が表の役割を宣言し、本文から機械語が消える。
	// 索引上は従来のマーカー付き明細と**同じ形式名**で載るので、集計は区別しない。
	itemsType, itemsDef := dataType, def
	if def.Items != "" {
		if idef, ok := VocabDefByType(def.Items); ok {
			itemsType, itemsDef = def.Items, idef
		}
	}

	var firstErr error
	eachPlainVocabChild(section, func(n *html.Node) {
		if firstErr != nil {
			return
		}
		switch n.Data {
		case "dl":
			no := ctx.Counter("vocab_index:" + dataType)
			firstErr = syncVocabDL(ctx.Tx, ctx.PageID, dataType, no, sectionBlockID, def, n)
		case "table":
			no := ctx.Counter("vocab_index:" + itemsType)
			// 素の表はブロックIDを持たない（振られるのはトップレベルだけ）ので、
			// 由来としては包んでいる section のIDを刻む。
			firstErr = syncVocabTable(ctx.Tx, ctx.PageID, itemsType, no, sectionBlockID, itemsDef, n)
		}
	})
	return firstErr
}

// eachPlainVocabChild は section の**素の中身**（data-type を持たない dl / table）を
// 文書順で fn へ渡します。マーカー付きは独立した形式（配送係が別に届ける）、
// 入れ子の section は独立した業務ブロックなので、どちらも渡しません。
// 索引（syncVocabSection）・種まき（template_new.go）・改名告知（vocab_notify.go）が
// 同じ切り分けを共有します——ここが割れると「索引には載るのに告知されない」形の
// ずれが生まれるため、巡回は1箇所に持ちます。
func eachPlainVocabChild(section *html.Node, fn func(n *html.Node)) {
	walkSkippingNested(section, map[string]bool{"section": true}, func(n *html.Node) {
		if Attr(n, "data-type") != "" {
			return
		}
		if n.Data == "dl" || n.Data == "table" {
			fn(n)
		}
	})
}

// vocabColumn は表の1列ぶんの解決済みスキーマ（文書の見出し行から読む）です。
type vocabColumn struct {
	key string
	typ ColumnType
}

// syncVocabTable は1つの表を索引へ書き込みます。
// 最初の tr が見出し行（列の鍵と型を運ぶ）、以降がデータ行です。
func syncVocabTable(tx *sql.Tx, pageID int, dataType string, blockNo int, blockID string, def VocabDef, table *html.Node) error {
	rows := tableRows(table)
	if len(rows) < 2 {
		return nil // 見出しだけ（またはデータ行なし）の表は索引に載せる値が無い
	}

	// 見出し行 → 列スキーマ（鍵と型）を解決する
	var cols []vocabColumn
	for _, cell := range rowCells(rows[0]) {
		key := strings.TrimSpace(nodeText(cell))
		cols = append(cols, vocabColumn{key: key, typ: resolveColumnType(cell, def, key)})
	}

	for r, row := range rows[1:] {
		for i, cell := range rowCells(row) {
			if i >= len(cols) || cols[i].key == "" {
				continue // 見出しの無い列は鍵が決まらないため索引できない
			}
			value := strings.TrimSpace(nodeText(cell))
			if err := insertVocabEntry(tx, pageID, dataType, blockNo, blockID, r, cols[i].key, cols[i].typ, value); err != nil {
				return err
			}
		}
	}
	return nil
}

// syncVocabDL は1つの <dl data-type> を索引へ書き込みます。
// dt が鍵（自由語）、後続の dd が値です。
//
// **タグは名前：値の1対**（2026-08-26 ユーザー決定「名前：値のタグは、値を複数持てません。
// 値を複数持てるのは、また別の形式だと思います」）。同じ名前が要るとき（担当者が2人など）は
// **対の繰り返し**で表す——各タグは1値のままなので決定と矛盾しない。
// 1 dt に複数 dd の形は形式外だが、来ても鍵の繰り返しとして寛容に読む（エコーバックの流儀）。
//
// 可変タグ（data_type="tags"）の行き先も**この索引だけ**です。かつては専用の
// page_tags テーブル（plugin_page_tags.go）が並走していたが、中身が完全に重複し
// **読む者が1人もいなかった**ため 2026-08-30（D-1）で吸収した。「親ページID」を
// 取り込まない旧ガードも同時に消えた——親はサイドカーが正本で、この語を親として
// 解釈するコードはもう無い（書けば普通のタグとして索引に載るだけ・不活性）。
func syncVocabDL(tx *sql.Tx, pageID int, dataType string, blockNo int, blockID string, def VocabDef, dl *html.Node) error {
	rowNo := 0
	var firstErr error
	eachDLPair(dl, false, func(key string, dd *html.Node) bool {
		if key == "" {
			return true // dt より前の dd は鍵が決まらない
		}
		typ := InferColumnType(key)
		if col, ok := def.columnFor(key); ok {
			typ = col.Type
		}
		value := strings.TrimSpace(nodeText(dd))
		if err := insertVocabEntry(tx, pageID, dataType, blockNo, blockID, rowNo, key, typ, value); err != nil {
			firstErr = err
			return false
		}
		rowNo++
		return true
	})
	return firstErr
}

// insertVocabEntry は1セル（1値）を索引へ書き込みます。正規化値は解釈できたときだけ併記します。
// number 型はさらに norm_num（REAL）へも入れ、大小・範囲の比較を数値で行えるようにします。
func insertVocabEntry(tx *sql.Tx, pageID int, dataType string, blockNo int, blockID string, rowNo int, field string, typ ColumnType, value string) error {
	var norm sql.NullString
	var normNum sql.NullFloat64
	if v, ok := NormalizeValue(typ, value); ok {
		norm = sql.NullString{String: v, Valid: true}
		if typ == ColNumber {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				normNum = sql.NullFloat64{Float64: f, Valid: true}
			}
		}
	}
	_, err := tx.Exec(
		`INSERT INTO vocab_index (page_id, data_type, block_no, block_id, row_no, field, value, norm_value, norm_num)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pageID, dataType, blockNo, blockID, rowNo, field, value, norm, normNum)
	return err
}

// resolveColumnType は列型を決定順序（th の data-type 明示 > レジストリ宣言 >
// 語→型推論辞書 > text）で解決します。
func resolveColumnType(headerCell *html.Node, def VocabDef, key string) ColumnType {
	if t := ColumnType(Attr(headerCell, "data-type")); validColumnTypes[t] {
		return t
	}
	if col, ok := def.columnFor(key); ok {
		return col.Type
	}
	return InferColumnType(key)
}

// tableRows は表の tr を文書順で返します。入れ子の表の tr は含めません
// （入れ子の <table data-type> は WalkElements が独立したブロックとして索引します）。
func tableRows(table *html.Node) []*html.Node {
	var rows []*html.Node
	walkSkippingNested(table, map[string]bool{"table": true}, func(n *html.Node) {
		if n.Data == "tr" {
			rows = append(rows, n)
		}
	})
	return rows
}

// rowCells は tr の直接の子である th / td を返します。
func rowCells(row *html.Node) []*html.Node {
	var cells []*html.Node
	for c := row.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "th" || c.Data == "td") {
			cells = append(cells, c)
		}
	}
	return cells
}

// eachDLPair は dl の「名前：値」の対を文書順で fn へ渡します。鍵（key）は
// **直前の dt の表示文字**（trim後）で、dt より前の dd は key="" のまま渡します
// （拾うか捨てるかは呼び出し側の責任）。fn が false を返すと打ち切ります。
//
// dt/dd を読む処理はすべてこの1関数を通ります（②索引・タグ・雛形の穴埋め・
// 改名告知の鍵集め）。かつては6箇所に同じ状態機械が写されており、「鍵は直前の
// dt」という規則を変えるとき全部を探して回る必要がありました。
//
// skipSection は入れ子の <section> の中へ降りるかです。形式の読み取り
// （VocabDLFields・dlHeadingKeys・freshenDL）は「入れ子の業務ブロックは独立して
// 読まれる」ため降りません（true）。タグと②索引の書き込みは従来どおり降ります
// （false・sanitize後の本文で dl の中に section が来ることは実際には無い）。
func eachDLPair(dl *html.Node, skipSection bool, fn func(key string, dd *html.Node) bool) {
	skip := map[string]bool{"dl": true, "table": true}
	if skipSection {
		skip["section"] = true
	}
	currentKey := ""
	stopped := false
	walkSkippingNested(dl, skip, func(n *html.Node) {
		if stopped {
			return
		}
		switch n.Data {
		case "dt":
			currentKey = strings.TrimSpace(nodeText(n))
		case "dd":
			if !fn(currentKey, n) {
				stopped = true
			}
		}
	})
}

// walkSkippingNested は root の子孫要素を文書順で走査します。ただし skip に挙げた
// 要素の**内側へは降りません**（root 自身は走査対象外）。
func walkSkippingNested(root *html.Node, skip map[string]bool, fn func(*html.Node)) {
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if skip[c.Data] {
			continue
		}
		fn(c)
		walkSkippingNested(c, skip, fn)
	}
}

// ── ③計算プラグイン向けの汎用読み取りヘルパ（移行第3段） ──────────────
// 文書自身が携帯するスキーマ（見出し行・dt）を機械キーへ解決して値を取り出す。
// 鍵の決定は②と同じ規則（見出しの表示文字→レジストリの Label 経由で Field へ）。

// VocabDLFields は dl の項目を機械キー→値の表として返します。
func VocabDLFields(dl *html.Node, def VocabDef) map[string]string {
	out := map[string]string{}
	if dl == nil {
		return out
	}
	eachDLPair(dl, true, func(key string, dd *html.Node) bool {
		if col, ok := def.columnFor(key); ok && col.Field != "" {
			key = col.Field
		}
		if key != "" {
			if _, dup := out[key]; !dup { // 多値は先頭を採る（TagValue と同じ）
				out[key] = strings.TrimSpace(nodeText(dd))
			}
		}
		return true
	})
	return out
}

// VocabTableRows は表のデータ行を機械キー→値の表の列として返します。
func VocabTableRows(table *html.Node, def VocabDef) []map[string]string {
	if table == nil {
		return nil
	}
	rows := tableRows(table)
	if len(rows) < 2 {
		return nil
	}
	fields := make([]string, 0, 8)
	for _, cell := range rowCells(rows[0]) {
		key := strings.TrimSpace(nodeText(cell))
		field := ""
		if col, ok := def.columnFor(key); ok {
			field = col.Field
		}
		fields = append(fields, field)
	}
	var out []map[string]string
	for _, row := range rows[1:] {
		values := map[string]string{}
		for i, cell := range rowCells(row) {
			if i < len(fields) && fields[i] != "" {
				values[fields[i]] = strings.TrimSpace(nodeText(cell))
			}
		}
		out = append(out, values)
	}
	return out
}

// FirstVocabChild は root 配下から最初の element[data-type==dataType]（dataType が
// 空なら data-type を問わない element）を返します。入れ子の section へは降りません
// （入れ子の業務ブロックは独立して読まれるため）。
func FirstVocabChild(root *html.Node, element, dataType string) *html.Node {
	if root == nil {
		return nil
	}
	var found *html.Node
	walkSkippingNested(root, map[string]bool{"section": true}, func(n *html.Node) {
		if found != nil || n.Data != element {
			return
		}
		if dataType == "" || Attr(n, "data-type") == dataType {
			found = n
		}
	})
	return found
}

// nodeText は要素配下のテキストを連結して返します（表示文字＝値。語彙モデル §2）。
func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}
