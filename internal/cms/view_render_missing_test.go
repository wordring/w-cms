package cms

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestComputedViewWithoutRendererShowsReason は、計算ビューとして宣言されたのに
// 中身を作る処理が用意されていない形式が、**無言の空白ではなく理由を表示する**
// ことを検証します。
//
// かつては RenderComputedViews が既知の2形式を名指しで拾っていたため、レジストリへ
// View: true の形式を足して描画側を足し忘れると、マーカーが空のまま何も起きず、
// テストも通ってしまいました（＝黙って壊れる）。
func TestComputedViewWithoutRendererShowsReason(t *testing.T) {
	// レジストリへ一時的に「描画処理の無いビュー」を足す。
	orig := vocabRegistry
	vocabRegistry = append(append([]VocabDef{}, vocabRegistry...), VocabDef{
		Type:        "orphan-view",
		DisplayName: "描画処理の無いビュー",
		Element:     "section",
		View:        true,
	})
	t.Cleanup(func() { vocabRegistry = orig })

	req := httptest.NewRequest("GET", "/000001", nil)
	got := RenderComputedViews(req, 1, `<section data-type="orphan-view"></section>`)

	if strings.Contains(got, `<section data-type="orphan-view"></section>`) {
		t.Fatalf("マーカーが空のまま素通りしています:\n%s", got)
	}
	if !strings.Contains(got, "orphan-view") || !strings.Contains(got, "view-error") {
		t.Errorf("理由が表示されていません:\n%s", got)
	}
}

// TestComputedViewDispatchComesFromRegistry は、描画の振り分けがレジストリの
// View 宣言に基づくこと（名指しのリストではないこと）を検証します。
// 宣言していない data-type は素通しのままであることも併せて固定します。
func TestComputedViewDispatchComesFromRegistry(t *testing.T) {
	req := httptest.NewRequest("GET", "/000001", nil)
	body := `<section data-type="not-a-view"><p>そのまま</p></section>`
	if got := RenderComputedViews(req, 1, body); got != body {
		t.Errorf("ビューでない section が書き換えられています:\n%s", got)
	}
}
