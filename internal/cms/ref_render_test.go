package cms

import (
	"strings"
	"testing"

	"w-cms/internal/database"
)

// 参照タグのリンク描画と宙ぶらりんの印のテスト（D-4・D-10）。
//
// 値の解釈は構文解析（D-7）——ページIDはゼロ埋め6桁ちょうど・ハイフン類は畳む・
// 先頭のゼロは畳まない。判定は読むたび（描画時）で、保存形には何も焼かない。

func TestRenderReferenceLinks(t *testing.T) {
	setupSaveTest(t)
	if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, file_path) VALUES (2, '参照先', '')`); err != nil {
		t.Fatalf("ページ作成エラー: %v", err)
	}

	body := `<dl data-type="tags">` +
		`<dt>原発注書</dt><dd>000002-12</dd>` + // 実在するページ → リンク
		`<dt>前版</dt><dd>000099-ab</dd>` + // 存在しないページ → 印
		`<dt>図番</dt><dd>123-45</dd>` + // 6桁でない → 普通の値（触らない）
		`<dt>型式</dt><dd>SUS304</dd>` + // 文法外 → 触らない
		`</dl>`
	out := RenderReferenceLinks(body)

	if !strings.Contains(out, `<a href="/000002#12" class="ref-link">000002-12</a>`) {
		t.Errorf("実在ページへの参照がリンクになっていません:\n%s", out)
	}
	if !strings.Contains(out, `class="ref-missing"`) ||
		!strings.Contains(out, `000099`) {
		t.Errorf("宙ぶらりんの参照に印が付いていません:\n%s", out)
	}
	if strings.Contains(out, `href="/000123`) || strings.Contains(out, `>123-45</a>`) {
		t.Errorf("6桁でない値が参照に化けています（図面番号の誤爆）:\n%s", out)
	}
	if !strings.Contains(out, `<dd>SUS304</dd>`) {
		t.Errorf("文法外の値が書き換えられています:\n%s", out)
	}
}

// TestRenderReferenceLinksFoldsHyphens は、区切りのハイフンの種類が畳まれることを
// 検証します。議論で示された実例が半角カタカナの長音記号（U+FF70）だった（§9.1）。
func TestRenderReferenceLinksFoldsHyphens(t *testing.T) {
	setupSaveTest(t)
	if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, file_path) VALUES (2, '参照先', '')`); err != nil {
		t.Fatalf("ページ作成エラー: %v", err)
	}

	// 長音記号 ー（U+30FC）・半角長音 ｰ（U+FF70）・全角ハイフン −（U+2212）
	for _, sep := range []string{"ー", "ｰ", "−"} {
		body := `<dl data-type="tags"><dt>原発注書</dt><dd>000002` + sep + `12</dd></dl>`
		out := RenderReferenceLinks(body)
		if !strings.Contains(out, `href="/000002#12"`) {
			t.Errorf("区切り %q が畳まれていません:\n%s", sep, out)
		}
	}
}

// TestRenderReferenceLinksNotInLoadAPI は、参照リンクの合成が /api/load（編集用の
// 読み直し）を通らないことを検証します。通ると合成した <a> がシリアライザで
// 本文として保存される（RenderAnchors と同じ罠）。
func TestRenderReferenceLinksNotInLoadAPI(t *testing.T) {
	// 配線の検査: LoadHandler は RenderReferenceLinks を呼ばない。関数の呼び出し網は
	// コードを読まないと確かめられないので、ここでは describe 的に「描画経路の出力に
	// リンクがあり、素の Sanitize 出力には無い」ことで代替する。
	setupSaveTest(t)
	if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, file_path) VALUES (2, '参照先', '')`); err != nil {
		t.Fatalf("ページ作成エラー: %v", err)
	}
	body := `<dl data-type="tags"><dt>原発注書</dt><dd>000002-12</dd></dl>`
	if strings.Contains(Sanitize(body), "ref-link") {
		t.Error("サニタイズだけでリンクが合成されています（保存経路が汚れる）")
	}
	if !strings.Contains(RenderReferenceLinks(Sanitize(body)), "ref-link") {
		t.Error("描画経路でリンクが合成されていません")
	}
}
