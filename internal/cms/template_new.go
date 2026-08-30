package cms

// ─────────────────────────────────────────────────────────────────────────
// ページテンプレート：新規化パス（docs/【考察】ページテンプレート.md §4.1）
//
// テンプレートの**単純なコピーでは足りません**。日付は作成時の日付であるべきで、
// 発注書番号のように一意でなければならない値もあります。同じ番号の発注書が
// 何枚もできると、番号でページを引いたときに区別がつきません
// （かつては client_orders / our_orders の UNIQUE 制約が保存を弾いていましたが、
// テーブルごと廃したので **DBはもう止めてくれません**。再採番がその代わりです）。
//
// 規則はただ1つ:
//
//	テンプレートの空欄は、列型の既定値で埋める。書いてある値はそのまま保つ。
//
// 新しい構文を発明せず、①語彙レジストリが既に持つ型知識だけで動きます
// （エディタ側の defaultFieldValue と同じ規則をサーバーへ持ってきたもの）。
// テンプレート作者は「空欄にしておけば埋まる」と覚えるだけで済みます。
//
// 地の文のプレースホルダ（{{今日}} 等）は v1 では持ちません（同書 §4.2）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"fmt"
	"log"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/database"
)

// orderNoField は再採番の対象になる機械キーです（UNIQUE 制約を持つ列）。
const orderNoField = "order-no"

// FreshenTemplateBody はテンプレート本文の空欄を列型の既定値で埋めて返します。
// newPageID は再採番した発注書番号に使います（PO-000123 のように**由来ページが辿れる**
// 番号にする。エディタの `PO-` ＋時刻下6桁より衝突しにくく、意味も読める）。
//
// 値が書かれているセルには触りません——テンプレート作者が意図して入れた既定値
// （「発注元: 得意先A」など）を消さないためです。
func FreshenTemplateBody(bodyHTML, newPageID string) string {
	nodes, err := htmldoc.ParseFragment(bodyHTML)
	if err != nil {
		return bodyHTML
	}
	// 走査はコアの配送係（walk.go）が行い、ここは**段の入口**だけを担います。
	// 種まきの担当はコア備え付けの1つ（レジストリの列型駆動）で、プラグイン化は
	// 口だけ用意してあります（設計 §3）。
	ctx := &SeedContext{
		DB:        database.DB,
		NewPageID: newPageID,
		Now:       time.Now(),
	}
	nodes, err = walkers.walkSeed(ctx, nodes)
	if err != nil {
		// 種まきの失敗は新規作成を止めるほどではない（設計 §7）。素のコピーで続行する。
		log.Printf("テンプレートの新規化でエラー page=%s: %v", newPageID, err)
	}

	var sb strings.Builder
	for _, n := range nodes {
		html.Render(&sb, n)
	}
	return sb.String()
}

// init はテンプレート新規化を**種まき**としてコアの回覧機構へ登録します（walk.go）。
//
// 引き金は TriggerAll で受け、レジストリ宣言（形式と要素の組）で自分の担当かを
// 判定します。判定の正本をレジストリ1箇所に保つためで、形式を足したときに
// ここへ書き足す必要はありません。
func init() {
	RegisterSeeder(TriggerAll, SeedHandlerFunc(
		func(ctx *SeedContext, el *html.Node) (bool, error) {
			def, ok := VocabDefByType(Attr(el, "data-type"))
			if !ok || el.Data != def.Element {
				return true, nil
			}
			f := &templateFreshener{
				pageID: ctx.NewPageID,
				today:  ctx.Now.Format("2006-01-02"),
				seq:    ctx.Counter("freshen"),
			}
			switch def.Element {
			case "table":
				f.freshenTable(el, def)
			case "dl":
				f.freshenDL(el, def)
			case "section":
				// 業務文書ブロックのヘッダは data-type を持たない直下の <dl>。
				// 明細表は table 形式として**別途配られる**のでここでは見ない
				// （vocabHeadingKeys と同じ切り分け）。だから子孫へは降りる。
				f.freshenDL(FirstVocabChild(el, "dl", ""), def)
			}
			return true, nil
		}))
}

// templateFreshener は1つのブロックを新規化するあいだの状態です。
// 連番（seq）は**コンテキストの Counter** から受け取ります——ページ内で通し番号に
// なる必要があり、ハンドラは登録時に1つだけ作られる singleton だからです。
type templateFreshener struct {
	pageID string
	today  string
	seq    int // 同じページ内で複数回採番したときの連番
}

// freshenTable は表のデータ行（見出し行より後）の空セルを埋めます。
// 列の鍵は見出し行の表示文字です（「見える文字がすべて」——語彙モデル §5.1）。
func (f *templateFreshener) freshenTable(table *html.Node, def VocabDef) {
	rows := tableRows(table)
	if len(rows) < 2 {
		return // 見出し行しかない＝データ行が無い
	}
	var cols []VocabColumn
	for _, cell := range rowCells(rows[0]) {
		key := strings.TrimSpace(nodeText(cell))
		if col, ok := def.columnFor(key); ok {
			cols = append(cols, col)
		} else {
			cols = append(cols, VocabColumn{Label: key, Type: InferColumnType(key)})
		}
	}
	for _, row := range rows[1:] {
		for i, cell := range rowCells(row) {
			if i < len(cols) {
				f.fillCell(cell, cols[i])
			}
		}
	}
}

// freshenDL は dl（または section 直下のヘッダ dl）の空の dd を埋めます。
// 鍵は直前の dt の表示文字です（dlHeadingKeys と同じ走査）。
func (f *templateFreshener) freshenDL(dl *html.Node, def VocabDef) {
	if dl == nil {
		return
	}
	eachDLPair(dl, true, func(key string, dd *html.Node) bool {
		col, ok := def.columnFor(key)
		if !ok {
			col = VocabColumn{Label: key, Type: InferColumnType(key)}
		}
		f.fillCell(dd, col)
		return true
	})
}

// fillCell は空のセルにだけ既定値を書き込みます。
func (f *templateFreshener) fillCell(cell *html.Node, col VocabColumn) {
	if cell == nil || strings.TrimSpace(nodeText(cell)) != "" {
		return // 値が書かれているセルには触らない
	}
	v := f.defaultValue(col)
	if v == "" {
		return
	}
	setCellText(cell, v)
}

// defaultValue は列型ごとの既定値です（エディタの defaultFieldValue と同じ規則）。
// text / enum / number / image は空のまま（人が書く）。
func (f *templateFreshener) defaultValue(col VocabColumn) string {
	if col.Field == orderNoField {
		return f.nextOrderNo()
	}
	if col.Type == ColDate {
		return f.today
	}
	return ""
}

// nextOrderNo は新しい発注書番号を返します。1ページ内に発注書ブロックが複数あっても
// 衝突しないよう、2件目以降は連番を足します。
func (f *templateFreshener) nextOrderNo() string {
	f.seq++
	if f.seq == 1 {
		return "PO-" + f.pageID
	}
	return fmt.Sprintf("PO-%s-%d", f.pageID, f.seq)
}

// setCellText はセルの中身をテキスト1つで置き換えます。
// 空セルのキャレット足場（<br>）も一緒に消えます。
func setCellText(cell *html.Node, text string) {
	for c := cell.FirstChild; c != nil; {
		next := c.NextSibling
		cell.RemoveChild(c)
		c = next
	}
	cell.AppendChild(&html.Node{Type: html.TextNode, Data: text})
}
