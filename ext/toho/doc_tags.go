package toho

// 書類のページを組むときの小さな道具（2026-10-20 に analyze_pdf.go から分けた）——列の見出しを宣言から引く・ヘッダのタグを書く。
// 解析・納品書・請求書・製造ページ・部材見積依頼書などが使う。

import (
	"strings"

	"w-cms/internal/cms"
)

// labelOf は形式の列の見出しを宣言から返します（未登録なら空）。
func labelOf(vocabType, field string) string {
	for _, c := range columnsOf(vocabType) {
		if c.Field == field {
			return c.Label
		}
	}
	return ""
}

// setHeaderTag はタグ name に値を入れます（空値なら何もしない＝テンプレートの空欄のまま、
// あとから人が埋める）。
//
// 日付・数値の見出し語（発注日 等）は**正規形へ揃えてから**書きます
// （D-3・`cms.CanonicalForIngest`）。図面番号や客先名は揃えません——
// 機械が畳んで書き換えると、原本と見比べたときに食い違うためです。
// 参照（`受信元`・`対応DXF`）もここを通ります——`ref` 型は正規化の対象外なので、
// 値はそのまま入ります。
func setHeaderTag(b cms.DraftBlock, name string, values ...string) {
	var vals []string
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			vals = append(vals, cms.CanonicalForIngest(name, v))
		}
	}
	b.SetTag(name, vals...)
}
