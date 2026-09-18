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
// **業務ブロック**（<table data-type> / <dl data-type> のうち可変タグ以外）を
// 1つの縦持ちテーブル vocab_index へ同期します。**コアに1実装**であり、形式（data-type）が
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
		// 縦持ち。1セルが1行（`(page_id, data_type, block_no, row_no, field)`）。
		// norm_num は number 型の値の**数値としての**正規化値。SQLite は TEXT 同士を
		// 文字列比較する（"8000" < "900"）ため、数の大小・範囲で絞る列は数値の
		// 格納クラスに分ける（【一覧】日付形式と数詞.md §5。2026-08-30 決定）。
		// norm_value（TEXT）は date の時系列比較と表示用にそのまま残る。
		//
		// **タグはここに入りません**（2026-09-13 にユーザー決定で分けた。下の page_tags）。
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

		// ── 可変タグ（`dl[data-type="tags"]`）の索引 ───────────────────────
		//
		// **2026-09-13 にユーザー決定で分けました**（「HTMLのTableとタグはDBのTableを
		// 分けたいと思います」）。2026-09-06 の考察では「分けない」と結論しましたが、
		// そのときの根拠3つのうち**2つが崩れていました**（実測）:
		//
		//   - 「タグは付箋であってブロックではない」→ **アドレス帳がタグになりました**。
		//     連絡先・担当者の実体は `メールアドレス`・`電話番号`・`役職` のタグで、
		//     いまやタグは業務データの本体です。
		//   - 「C-1（読む側で `data_type='tags'` を足す）で足りる」→ **足りませんでした**。
		//     決定から1週間、13箇所が絞り込み無しのまま残り、2026-09-13 には**3箇所
		//     増えました**（連絡先の実装で、書いた本人が規律を3回忘れた）。
		//     表が分かれていれば、間違った問いはそもそも書けません。
		//
		// 数でも別物です（実測・212ページ）: タグ1172行に対し業務ブロックは全部で230行。
		// **索引の84%がタグ**で、`block_id` が埋まるのは2.5%だけ（タグはたいていページ単位）。
		//
		// **列はタグのために選び直しました**（2026-09-13 ユーザー:「なんだかすっきり
		// しませんね」）。最初は `vocab_index` から `data_type` を抜いただけの借り物で、
		// **8列のうち3列が誰にも読まれていませんでした**（実測・タグ1172行）:
		//
		//	block_no   全行が 0（情報量ゼロ）        → 落とした
		//	block_id   2.5%しか埋まらず、読む所ゼロ   → 落とした
		//	           （参照の指し先8個も、すべて添付の要素でタグの dl ではなかった）
		//	norm_num   4.9%しか入らず、読む所ゼロ     → 落とした
		//
		// 残した5列は全部働いています:
		//
		//	page_id    どのページか
		//	seq        **ページの中の通し番号**。`dt`/`dd` の対の順で、
		//	           「`CCアドレス` の表示名は1つ手前の `CC`」の対応づけに要る
		//	           （これが無いと、CCが2人のとき両方が先頭の名前になります）
		//	name       名前（`dt` の表示文字）。**これが鍵**——機械キーは持たない
		//	value      値（`dd` の表示文字）。**これが正本**
		//	norm_value 比較用に畳んだ値（`受信日時` の UTC 化など・9%で value と異なる）
		//	           **宣言型を持ちません**（2026-09-15。下の「型は値が持つ」）
		//
		// `seq` は**ページ通し**です（`dl` ごとに 0 へ戻していた `row_no` を改めました）。
		// 1ページに `dl` を2つ置けるので、`dl` ごとの番号では鍵になりません
		// ——いまの実データでは衝突ゼロですが、それは1つしか無いからで、構造の保証では
		// ありません。主鍵にしたことで、同じ位置の二重書き込みも起こせなくなりました。
		//
		// `data_type` 列はありません——ここに入るものは全部タグだからです。
		//
		// **名前は一度使って消したもの**です（`page_tags` は 2026-08-30 に vocab_index へ
		// 吸収されました）。同じ名前で戻したのは、これが**そのとき畳んだものを畳み直す**
		// 話ではなく、**タグが業務データの本体になった**という別の理由だからです。
		//
		// ── `norm_value` に宣言型が無いのは、型を**値**に持たせるため ──────
		//
		// 2026-09-15 にユーザー決定で外しました（「norm_value は文字列、数値、時刻の
		// ような種類が考えられます」）。**SQLite は宣言の無い列で、値ごとの格納クラスを
		// そのまま保ちます**。`TEXT` と宣言すると数まで文字列に化け、辞書順で
		// **`"8000" < "900"`** になります（実測）:
		//
		//	宣言 TEXT   → 12.5, 8000.0, 900.0, abc   ← 並びが壊れている
		//	宣言なし    → 12.5, 900, 8000, abc       ← 数は数として並ぶ
		//
		// **混ざっても平気な理由は2つ**: ①SQLite の並びは格納クラスのグループが先
		// （NULL < 数値 < 文字列 < BLOB）なので**範囲が重ならない** ②問いは必ず
		// `name` で絞り、**名前が型を決める**ので1つの問いの中では型が揃う。
		// 索引もそのまま効きます（`SEARCH page_tags USING INDEX … (name=? AND norm_value>?)`）。
		//
		// ⚠ **代償は「型を取り違えると、エラーにならず0件」**（実測）。SQLite は型の
		// 違う値を等しいと見ないだけで、何も言いません。だから束ねる形の判断は
		// `tagNormBind` 1か所に閉じ、**書き手と読み手が同じ関数を通ります**——
		// 割れた瞬間、number のタグは「書いたのに引けない」になります。
		//
		// `vocab_index` 側は **`norm_num`（REAL）を持ったまま**です（統合は別途・
		// あちらは読み手が実在します——`vocabRows` の集計）。
		`CREATE TABLE IF NOT EXISTS page_tags (
			page_id INTEGER NOT NULL,
			seq INTEGER NOT NULL,
			name TEXT NOT NULL,
			value TEXT NOT NULL,
			norm_value,
			PRIMARY KEY (page_id, seq),
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
		// ページ単位の引きは主鍵の先頭列で足りるので、`page_id` 単独の索引は要りません。
		`CREATE INDEX IF NOT EXISTS idx_page_tags_name_value ON page_tags(name, value);`,
		`CREATE INDEX IF NOT EXISTS idx_page_tags_name_norm ON page_tags(name, norm_value);`,
	}
}

func (vocabIndexPlugin) Tables() []string { return []string{"vocab_index", "page_tags"} }

// Triggers はマーカーのある要素**すべて**を受け取ることを宣言します。
// 未知の `data-type` もそのまま索引に載る、という現行仕様がそのままこの1行になります。
func (vocabIndexPlugin) Triggers() []string { return []string{TriggerAll} }

// OnPageStart は当該ページ分を洗い流します（洗い替えの前半）。
func (vocabIndexPlugin) OnPageStart(ctx *ObserveContext) error {
	if _, err := ctx.Tx.Exec(`DELETE FROM vocab_index WHERE page_id = ?`, ctx.PageID); err != nil {
		return err
	}
	// **タグの表も一緒に洗い流します**（2026-09-13 に分離）。片方だけ消すと、
	// 本文から消したタグが索引に居残り、逆引きが幽霊を返します。
	_, err := ctx.Tx.Exec(`DELETE FROM page_tags WHERE page_id = ?`, ctx.PageID)
	return err
}

// OnElement はマーカー付きの table / dl を索引します。
//
// ⚠ **`section` の素の定義リストは索引しません**（2026-09-18 ユーザー決定:「素の定義
// リストはDBから外しましょう。問題が出てから再検討しましょう」）。それまで機能見出しの
// 節の中の素の `dl` を**業務ブロックのヘッダ**として索引していましたが、やめました:
//
//   - **見た目がタグと同じで振る舞いが違いました**。素の `<dl>` と `<dl data-type="tags">` は
//     画面で区別が付かないのに、入る表が違う（`vocab_index` と `page_tags`）
//   - **横断検索の口（`PagesByTag`）が読むのは `page_tags`** です。いちばん検索したい値
//     （図面番号・発注書番号）が、検索の口を持たない表に入っていました
//   - ヘッダは「下の明細表の見出し」の役目でしたが、**1文書＝1ページ**を規則にすると
//     ページのタグで足ります（ユーザー:「発注書は一ページ一発注書で問題ない」）
//
// ⚠ **素の「表」は拾い続けます**（`syncVocabSection`）——`■材料` のような見出しの下に
// 表が並ぶワンノートの形が、そのまま形式宣言になる受け皿だからです（D-2・移行の要）。
// つまりDBに入るのは**タグと表だけ**。節の中のマーカー付き table / dl は配送係が
// 別に届けてくれるので、ここでは降ります（＝ descend は常に true）。
func (vocabIndexPlugin) OnElement(ctx *ObserveContext, el *html.Node) (bool, error) {
	if el.Data == "section" {
		// 節の**素の表**だけを拾います（⚠ 素の定義リストは拾いません・2026-09-18）。
		return true, syncVocabSection(ctx, el)
	}
	if el.Data != "table" && el.Data != "dl" {
		return true, nil // 形式の無い要素は配送係が弾く。ここへ来るのは section 等
	}
	// 形式の解決は配送係と同じ vocabTypeOf——属性が正、無ければ位置の規則
	// （セクション外の素の dl＝タグ）。
	dataType := vocabTypeOf(el)
	// block_no は同一 data-type のブロックの文書順連番（同じ形式の表が
	// ページに複数あっても行を区別できるようにする）。
	no := ctx.Counter("vocab_index:" + dataType)
	def, _ := VocabDefByType(dataType) // 未定義でもゼロ値の def で続行（推論辞書だけ効く）

	if el.Data == "table" {
		return true, syncVocabTable(ctx.Tx, ctx.PageID, dataType, no, Attr(el, "data-id"), def, el)
	}
	// **タグはページ通しの番号**（主鍵にするため。`dl` が2つあっても衝突しない）。
	return true, syncVocabDL(ctx.Tx, ctx.PageID, dataType, no, Attr(el, "data-id"), def, el,
		tagSeqOf(ctx, dataType))
}

// syncVocabSection は形式を持つ section の**素の表**をその形式で索引します。
//
// **素の table は data-type を持ちません**（役割は包む section が宣言し、列の鍵は
// 見出し行の表示文字。語彙モデル §8.2・§11.5-4）。配送係は引き金のある要素しか
// 届けないので、素の表は誰の手にも渡りません——section の側から拾うのがここです。
//
// **これはワンノート移行の受け皿**です（D-2・2026-08-31）——`■材料` のような見出しの
// 下に表が並ぶ形がそのまま w-cms の形式宣言になります
// （【考察】ワンノート移行.md §2.5。766ページがこの形）。
//
// ⚠ **素の `dl`（ヘッダ）は拾いません**（2026-09-18 ユーザー決定:「素の定義リストは
// DBから外しましょう。問題が出てから再検討しましょう」）。名前：値はタグで書きます
// ——`<dl data-type="tags">` だけがDBに入ります。**表とタグだけがDBに入る**、が
// 説明の全部です。やめた理由は `OnElement` の注記に。
//
// 形式は vocabTypeOf で解決します——data-type 属性が正、無ければ機能見出し。
// マーカー付きの table / dl（可変タグ・明細表）は独立した形式として配送係が別に
// 届けるので、ここでは拾いません（拾うと同じ値が二重に索引されます）。
// 入れ子の section へは降りません（入れ子の業務ブロックは独立して読まれます）。
func syncVocabSection(ctx *ObserveContext, section *html.Node) error {
	dataType := vocabTypeOf(section)
	def, _ := VocabDefByType(dataType)
	sectionBlockID := Attr(section, "data-id")

	// 素の表の読み方: 形式が明細（Items）を宣言していれば、素の表は**明細**である
	// ——見出しの言葉が表の役割を宣言し、th の表示文字が列を宣言するので、本文から
	// 機械語が消えます（2026-08-31 ユーザー:「THに表示される文字列が、すなわち列の
	// データを表します。人に対しても機械に対しても有効」）。
	itemsType, itemsDef := dataType, def
	if def.Items != "" {
		if idef, ok := VocabDefByType(def.Items); ok {
			itemsType, itemsDef = def.Items, idef
		}
	}

	// 由来（block_id）は素の表自身の data-id が最優先（参照 `ページID-ブロックID` で
	// 表そのものを指せる）。無ければ包んでいる section のIDを刻む。
	var firstErr error
	eachPlainVocabTable(section, func(n *html.Node) {
		if firstErr != nil {
			return
		}
		blockID := Attr(n, "data-id")
		if blockID == "" {
			blockID = sectionBlockID
		}
		no := ctx.Counter("vocab_index:" + itemsType)
		firstErr = syncVocabTable(ctx.Tx, ctx.PageID, itemsType, no, blockID, itemsDef, n)
	})
	return firstErr
}

// eachPlainVocabTable は section の**素の表**（data-type を持たない table）を文書順で
// fn へ渡します。マーカー付きは独立した形式（配送係が別に届ける）、入れ子の section は
// 独立した業務ブロックなので、どちらも渡しません。
//
// ⚠ **素の `dl` は渡しません**（2026-09-18 に索引から外した）。索引・種まき・改名告知が
// 同じ切り分けを共有します——ここが割れると「索引には載るのに告知されない」ずれが
// 生まれるため、巡回は1箇所に持ちます。
func eachPlainVocabTable(section *html.Node, fn func(n *html.Node)) {
	walkSkippingNested(section, map[string]bool{"section": true}, func(n *html.Node) {
		if Attr(n, "data-type") != "" {
			return
		}
		if n.Data == "table" {
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
// ⚠ **可変タグ（data_type="tags"）はこの索引に入りません。** 行き先は page_tags で、
// 振り分けるのは書き手1か所（insertVocabEntry）です。
//
// 一度は逆の決定をしていました——2026-08-30（D-1 の第一歩）に専用の page_tags を
// 吸収し「唯一の行き先」にしています（当時は中身が重複し、読む者も居なかった）。
// **2026-09-13 にユーザー決定で分け直しました**（「HTMLのTableとタグはDBのTableを
// 分けたいと思います」）——アドレス帳がタグになってタグが業務データの本体になり
// （索引の84%）、読む側で data_type='tags' を足す規律が1週間で13箇所破られたため。
// 正本は docs/【考察】タグ表と業務ブロック表の分離.md §8。
// **このコメントは 2026-09-14 まで「唯一の行き先」と言い続けていました**
// ——同じファイルの53行目と正面から食い違ったまま。「親ページID」を
// 取り込まない旧ガードも同時に消えた——親はサイドカーが正本で、この語を親として
// 解釈するコードはもう無い（書けば普通のタグとして索引に載るだけ・不活性）。
// tagSeqOf は、タグならページ通しの番号を配る関数を、そうでなければ nil を返します。
//
// 数えるのは `ObserveContext` です——観察係はページ間で使い回される singleton なので、
// そちらに持たせると番号がページをまたいで漏れます（walk.go の Counter の説明）。
func tagSeqOf(ctx *ObserveContext, dataType string) seqCounter {
	if dataType != TagsDataType {
		return nil
	}
	return func() int { return ctx.Counter("page_tags:seq") }
}

// seqCounter は次の通し番号を返す関数です（タグのときだけ渡します）。
//
// **タグの番号はページ通し**にします（2026-09-13）。`dl` ごとに 0 へ戻すと、
// 1ページに `dl` が2つあったとき番号が衝突し、主鍵にできません。
// nil なら `dl` ごとの連番（業務ブロックはこれまでどおり `block_no` と対で使う）。
type seqCounter func() int

func syncVocabDL(tx *sql.Tx, pageID int, dataType string, blockNo int, blockID string, def VocabDef, dl *html.Node, nextSeq seqCounter) error {
	rowNo := 0
	var firstErr error
	eachDLPair(dl, false, func(key string, dd *html.Node) bool {
		if key == "" {
			return true // dt より前の dd は鍵が決まらない
		}
		if nextSeq != nil {
			rowNo = nextSeq()
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
	// **振り分けはここ1か所**（2026-09-13 の分離）。書き手が1本なので、タグが
	// 業務ブロックの表へ紛れることも、その逆も起きません——読む側で毎回
	// `data_type='tags'` を思い出す必要が消えました（それを1週間で3回忘れたのが
	// 分けた理由です）。
	if dataType == TagsDataType {
		// タグの表は列がタグのために選んであります（block_no / block_id / norm_num は
		// 誰も読まなかったので持ちません。上の Schema の説明）。`rowNo` はここでは
		// **ページ通しの `seq`** です（呼ぶ側が ctx.Counter で採る）。
		//
		// **畳んだ値は格納クラスごと入れます**（`norm_value` に宣言型が無いため）。
		// 畳めなかったものは nil のまま＝NULL で、併記しません。
		var normBind any
		if norm.Valid {
			normBind = tagNormBind(typ, norm.String)
		}
		_, err := tx.Exec(
			`INSERT INTO page_tags (page_id, seq, name, value, norm_value)
			 VALUES (?, ?, ?, ?, ?)`,
			pageID, rowNo, field, value, normBind)
		return err
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
// （入れ子の表は配送係が独立したブロックとして届け、別に索引されます）。
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
// （dlHeadingKeys・freshenDL）は「入れ子の業務ブロックは独立して
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
