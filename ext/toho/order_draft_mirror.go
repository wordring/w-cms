package toho

// ─────────────────────────────────────────────────────────────────────────
// 発注部材表の足元に「部材発注書を作る」を出す（2026-09-22）
//
// ユーザー:「**発注部材表から発注書を作るので「発注書を作る」ボタンは発注部材表の下に
// あるはずです**」。
//
// ⚠ **最初の実装は、未発注の表のフォームに両方のボタンを置いていました**——
// 「表作成」と「発注書作成」が並んでいて、**どちらの表から発注書ができるのか**が
// 画面から読み取れませんでした。**ボタンは、その相手の隣にあるべき**です。
//
// ⚠ **表が何枚あってもかまいません**（同日のユーザー訂正）。**1枚＝1社**なので、
// **業者ごとに同時に進める**のが普通の形です——だから**それぞれの表に**自分の
// 「部材発注書を作る」が付きます。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

func init() {
	cms.RegisterMirror(OrderDraftType, cms.MirrorHandlerFunc(renderOrderDraftForm))
}

// renderOrderDraftForm は発注部材表の足元に、部材発注書を作る欄を足します。
//
// ⚠ **鏡なので本文には残りません**（`.vocab-chrome`）——人が画面の表をコピーして
// 貼っても、入力欄が本文に焼き付くことはありません。
func renderOrderDraftForm(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	cms.DropChrome(el)
	// この表が何枚目かを数えます（画面が「どの表から作るか」を送るため）。
	idx := draftIndexOf(ctx)
	// 表の中身の指紋（画面が送り、サーバーが同じ表かを確かめる・2026-10-26）。画面の部品を足す前に数える。
	sig := tableSig(el)
	// 仕入先の初めの値は、表の「推奨業者」が1社にそろっていればその社（2026-10-21・§6.3）。行ごとの「外す」を足す前に読む。
	vendor := draftVendorOf(el)
	// ⚠ **行ごとに「戻す」を付けます**（2026-09-22 ユーザー:「**発注部材表から
	//    未手配の一覧へ戻す方法がありません**」）。
	//    ⚠ **「戻す」は「外す」です**——一覧は毎回計算される鏡なので、ここから消せば
	//    **自動的に戻ってきます**。戻す先へ何かを書く必要はありません。
	// 2026-10-21 夕から、行ごとのボタンは表の中に置かない——行の先頭にチェック、「不要」「外す」は表の下の欄（利用者:「この表はご法度のはずの一緒にボタンを入れています」）。
	prependRowChromeCells(el, "draft-pick-cell", func(n int, _ *html.Node) string {
		return pickHTML("draft-pick", map[string]string{"row": strconv.Itoa(n + 1)})
	})
	// 数量の右に、部材の在庫数（2026-10-23・stock_mirror.go）。
	insertStockColumn(el, stockLevels(ctx.DB, ctx.Viewer), partStockKey)
	rowBar := draftRowBarHTML(page.FormatID(ctx.PageID), idx, pageKindOf(ctx.PageID) == PageKindMfg)
	// 「部材発注書を作る」の欄は**表の外（包む節の中・表の直後）**へ（2026-09-28・`draftBoxOf`）。
	box := draftBoxOf(el)
	cms.DropChrome(box)
	appendHTML(box, `<div class="vocab-chrome draft-form-box w-no-print" contenteditable="false">`+rowBar+
		draftFormHTML(ctx.Viewer, page.FormatID(ctx.PageID), idx, sig, vendor)+`</div>`)
	return false, nil
}

// draftRowBarHTML は発注部材表の下の、選んだ行への操作です（2026-10-21 夕）——「↩ 外す」（発注部材表から外す・「部材」でまた集められる）と、
// 製造ページなら「🚫 不要」（手配不要表へ移す・mfg_skip.go）。
func draftRowBarHTML(pageID string, idx int, mfg bool) string {
	attrs := ` data-draft-page="` + stdhtml.EscapeString(pageID) + `" data-draft-table="` + strconv.Itoa(idx) + `"`
	out := `<div class="draft-row-bar">選んだ行を: `
	if mfg {
		out += `<button type="button" class="chip-btn draft-sel-skip"` + attrs + ` title="この製造では手配しません（理由と日付つきで手配不要表へ移します）">🚫 不要</button> `
	}
	return out + `<button type="button" class="chip-btn draft-sel-back"` + attrs + ` title="発注部材表から外します（製造明細表の「部材」で、また集められます）">↩ 外す</button> <span class="draft-row-say"></span></div>`
}

// draftVendorOf は、発注部材表の「推奨業者」の列の値が1社にそろっていればその社を、そうでなければ空を返します。
func draftVendorOf(table *html.Node) string {
	rows := rowsOf(table)
	if len(rows) < 2 {
		return ""
	}
	vi := headerIndexOf(rows[0], vendorLabel)
	if vi < 0 {
		return ""
	}
	one := ""
	for _, tr := range rows[1:] {
		v := strings.TrimSpace(cellText(tr, vi))
		switch {
		case v == "":
			return ""
		case one == "":
			one = v
		case v != one:
			return ""
		}
	}
	return one
}

// draftIndexOf は、その表がページの何枚目の発注部材表かを返します（1始まり）。
//
// ⚠ **鏡は表ごとに呼ばれる**ので、自分が何枚目かは自分では分かりません。
// `MirrorContext.Counter` が形式ごとの文書順の連番を配るので、それを使います。
func draftIndexOf(ctx *cms.MirrorContext) int {
	return ctx.Counter(OrderDraftType) + 1
}

// draftFormHTML は「部材発注書を作る」の欄です。
//
// ⚠ **仕入先と差出人はここで決めます**——未発注の表ではありません。
// **部材発注書は1枚に1社**で、その1社が決まるのは**この表を作り終えたとき**だからです。
// supplier は仕入先の欄の初めの値です（空なら空の欄）。
func draftFormHTML(user *auth.User, pageID string, idx int, sig, supplier string) string {
	f := func(id, label, ph, typ string) string {
		return searchFieldHTML("draft", id, label, ph, typ)
	}
	return `<div class="matsearch-form draft-form" data-draft-page="` +
		stdhtml.EscapeString(pageID) + `" data-draft-index="` + strconv.Itoa(idx) + `" data-draft-sig="` + stdhtml.EscapeString(sig) + `">` +
		signerFieldHTML(user) +
		strings.Replace(f("supplier", "仕入先", "みなと商店", "text"), `/></label>`, ` value="`+stdhtml.EscapeString(supplier)+`"/></label>`, 1) +
		// 相乗り（2026-10-21・order_join.go）——ページ番号を書けば、新しく作らずその部材発注書に足す。候補は同じ仕入先のまだ送っていない部材発注書。
		joinFieldHTML(user, supplier) +
		f("order_at", "部材発注日", "", "date") +
		f("due", "納期", "", "date") +
		f("note", "備考", "定尺で結構です", "text") +
		`<button type="button" class="matsearch-go" data-draft-go="1">部材発注書を作る</button>` +
		`</div><div class="unorder-result" data-draft-result="1"></div>`
}

// signerFieldHTML は差出人を選ぶ欄です（2026-09-22）。
//
// ユーザー:「担当者によって変わるので**都度選ぶ**しかないのでは？」——だから
// **毎回ここで選びます**。
//
// ⚠ **候補はサーバーが描きます**（`<select>`）。`推奨業者` のような打ちながらの候補に
// しないのは、差出人が**打つものではなく選ぶもの**だからです——連絡帳に居る人しか
// なれません。（2026-10-19 に推奨業者の候補の仕組みは外し、表のセルの候補はコアのオートコンプリートに統一しました——vocab.go。）
//
// ⚠ **初期値はログイン名と同じ題の人**（`DefaultSigner`）。当たらなければ**未選択**で、
// 人が選びます。⚠ **勝手に先頭の人を選ばないこと**——**誰の名前で紙が出るか**は、
// 黙って決めてよいことではありません。
func signerFieldHTML(user *auth.User) string {
	list := Signers(user)
	def, hasDef := DefaultSigner(user, list)

	var b strings.Builder
	b.WriteString(`<label class="matsearch-field"><span>差出人</span>`)
	b.WriteString(`<select class="matsearch-input" data-unorder="signer">`)
	b.WriteString(`<option value="">（選んでください）</option>`)
	for _, sg := range list {
		sel := ""
		if hasDef && sg.PageID == def.PageID {
			sel = ` selected`
		}
		label := sg.Title
		if sg.Org != "" {
			label += "（" + sg.Org + "）"
		}
		b.WriteString(`<option value="` + page.FormatID(sg.PageID) + `"` + sel + `>` +
			stdhtml.EscapeString(label) + `</option>`)
	}
	b.WriteString(`</select></label>`)
	if len(list) == 0 {
		// ⚠ **0件も黙りません**——「誰も居ない」と「連絡帳を作っていない」は別です。
		b.WriteString(`<p class="unorder-help">⚠ 差出人の候補がありません` +
			`（連絡帳の担当者ページに「<strong>` + OrderSignatureHeading +
			`</strong>」の見出しで署名を書くと、ここに出ます）。</p>`)
	}
	return b.String()
}
