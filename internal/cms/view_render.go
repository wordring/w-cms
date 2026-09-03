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
	"log"
	"net/http"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/database"
)

// viewRenderers は計算ビューの形式ごとの描画処理です。
//
// **振り分けはレジストリの `View: true` 宣言**（vocab.go）で決まり、実際に中身を
// 作る処理をここで引きます。名指しのリストを持たないのは、レジストリへビューを
// 足して描画側を足し忘れたときに**無言の空白**にしないため——引けなければ
// missingViewHTML が理由を画面に出します（足し忘れは必ず目に見える）。
var viewRenderers = map[string]func(user *auth.User, pageIDInt int) string{
	"child-list": childListViewHTML,
}

// RegisterView は計算ビューの描画処理を足します。**拡張の `init()` から**
// 形式の宣言（`RegisterVocab`）と対で呼びます——`View: true` を宣言したのに
// 描画を足し忘れると `missingViewHTML` が画面に理由を出すので、**足し忘れは
// 必ず目に見えます**（無言の空白にはならない）。
//
// 二重登録はその場で落とします（どちらが描いているか分からなくなるため）。
func RegisterView(vocabType string, render func(user *auth.User, pageIDInt int) string) {
	if _, dup := viewRenderers[vocabType]; dup {
		panic("計算ビューの描画が重複しています: " + vocabType)
	}
	viewRenderers[vocabType] = render
}

// missingViewHTML は「ビューと宣言されているのに描画処理が無い」ことの表示です。
// 形式名を出すのは、直す人がどの宣言を足せばよいか分かるようにするため。
func missingViewHTML(vocabType string) string {
	return `<p class="view-error">この形式（` + stdhtml.EscapeString(vocabType) +
		`）の中身を作る処理がまだ用意されていません。</p>`
}

// init は計算ビューを**鏡型**としてコアの回覧機構へ登録します（walk.go）。
//
// 引き金はレジストリの `View: true` 宣言から作ります——名指しのリストを持たないので、
// ビューを足して登録を足し忘れる形の事故が起きません。描画処理が無い形式は
// `missingViewHTML` が理由を画面に出します（無言の空白を作らない）。
func init() {
	// 引き金は TriggerAll（マーカーのある要素すべて）で受け、**レジストリの
	// `View: true` 宣言で自分の担当かを判定**します。形式名ごとに登録しないのは、
	// 判定の正本をレジストリ1箇所に保つため——形式を足したとき、ここへ書き足す
	// 必要がありません（足し忘れが起きない）。
	RegisterMirror(TriggerAll, MirrorHandlerFunc(
		func(ctx *MirrorContext, el *html.Node) (bool, error) {
			if el.Data != "section" {
				return true, nil
			}
			// data-type が正、無ければ機能見出し（D-2）——
			// <section><h2>子ページ一覧</h2></section> だけで鏡が動く。
			vocabType := vocabTypeOf(el)
			def, ok := VocabDefByType(vocabType)
			if !ok || !def.View {
				return true, nil // 計算ビューではない（普通の業務ブロック）
			}
			inner := missingViewHTML(vocabType)
			if render, ok := viewRenderers[vocabType]; ok {
				inner = render(ctx.Viewer, ctx.PageID)
			}
			fillViewMarker(el, inner)
			// 中身はサーバーの所有物なので、その先へは配らない
			// （埋めた中身は .vocab-chrome なので、どのみち配送係が歩かない）。
			return false, nil
		}))
}

// hasViewMarker は本文に計算ビューのマーカーがあるかを文字列だけで判定します（早道）。
//
// 判定は**レジストリの `View: true` 宣言**から作ります。名指しのリストを持たないので、
// ビューを足したのにここだけ古いままで「パースされず素通り」する形の足し忘れが
// 起きません。マーカーが無い普通のページには、パースの費用を掛けません。
func hasViewMarker(bodyHTML string) bool {
	for _, def := range VocabDefs() {
		// ビューだけでなく、**鏡型の担当が付いている形式**も歩く価値があります
		// ——登録したのに走査されない、という無言の不発を避けるため
		// （古い図面の赤枠は View ではない鏡です。drawing_mirror.go）。
		if !def.View && !HasMirror(def.Type) {
			continue
		}
		if strings.Contains(bodyHTML, `data-type="`+def.Type+`"`) {
			return true
		}
		// 機能見出し（D-2）: 表示名が本文に現れたら歩く価値がある。
		// 早道は「無ければ確実に無い」ことだけ保証すればよく、
		// 偽陽性（表示名が地の文に出てくる）はパース1回の費用で済む。
		if strings.Contains(bodyHTML, def.DisplayName) {
			return true
		}
	}
	return false
}

// RenderComputedViews は本文HTML中の計算ビューのマーカーへ中身を埋めて返します。
// 中身は閲覧者によって変わる（子ページ一覧は read 権限で絞る）ため、リクエストを受け取る。
// マーカーが無い本文はパースせずそのまま返す（通常ページに追加コストを掛けない）。
//
// 走査そのものはコアの配送係（walk.go）が行い、ここは**段の入口**だけを担います。
func RenderComputedViews(r *http.Request, pageIDInt int, bodyHTML string) string {
	// 早道: 登録済みの引き金が本文に1つも無ければパースも走査もしない。
	if !hasViewMarker(bodyHTML) {
		return bodyHTML
	}
	nodes, err := htmldoc.ParseFragment(bodyHTML)
	if err != nil {
		return bodyHTML
	}

	ctx := &MirrorContext{
		DB:     database.DB,
		Viewer: auth.CurrentUser(r),
		PageID: pageIDInt,
	}
	nodes, err = walkers.walkMirror(ctx, nodes)
	if err != nil {
		// 鏡型のエラーはページ全体を道連れにしない（設計 §7）。
		// 各ビューは自分の中へ理由を描いて続行する作りなので、ここへは通常来ない。
		log.Printf("計算ビューの描画でエラー page=%d: %v", pageIDInt, err)
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
	// 何を消すかはマーカーの流儀で分かれる（D-2・2026-08-31）:
	//
	//   - **data-type の空マーカー**……中身はサーバーの所有物。全部消して描き直す
	//     （紛れ込んだ内容は表示に乗せない——保存内容は無傷のまま。従来どおり）。
	//   - **機能見出しのセクション**……見出しや注記が本文としてここに住んでいる。
	//     消すのは前回描いたクロームだけで、「見出しが鏡を呼び、人の書き込みは
	//     保存されて残り、鏡の中身はその下へ毎回描かれる」（語彙モデル §11.5-7）。
	var stale []*html.Node
	for c := el.FirstChild; c != nil; c = c.NextSibling {
		if Attr(el, "data-type") != "" || isChrome(c) {
			stale = append(stale, c)
		}
	}
	for _, n := range stale {
		el.RemoveChild(n)
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
