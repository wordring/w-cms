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

// TestSanitizeDropsLegacyCustomElements は旧カスタム要素（<m-*>）がアンラップされ、
// 属性ごと落ちることを検証します。語彙モデルへの移行完了（2026-08-20）で語彙宣言を
// 撤去したため、旧要素は「未知の要素」として扱われます——中身のテキストは残るので
// 万一の未変換データでも文章は失われません。
func TestSanitizeDropsLegacyCustomElements(t *testing.T) {
	in := `<m-tag name="発注元" value="株式会社テスト"></m-tag>` +
		`<m-file src="po.pdf" name="発注書.pdf">` +
		`<m-client-order order-no="PO-1"><p>本文は残る</p></m-client-order>` +
		`</m-file>` +
		`<m-child-list></m-child-list>`

	got := Sanitize(in)

	for _, notWant := range []string{"m-tag", "m-file", "m-client-order", "m-child-list", "order-no", "src="} {
		if strings.Contains(got, notWant) {
			t.Errorf("旧カスタム要素の痕跡 %q が残っています: %s", notWant, got)
		}
	}
	if !strings.Contains(got, "<p>本文は残る</p>") {
		t.Errorf("アンラップで中身まで失われました: %s", got)
	}
}

// TestSanitizeDropsUnknownAttributes は許可リストに無い属性が落ちることを検証します。
func TestSanitizeDropsUnknownAttributes(t *testing.T) {
	got := Sanitize(`<dl data-type="tags" data-evil="x" class="y" id="z"><dt>A</dt><dd>B</dd></dl>`)
	for _, notWant := range []string{"data-evil", "class", "id="} {
		if strings.Contains(got, notWant) {
			t.Errorf("許可されていない属性 %q が残っている: %s", notWant, got)
		}
	}
	if !strings.Contains(got, `data-type="tags"`) {
		t.Errorf("許可属性が失われた: %s", got)
	}
}

// TestSanitizeUnwrapsUnknownElements は未知の要素がアンラップされ、中身が残ることを検証します。
func TestSanitizeUnwrapsUnknownElements(t *testing.T) {
	// 許可リストにも危険リストにも無い要素はアンラップされ、中身だけが残る。
	got := Sanitize(`<unknown-thing><weird><p>本文</p></weird></unknown-thing>`)
	if strings.Contains(got, "unknown-thing") || strings.Contains(got, "weird") {
		t.Errorf("未知の要素が残っている: %s", got)
	}
	if !strings.Contains(got, "<p>本文</p>") {
		t.Errorf("アンラップ後に中身が失われた: %s", got)
	}
}

// TestSanitizeKeepsStructuralElements は、方針「タグは寛容」に従って追加した要素が
// 通ることを検証します（docs/本文サニタイズ設計.md §5.2）。
func TestSanitizeKeepsStructuralElements(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"定義リスト", `<dl><dt>用語</dt><dd>説明</dd></dl>`, "<dl>"},
		{"ルビ（振り仮名）", `<ruby>漢字<rp>(</rp><rt>かんじ</rt><rp>)</rp></ruby>`, "<rt>"},
		{"区分", `<section><article>本文</article></section>`, "<section>"},
		{"折りたたみ", `<details open=""><summary>概要</summary>中身</details>`, "<summary>"},
		{"変更履歴", `<ins datetime="2026-08-04">追加</ins><del>削除</del>`, "<ins"},
		{"上付き・下付き", `H<sub>2</sub>O / m<sup>3</sup>`, "<sub>"},
		{"表の見出し範囲", `<table><tr><th scope="col">列</th></tr></table>`, `scope="col"`},
		{"図", `<figure><figcaption>図1</figcaption></figure>`, "<figcaption>"},
		{"日時", `<time datetime="2026-08-04">8月4日</time>`, "datetime="},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Sanitize(c.in); !strings.Contains(got, c.want) {
				t.Errorf("%q が失われた:\n入力: %s\n出力: %s", c.want, c.in, got)
			}
		})
	}
}

// TestSanitizeURLPolicy は、リンク（外部可）と埋め込み（相対のみ）の違いを検証します。
// docs/本文サニタイズ設計.md §5.5。
func TestSanitizeURLPolicy(t *testing.T) {
	t.Run("リンクは外部URLを許可", func(t *testing.T) {
		for _, in := range []string{
			`<a href="https://example.com/x">外部</a>`,
			`<a href="mailto:a@example.com">メール</a>`,
			`<a href="tel:0312345678">電話</a>`,
			`<a href="/000123">内部</a>`,
		} {
			if got := Sanitize(in); !strings.Contains(got, "href=") {
				t.Errorf("リンクが落ちた:\n入力: %s\n出力: %s", in, got)
			}
		}
	})

	t.Run("リンクでも危険スキームは拒否", func(t *testing.T) {
		for _, in := range []string{
			`<a href="javascript:alert(1)">x</a>`,
			`<a href="data:text/html,<script>alert(1)</script>">x</a>`,
		} {
			if got := Sanitize(in); strings.Contains(got, "href=") {
				t.Errorf("危険なリンクが残った:\n入力: %s\n出力: %s", in, got)
			}
		}
	})

	t.Run("埋め込みは相対URLのみ", func(t *testing.T) {
		if got := Sanitize(`<img src="a.png">`); !strings.Contains(got, `src="a.png"`) {
			t.Errorf("相対URLの画像が落ちた: %s", got)
		}
		for _, in := range []string{
			`<img src="https://evil.example/beacon.png">`,
			`<video src="https://evil.example/v.mp4"></video>`,
			`<img src="//evil.example/beacon.png">`, // プロトコル相対＝別ホスト
		} {
			if got := Sanitize(in); strings.Contains(got, "evil.example") {
				t.Errorf("外部URLの埋め込みが残った（トラッキングビーコンになる）:\n入力: %s\n出力: %s", in, got)
			}
		}
	})

	t.Run("リンクでもプロトコル相対は拒否", func(t *testing.T) {
		if got := Sanitize(`<a href="//evil.example/x">x</a>`); strings.Contains(got, "evil.example") {
			t.Errorf("プロトコル相対URLが残った: %s", got)
		}
	})
}

// TestSanitizeDropsDangerousAdditions は、危険リストへ追加した要素が消えることを検証します。
func TestSanitizeDropsDangerousAdditions(t *testing.T) {
	for _, in := range []string{
		`<dialog open="">画面を覆う</dialog>`,
		`<base href="https://evil.example/">`,
		`<noscript>x</noscript>`,
	} {
		got := Sanitize(in)
		for _, bad := range []string{"dialog", "base", "noscript", "evil.example", "画面を覆う"} {
			if strings.Contains(got, bad) {
				t.Errorf("危険な要素の痕跡が残った（%q）:\n入力: %s\n出力: %s", bad, in, got)
			}
		}
	}
}

// 旧形式の <m-tag name="親ページID"> を取り込まない規則は、m-tag を所有する
// plugin_page_tags.go の責務に移した（サニタイザはカスタム要素の意味に立ち入らない）。
// 検証は page_tags_test.go の TestSyncSkipsLegacyParentTag を参照。

// TestSanitizeIsIdempotent は冪等性を検証します。保存時にサニタイズ結果をエディタへ返す
// エコーバック方式は、これが成り立たないと保存のたびに内容が変化して収束しません。
func TestSanitizeIsIdempotent(t *testing.T) {
	inputs := []string{
		`<p>ふつうの本文</p>`,
		`<p onclick="alert(1)">危険</p><script>x</script>`,
		`<m-file src="a.pdf" name="n"><m-client-order order-no="PO-1"><m-item item-name="部品" quantity="1" status=""></m-item></m-client-order></m-file>`,
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
	if _, changed := SanitizeReport(`<dl data-type="tags"><dt>A</dt><dd>B</dd></dl>`); changed {
		t.Error("マーカー付き標準HTMLだけの入力で changed=true になっている")
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

// TestSanitizeAcceptsEditorOutput は、エディタが書き出すHTMLがサニタイザを
// **無変更で**通ることを検証します（実際の serializeBlock の出力から採取）。
//
// ここが崩れると、保存のたびにサーバーが本文を書き換えて返し、エコーバックで
// ブロックが差し替わってキャレットが飛びます。エディタの語彙は /api/tag-schema
// （＝この許可リスト）から導かれるので、本来ズレないはずのものを念のため固定します。
func TestSanitizeAcceptsEditorOutput(t *testing.T) {
	outputs := []string{
		`<h4 data-id="ab12">章タイトル</h4>`,
		`<p data-id="zz01">これは<strong>太字</strong>と<em>斜体</em>です</p>`,
		`<p data-id="a003">あ<strong><em>いう</em></strong>えお</p>`,
		`<p data-id="a008"><strong>あ<em>いう</em>え</strong></p>`,
		"<ul data-id=\"l001\">\n    <li>あ</li>\n    <li>い</li>\n</ul>",
		"<dl data-id=\"d001\">\n    <dt>語</dt>\n    <dd>説明</dd>\n</dl>",
		"<table data-id=\"t001\">\n    <tbody>\n        <tr>\n            <th scope=\"col\">列</th>\n            <td>値</td>\n        </tr>\n    </tbody>\n</table>",
		`<blockquote data-id="q001" cite="/000123"><p>引用文</p></blockquote>`,
		`<p data-id="r001"><ruby>漢字<rt>かんじ</rt></ruby></p>`,
		`<p data-id="a007"><u>あいう</u></p>`,
	}
	for _, in := range outputs {
		got, changed := SanitizeReport(in)
		if changed || got != in {
			t.Errorf("エディタの出力がサニタイズで変化した（保存のたびに書き換わる）:\n入力: %s\n出力: %s", in, got)
		}
	}

	// 空要素だけは表記が変わる（エディタは `<br>`、Goの描画器は `<br/>` と書く）。
	// これは同じ木の別表記なので changed は立たず、エコーバックも起きない。
	// 正本ファイルには `<br/>` の形で残るが、読み込めば同じDOMになるため実害はない。
	for _, in := range []string{
		`<p data-id="br01">上<br>下</p>`,
		`<p data-id="im01"><img src="a.png" alt="図"></p>`,
	} {
		got, changed := SanitizeReport(in)
		if changed {
			t.Errorf("空要素の表記差でエコーバックが起きている:\n入力: %s\n出力: %s", in, got)
		}
	}
}
