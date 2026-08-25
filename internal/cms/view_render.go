package cms

// ─────────────────────────────────────────────────────────────────────────
// 計算ビュー（表示専用）のサーバー事前描画
//
// 本文に保存されるのは空のマーカーだけで、中身はページを返すたびにここで埋める:
//
//   <section data-type="child-list">          → 子ページ一覧
//   <section data-type="required-materials">  → 部材手配・発注進捗の集計表
//
// 描画した中身は <div class="vocab-chrome" contenteditable="false"> に包む。
// エディタのシリアライザは .vocab-chrome を保存しない（エンハンサのクロームと
// 同じ規則）ので、保存経路にはマーカーだけが残る。閲覧はゼロJSで完結し、
// 公開ページの SEO 要件（要件定義書 §4.4）とも整合する
// （ユーザー決定 2026-08-19: 計算ビューはサーバー事前描画。語彙モデル §9）。
//
// 編集モードで子ページ作成や発注の変更があっても表示は自動更新されない。
// 反映は再読み込みで行う（描画ロジックを Go と JS の2箇所に持たないための割り切り）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/htmldoc"
)

// viewRenderers は計算ビューの形式ごとの描画処理です。
//
// **振り分けはレジストリの `View: true` 宣言**（vocab.go）で決まり、実際に中身を
// 作る処理をここで引きます。名指しのリストを持たないのは、レジストリへビューを
// 足して描画側を足し忘れたときに**無言の空白**にしないため——引けなければ
// missingViewHTML が理由を画面に出します（足し忘れは必ず目に見える）。
var viewRenderers = map[string]func(user *auth.User, pageIDInt int) string{
	"child-list":         childListViewHTML,
	"required-materials": requiredMaterialsViewHTML,
}

// missingViewHTML は「ビューと宣言されているのに描画処理が無い」ことの表示です。
// 形式名を出すのは、直す人がどの宣言を足せばよいか分かるようにするため。
func missingViewHTML(vocabType string) string {
	return `<p class="view-error">この形式（` + stdhtml.EscapeString(vocabType) +
		`）の中身を作る処理がまだ用意されていません。</p>`
}

// RenderComputedViews は本文HTML中の計算ビューのマーカーへ中身を埋めて返します。
// 中身は閲覧者によって変わる（子ページ一覧は read 権限で絞る）ため、リクエストを受け取る。
// マーカーが無い本文はパースせずそのまま返す（通常ページに追加コストを掛けない）。
func RenderComputedViews(r *http.Request, pageIDInt int, bodyHTML string) string {
	// 早期リターンの判定もレジストリから作る（名指しにすると、ビューを足したのに
	// ここだけ古いままで「パースされず素通り」する形の足し忘れが起きる）。
	viewTypes := map[string]bool{}
	for _, def := range VocabDefs() {
		if def.View {
			viewTypes[def.Type] = true
		}
	}
	found := false
	for t := range viewTypes {
		if strings.Contains(bodyHTML, `data-type="`+t+`"`) {
			found = true
			break
		}
	}
	if !found {
		return bodyHTML
	}
	nodes, err := htmldoc.ParseFragment(bodyHTML)
	if err != nil {
		return bodyHTML
	}

	// 走査中の書き換えを避けるため、先に対象を集めてから埋める。
	var targets []*html.Node
	for _, n := range nodes {
		WalkElements(n, func(el *html.Node) {
			if el.Data == "section" && viewTypes[Attr(el, "data-type")] {
				targets = append(targets, el)
			}
		})
	}
	if len(targets) == 0 {
		return bodyHTML
	}

	for _, el := range targets {
		vocabType := Attr(el, "data-type")
		inner := missingViewHTML(vocabType)
		if render, ok := viewRenderers[vocabType]; ok {
			inner = render(auth.CurrentUser(r), pageIDInt)
		}
		fillViewMarker(el, inner)
	}

	var sb strings.Builder
	for _, n := range nodes {
		html.Render(&sb, n)
	}
	return sb.String()
}

// fillViewMarker はマーカー要素の中身を描画結果（vocab-chrome）へ置き換えます。
// マーカーの中へ誤って書かれた内容は表示に乗せない（ビューの中身はサーバーが所有する）。
func fillViewMarker(el *html.Node, innerHTML string) {
	for el.FirstChild != nil {
		el.RemoveChild(el.FirstChild)
	}
	chrome := `<div class="vocab-chrome" contenteditable="false">` + innerHTML + `</div>`
	nodes, err := htmldoc.ParseFragment(chrome)
	if err != nil {
		return
	}
	for _, n := range nodes {
		el.AppendChild(n)
	}
}

// childListViewHTML は子ページ一覧の中身を組み立てます（/api/children と同じ絞り込み）。
// タイトルは利用者の入力なので必ずエスケープする。
func childListViewHTML(user *auth.User, parentIDInt int) string {
	children, err := visibleChildren(user, parentIDInt)
	if err != nil {
		return `<p class="view-error">子ページ一覧を取得できませんでした。</p>`
	}
	if len(children) == 0 {
		return `<p class="child-list-empty">子ページはありません</p>`
	}
	var sb strings.Builder
	sb.WriteString(`<ul class="child-list">`)
	for _, c := range children {
		sb.WriteString(`<li><a href="/` + stdhtml.EscapeString(c.ID) + `">📄 ` +
			stdhtml.EscapeString(c.Title) + `</a></li>`)
	}
	sb.WriteString(`</ul>`)
	return sb.String()
}

// requiredMaterialsViewHTML は部材手配・発注進捗の集計表を組み立てます
// （/api/required-materials と同じ集計。見出しは旧テンプレートを踏襲）。
//
// 子ページ一覧と同じく閲覧者を受け取る。部材の定義元ページを読めない相手には
// その行を出さないため（RequiredMaterials 側で絞る）。この経路は匿名にも描画される
// ので、渡し忘れると非公開ページ由来の部材名・仕入先が公開ページへ出る。
func requiredMaterialsViewHTML(user *auth.User, pageIDInt int) string {
	head := `<h3 class="materials-title">📊 部材手配・発注進捗状況</h3>`
	list, err := RequiredMaterials(user, pageIDInt)
	if err != nil {
		return head + `<p class="view-error">集計データの取得に失敗しました。</p>`
	}
	if len(list) == 0 {
		return head + `<p class="materials-empty">必要部材として登録されているアイテムはありません。</p>`
	}
	var sb strings.Builder
	sb.WriteString(head)
	sb.WriteString(`<table class="materials-table"><thead><tr>` +
		`<th>必要材料・部材名</th><th>仕入先・外注先</th>` +
		`<th class="num">必要総数</th><th class="num">発注済数</th>` +
		`<th class="num">残要手配数</th><th class="status">ステータス</th>` +
		`</tr></thead><tbody>`)
	for _, m := range list {
		supplier := m.SupplierName
		if supplier == "" {
			supplier = "-"
		}
		status := `<span class="badge ok">手配完了</span>`
		if m.Remaining > 0 {
			status = `<span class="badge ng">要手配 (` + strconv.Itoa(m.Remaining) + `)</span>`
		}
		sb.WriteString(`<tr><td>` + stdhtml.EscapeString(m.MaterialName) + `</td>` +
			`<td>` + stdhtml.EscapeString(supplier) + `</td>` +
			`<td class="num">` + strconv.Itoa(m.TotalRequired) + `</td>` +
			`<td class="num">` + strconv.Itoa(m.Ordered) + `</td>` +
			`<td class="num">` + strconv.Itoa(m.Remaining) + `</td>` +
			`<td class="status">` + status + `</td></tr>`)
	}
	sb.WriteString(`</tbody></table>`)
	return sb.String()
}
