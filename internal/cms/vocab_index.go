package cms

import (
	"database/sql"
	"strings"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// ② 汎用索引（3層モデルの②。docs/【考察】語彙モデル.md §4）
//
// 全 <table data-type> / <dl data-type> を1つの縦持ちテーブル vocab_index へ
// 同期します（page_tags の一般化）。**コアに1実装**であり、形式（data-type）が
// 増えてもこのコードとDBスキーマは変わりません（完全正規化・縦持ちを選んだのは
// 検索の速さより定義変更への強さを優先する決定——同書 §9）。
//
// 同期の駆動には既存のプラグイン機構（Register / Sync）へ相乗りしますが、
// これはドメインのユースケースプラグイン（③計算プラグイン）ではありません。
// カスタム要素も所有しません（Tags は nil）。
//
// 鍵と型の決定は文書自身が携帯するスキーマ（見出し行）に従います（同書 §5.1）:
//   - 鍵 = data-field があればその値、無ければ見出し（th / dt）のテキスト
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
		`CREATE TABLE IF NOT EXISTS vocab_index (
			page_id INTEGER,
			data_type TEXT,
			block_no INTEGER,
			block_id TEXT,
			row_no INTEGER,
			field TEXT,
			value TEXT,
			norm_value TEXT,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_vocab_index_page ON vocab_index(page_id);`,
		`CREATE INDEX IF NOT EXISTS idx_vocab_index_type_field ON vocab_index(data_type, field);`,
	}
}

func (vocabIndexPlugin) Tables() []string { return []string{"vocab_index"} }

// Tags は nil を返します。②汎用索引はマーカー付き**標準HTML**だけを読み、
// カスタム要素を所有しません（語彙は htmldoc の構造HTML側で許可済み）。
func (vocabIndexPlugin) Tags() []TagSpec { return nil }

func (vocabIndexPlugin) Sync(tx *sql.Tx, pageID int, root *html.Node) error {
	if _, err := tx.Exec(`DELETE FROM vocab_index WHERE page_id = ?`, pageID); err != nil {
		return err
	}

	// block_no は同一 data-type のブロックの文書順連番（同じ形式の表が
	// ページに複数あっても行を区別できるようにする）。
	blockNo := map[string]int{}
	var firstErr error
	WalkElements(root, func(n *html.Node) {
		if firstErr != nil || (n.Data != "table" && n.Data != "dl") {
			return
		}
		dataType := Attr(n, "data-type")
		if dataType == "" {
			return // 素の table / dl は文書中の普通の表・定義リスト（オプトイン規約）
		}
		no := blockNo[dataType]
		blockNo[dataType]++
		def, _ := VocabDefByType(dataType) // 未定義でもゼロ値の def で続行（推論辞書だけ効く）

		var err error
		if n.Data == "table" {
			err = syncVocabTable(tx, pageID, dataType, no, def, n)
		} else {
			err = syncVocabDL(tx, pageID, dataType, no, def, n)
		}
		if err != nil {
			firstErr = err
		}
	})
	return firstErr
}

// vocabColumn は表の1列ぶんの解決済みスキーマ（文書の見出し行から読む）です。
type vocabColumn struct {
	key string
	typ ColumnType
}

// syncVocabTable は1つの <table data-type> を索引へ書き込みます。
// 最初の tr が見出し行（列の鍵と型を運ぶ）、以降がデータ行です。
func syncVocabTable(tx *sql.Tx, pageID int, dataType string, blockNo int, def VocabDef, table *html.Node) error {
	blockID := Attr(table, "data-id")
	rows := tableRows(table)
	if len(rows) < 2 {
		return nil // 見出しだけ（またはデータ行なし）の表は索引に載せる値が無い
	}

	// 見出し行 → 列スキーマ（鍵と型）を解決する
	var cols []vocabColumn
	for _, cell := range rowCells(rows[0]) {
		key := Attr(cell, "data-field")
		if key == "" {
			key = strings.TrimSpace(nodeText(cell))
		}
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
// dt が鍵（自由語）、後続の dd が値です（多値は dt の繰り返し／複数 dd）。
func syncVocabDL(tx *sql.Tx, pageID int, dataType string, blockNo int, def VocabDef, dl *html.Node) error {
	blockID := Attr(dl, "data-id")
	currentKey := ""
	rowNo := 0
	var firstErr error
	walkSkippingNested(dl, map[string]bool{"dl": true, "table": true}, func(n *html.Node) {
		if firstErr != nil {
			return
		}
		switch n.Data {
		case "dt":
			currentKey = strings.TrimSpace(nodeText(n))
		case "dd":
			key := Attr(n, "data-field") // ③計算形式の機械キー（骨格生成時に自動付与）が優先
			if key == "" {
				key = currentKey
			}
			if key == "" {
				return // dt より前の dd は鍵が決まらない
			}
			typ := InferColumnType(key)
			if col, ok := def.columnFor(key); ok {
				typ = col.Type
			}
			value := strings.TrimSpace(nodeText(n))
			if err := insertVocabEntry(tx, pageID, dataType, blockNo, blockID, rowNo, key, typ, value); err != nil {
				firstErr = err
				return
			}
			rowNo++
		}
	})
	return firstErr
}

// insertVocabEntry は1セル（1値）を索引へ書き込みます。正規化値は解釈できたときだけ併記します。
func insertVocabEntry(tx *sql.Tx, pageID int, dataType string, blockNo int, blockID string, rowNo int, field string, typ ColumnType, value string) error {
	var norm sql.NullString
	if v, ok := NormalizeValue(typ, value); ok {
		norm = sql.NullString{String: v, Valid: true}
	}
	_, err := tx.Exec(
		`INSERT INTO vocab_index (page_id, data_type, block_no, block_id, row_no, field, value, norm_value)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		pageID, dataType, blockNo, blockID, rowNo, field, value, norm)
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
// 鍵の決定は②と同じ規則（data-field 優先→表示文字→レジストリの Label 経由で Field へ）。

// VocabDLFields は dl の項目を機械キー→値の表として返します。
func VocabDLFields(dl *html.Node, def VocabDef) map[string]string {
	out := map[string]string{}
	if dl == nil {
		return out
	}
	currentKey := ""
	walkSkippingNested(dl, map[string]bool{"dl": true, "table": true, "section": true}, func(n *html.Node) {
		switch n.Data {
		case "dt":
			currentKey = strings.TrimSpace(nodeText(n))
		case "dd":
			key := Attr(n, "data-field")
			if key == "" {
				key = currentKey
			}
			if col, ok := def.columnFor(key); ok && col.Field != "" {
				key = col.Field
			}
			if key != "" {
				if _, dup := out[key]; !dup { // 多値は先頭を採る（TagValue と同じ）
					out[key] = strings.TrimSpace(nodeText(n))
				}
			}
		}
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
		key := Attr(cell, "data-field")
		if key == "" {
			key = strings.TrimSpace(nodeText(cell))
		}
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

// ClosestFileSrc は祖先のファイル容器から PDF パスを返します。
// 新形式 <section data-type="file" data-src> と旧 <m-file src> の両対応。
func ClosestFileSrc(n *html.Node) string {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		if p.Data == "section" && Attr(p, "data-type") == "file" {
			return Attr(p, "data-src")
		}
		if p.Data == "m-file" {
			return Attr(p, "src")
		}
	}
	return ""
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
