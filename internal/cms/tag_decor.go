package cms

// ─────────────────────────────────────────────────────────────────────────
// タグの飾り（2026-10-20・docs/考察/【考察】画面の JS のコアと拡張.md §4.5）
//
// 2026-10-20 利用者:「拡張の機能は、出来る限りタグとして実装してほしいです」（本文に置く印〔鏡の印〕の意味）。
//
// 拡張が「名前：値」のタグの値に飾りを足す口です（例: 通信の ☎ 発信は `電話番号`、連絡帳の「未分類へ戻す」は `メールアドレス`）。
// それまでは拡張の JS がページを見て、タグを探して足していました。いまはサーバーが、タグの並び（`dl[data-type="tags"]`）を
// 描くときに、登録された飾りを値の欄（dd）の末尾へ足します——本文の印はタグそのもの。
//
//   - 飾りは**本文ではありません**。拡張は `vocab-chrome` の印を付けて返し、保存のときに落ちます（StripChrome・画面の保存の巡り）。
//   - 鏡なので、閲覧と書ける形の両方で描かれます（/api/load と ?view=1）。押したときの働きは、拡張の JS が document へ委ねて配線する。
//   - 鏡型の引き金は1つに1人なので、`tags` の引き金はコアが持ち、拡張の飾りはここへ登録します。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strings"

	"golang.org/x/net/html"
)

// TagDecorator は、タグの値 value に足す飾り（値の欄の末尾に足すノード）を返します。足さないなら nil。
type TagDecorator func(ctx *MirrorContext, value string) []*html.Node

// tagDecorators は名前 → 飾りです。**`init()` の中からだけ**登録します。
var tagDecorators = map[string][]TagDecorator{}

// RegisterTagDecorator は、名前 name のタグの値に飾りを足す働きを登録します（拡張の `init()` から呼ぶ）。
func RegisterTagDecorator(name string, fn TagDecorator) {
	tagDecorators[name] = append(tagDecorators[name], fn)
}

func init() {
	RegisterMirror(TagsDataType, MirrorHandlerFunc(decorateTags))
}

// decorateTags は、タグの並びの dt/dd の組のうち、飾りの登録された名前の値の欄へ飾りを足します。
func decorateTags(ctx *MirrorContext, el *html.Node) (bool, error) {
	if el.Data != "dl" || len(tagDecorators) == 0 {
		return true, nil
	}
	for dt := el.FirstChild; dt != nil; dt = dt.NextSibling {
		if dt.Type != html.ElementNode || dt.Data != "dt" {
			continue
		}
		fns := tagDecorators[strings.TrimSpace(nodeText(dt))]
		if len(fns) == 0 {
			continue
		}
		dd := dt.NextSibling
		for dd != nil && dd.Type != html.ElementNode {
			dd = dd.NextSibling
		}
		if dd == nil || dd.Data != "dd" {
			continue
		}
		value := strings.TrimSpace(nodeText(dd))
		if value == "" {
			continue
		}
		for _, fn := range fns {
			for _, n := range fn(ctx, value) {
				dd.AppendChild(n)
			}
		}
	}
	return true, nil
}

// hasDecoratedTag は、本文に飾りの登録された名前のタグ（`<dt>名前</dt>`）がありそうかです（鏡の早道・偽陽性はかまわない）。
func hasDecoratedTag(bodyHTML string) bool {
	for name := range tagDecorators {
		if strings.Contains(bodyHTML, ">"+name+"<") {
			return true
		}
	}
	return false
}
