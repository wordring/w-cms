package cms

import (
	"strings"
	"testing"
)

// TestSanitizeRemovesDangerous は危険な要素・属性・URLが除去されることを検証します。
func TestSanitizeRemovesDangerous(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		mustNot  []string // 出力に含まれてはいけない部分文字列
		mustHave []string // 出力に残っていてほしい部分文字列
	}{
		{
			name:    "scriptは中身ごと消える",
			in:      `<p>前</p><script>alert(1)</script><p>後</p>`,
			mustNot: []string{"script", "alert"},
			// 前後の本文は残す
			mustHave: []string{"<p>前</p>", "<p>後</p>"},
		},
		{
			name:     "styleブロックも消える",
			in:       `<style>body{display:none}</style><p>本文</p>`,
			mustNot:  []string{"style", "display:none"},
			mustHave: []string{"<p>本文</p>"},
		},
		{
			name:     "onイベント属性は消えるが要素は残る",
			in:       `<p onclick="alert(1)" onmouseover="x()">本文</p>`,
			mustNot:  []string{"onclick", "onmouseover", "alert"},
			mustHave: []string{"<p>本文</p>"},
		},
		{
			name:     "style属性は消える",
			in:       `<p style="color:red">本文</p>`,
			mustNot:  []string{"style", "color:red"},
			mustHave: []string{"<p>本文</p>"},
		},
		{
			name:     "javascript:のhrefは属性ごと消える",
			in:       `<a href="javascript:alert(1)">リンク</a>`,
			mustNot:  []string{"javascript", "href"},
			mustHave: []string{"リンク"},
		},
		{
			name:     "タブを挟んだjavascript:も弾く",
			in:       "<a href=\"java\tscript:alert(1)\">リンク</a>",
			mustNot:  []string{"script", "href"},
			mustHave: []string{"リンク"},
		},
		{
			name:     "iframeとobjectは消える",
			in:       `<iframe src="http://evil.example"></iframe><object data="x"></object><p>本文</p>`,
			mustNot:  []string{"iframe", "object", "evil.example"},
			mustHave: []string{"<p>本文</p>"},
		},
		{
			name:     "formとinputは消える",
			in:       `<form action="/x"><input name="a"></form><p>本文</p>`,
			mustNot:  []string{"<form", "<input"},
			mustHave: []string{"<p>本文</p>"},
		},
		{
			name:     "相対URLとhttpsのリンクは残る",
			in:       `<a href="/000123">内部</a><a href="https://example.com">外部</a>`,
			mustNot:  []string{"javascript"},
			mustHave: []string{`href="/000123"`, `href="https://example.com"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Sanitize(tc.in)
			for _, s := range tc.mustNot {
				if strings.Contains(got, s) {
					t.Errorf("除去されるべき %q が残っている:\n入力: %s\n出力: %s", s, tc.in, got)
				}
			}
			for _, s := range tc.mustHave {
				if !strings.Contains(got, s) {
					t.Errorf("保持されるべき %q が失われた:\n入力: %s\n出力: %s", s, tc.in, got)
				}
			}
		})
	}
}

// TestSanitizeKeepsCustomElements は6種のカスタム要素と許可属性が保持されることを検証します。
// 許可リストは docs/【一覧】カスタムタグ.md と同期していること。
func TestSanitizeKeepsCustomElements(t *testing.T) {
	in := `<m-tag name="発注元" value="株式会社テスト"></m-tag>` +
		`<m-file src="po.pdf" name="発注書.pdf" tag="顧客の発注書" order-no="PO-1" client-name="得意先" ordered-at="2026-06-18">` +
		`<m-item item-id="A-1" item-name="部品" price="1200" quantity="20" status="未着手"></m-item>` +
		`</m-file>` +
		`<m-material item-name="鋼材" cost="500" supplier-name="材料屋" quantity="2"></m-material>` +
		`<m-required-materials page-id="000123"></m-required-materials>` +
		`<m-child-list></m-child-list>`

	got := Sanitize(in)

	for _, want := range []string{
		`<m-tag name="発注元" value="株式会社テスト">`,
		`<m-file src="po.pdf"`, `tag="顧客の発注書"`, `order-no="PO-1"`,
		`<m-item item-id="A-1"`, `status="未着手"`,
		`<m-material item-name="鋼材"`, `supplier-name="材料屋"`,
		`<m-required-materials page-id="000123">`,
		`<m-child-list>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("カスタム要素/属性 %q が失われた:\n出力: %s", want, got)
		}
	}
}

// TestSanitizeDropsUnknownAttributes は許可リストに無い属性が落ちることを検証します。
func TestSanitizeDropsUnknownAttributes(t *testing.T) {
	got := Sanitize(`<m-tag name="A" value="B" data-evil="x" class="y" id="z"></m-tag>`)
	for _, notWant := range []string{"data-evil", "class", "id="} {
		if strings.Contains(got, notWant) {
			t.Errorf("許可されていない属性 %q が残っている: %s", notWant, got)
		}
	}
	if !strings.Contains(got, `name="A"`) || !strings.Contains(got, `value="B"`) {
		t.Errorf("許可属性が失われた: %s", got)
	}
}

// TestSanitizeUnwrapsUnknownElements は未知の要素がアンラップされ、中身が残ることを検証します。
func TestSanitizeUnwrapsUnknownElements(t *testing.T) {
	got := Sanitize(`<section><article><p>本文</p></article></section>`)
	if strings.Contains(got, "section") || strings.Contains(got, "article") {
		t.Errorf("未知の要素が残っている: %s", got)
	}
	if !strings.Contains(got, "<p>本文</p>") {
		t.Errorf("アンラップ後に中身が失われた: %s", got)
	}
}

// TestSanitizeDropsLegacyParentTag は旧形式の親ページIDタグが取り込まれないことを検証します
// （ページ属性はサイドカー <id>.meta.json が正本）。
func TestSanitizeDropsLegacyParentTag(t *testing.T) {
	got := Sanitize(`<m-tag name="親ページID" value="000000"></m-tag><m-tag name="発注元" value="X"></m-tag>`)
	if strings.Contains(got, "親ページID") {
		t.Errorf("旧形式の親ページIDタグが残っている: %s", got)
	}
	if !strings.Contains(got, "発注元") {
		t.Errorf("通常のm-tagまで落ちている: %s", got)
	}
}

// TestSanitizeIsIdempotent は冪等性を検証します。保存時にサニタイズ結果をエディタへ返す
// エコーバック方式は、これが成り立たないと保存のたびに内容が変化して収束しません。
func TestSanitizeIsIdempotent(t *testing.T) {
	inputs := []string{
		`<p>ふつうの本文</p>`,
		`<p onclick="alert(1)">危険</p><script>x</script>`,
		`<m-file src="a.pdf" name="n" tag="顧客の発注書"><m-item item-name="部品" quantity="1" status=""></m-item></m-file>`,
		`<section><p>入れ子</p></section>`,
		`<a href="javascript:alert(1)">リンク</a>`,
		`<p>記号 &amp; と &lt;タグ風&gt; のテキスト</p>`,
	}
	for _, in := range inputs {
		once := Sanitize(in)
		twice := Sanitize(once)
		if once != twice {
			t.Errorf("冪等でない:\n入力: %s\n1回目: %s\n2回目: %s", in, once, twice)
		}
	}
}

// TestSanitizeReportChanged は「何かを除去したか」のフラグが、整形の差では立たず
// 意味のある除去でだけ立つことを検証します。
func TestSanitizeReportChanged(t *testing.T) {
	if _, changed := SanitizeReport(`<p>ふつうの本文</p>`); changed {
		t.Error("除去が無いのに changed=true になっている")
	}
	if _, changed := SanitizeReport(`<m-tag name="A" value="B"></m-tag>`); changed {
		t.Error("カスタム要素だけの入力で changed=true になっている")
	}
	if _, changed := SanitizeReport(`<p onclick="alert(1)">本文</p>`); !changed {
		t.Error("on* を除去したのに changed=false になっている")
	}
	if _, changed := SanitizeReport(`<script>alert(1)</script>`); !changed {
		t.Error("script を除去したのに changed=false になっている")
	}
}

// TestSanitizeAttributeInjection はシリアライザが属性値をエスケープし損ねた場合でも、
// 注入された on* 属性がサーバー側で落ちることを検証します
// （updateHtmlPreview は m-tag の value を素で埋め込むため、value に " を含めると
// 属性を注入できてしまう。その最終防壁）。
func TestSanitizeAttributeInjection(t *testing.T) {
	// value="x" onload="alert(1)" が生成されてしまった状況を模す
	got := Sanitize(`<m-tag name="n" value="x" onload="alert(1)"></m-tag>`)
	if strings.Contains(got, "onload") || strings.Contains(got, "alert") {
		t.Errorf("注入された on* 属性が残っている: %s", got)
	}
}
