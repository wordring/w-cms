package cms

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/database"
)

// ── 移行第4段: 計算ビュー（子ページ一覧・手配集計）のサーバー事前描画 ──────────

// TestRenderComputedViews は計算ビューのマーカーへサーバーが中身（vocab-chrome）を
// 埋めることを検証します——子ページ一覧のリンクとタイトルのエスケープ、手配集計表、
// data-id の保持、マーカー内の紛れ込み内容の破棄。
func TestRenderComputedViews(t *testing.T) {
	setupSaveTest(t)

	// 親 60 の子として 61 を用意（タイトルにHTML特殊文字を含める）
	mustExec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := database.DB.Exec(q, args...); err != nil {
			t.Fatalf("SQL準備エラー: %v", err)
		}
	}
	mustExec(`INSERT INTO pages (id, title, file_path, parent_id) VALUES (60, '受注ページ', '', NULL)`)
	mustExec(`INSERT INTO pages (id, title, file_path, parent_id) VALUES (61, '<加工>記録', '', 60)`)

	// 印は見出し（D-2）。見出しの下の人の書き込みは本文として残り、鏡はその下へ描かれる
	// （語彙モデル §11.5-7）。前回描いたクロームが紛れ込んでいても描き直す。
	body := `<h1>受注</h1>` +
		`<section data-id="v1"><h2>子ページ一覧</h2><p>人の書き込み</p>` +
		`<div class="vocab-chrome"><p>前回のクローム</p></div></section>`

	req := httptest.NewRequest("GET", "/000060", nil)
	req = auth.WithUser(req, &auth.User{Username: "tester", IsAdmin: true})
	out := RenderComputedViews(req, 60, body)

	for _, want := range []string{
		`<section data-id="v1"><h2>子ページ一覧</h2>`, // 印と data-id は保存内容のまま
		`人の書き込み`,
		`class="vocab-chrome"`, `contenteditable="false"`,
		`href="/000061"`, `&lt;加工&gt;記録`, // 子リンク＋タイトルのエスケープ
	} {
		if !strings.Contains(out, want) {
			t.Errorf("描画結果に %q がありません:\n%s", want, out)
		}
	}
	if strings.Contains(out, "前回のクローム") {
		t.Errorf("前回描いたクロームが残っています:\n%s", out)
	}
}

// TestRetiredAttributeViewMarkerIsNotRendered は、**名前の見えない印**
// （`<section data-type="child-list">`）では鏡が描かれないことを固定します。
// 2026-09-27 に廃止（利用者:「名前の見えない印は廃止して削除して欲しい」）——鏡は
// 見出しだけで名乗ります。ここが緑のまま属性の印を読むように戻ると、画面に名前の
// 出ない鏡がまた作れてしまいます。
func TestRetiredAttributeViewMarkerIsNotRendered(t *testing.T) {
	setupSaveTest(t)
	req := httptest.NewRequest("GET", "/000060", nil)
	req = auth.WithUser(req, &auth.User{Username: "tester", IsAdmin: true})
	for _, body := range []string{
		`<section data-type="child-list"></section>`,
		`<h1>子ページ一覧</h1><section data-type="child-list"></section>`, // 早道を抜けても描かない
	} {
		if out := RenderComputedViews(req, 60, body); strings.Contains(out, "vocab-chrome") {
			t.Errorf("廃止した印で鏡が描かれました:\n%s", out)
		}
	}
	if got := ViewMarkerHTML("child-list"); got != `<section><h2>子ページ一覧</h2></section>` {
		t.Errorf("ViewMarkerHTML が見出しの印を返していません: %s", got)
	}
}

// TestRenderComputedViewsAnonymous は匿名の閲覧者に対して、実効公開でない子ページが
// 一覧に出ないことを検証します（/api/children と同じ絞り込みを共用しているため）。
func TestRenderComputedViewsAnonymous(t *testing.T) {
	setupSaveTest(t)

	if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, file_path, parent_id) VALUES (62, '非公開の子', '', 60)`); err != nil {
		t.Fatalf("SQL準備エラー: %v", err)
	}

	body := ViewMarkerHTML("child-list")
	req := httptest.NewRequest("GET", "/000060", nil) // 匿名（WithUser なし）
	out := RenderComputedViews(req, 60, body)

	if strings.Contains(out, "非公開の子") {
		t.Errorf("匿名に非公開の子ページが見えています:\n%s", out)
	}
	if !strings.Contains(out, "子ページはありません") {
		t.Errorf("空表示の文言がありません:\n%s", out)
	}
}

// TestRenderComputedViewsNoMarker はマーカーの無い本文がそのまま返る
// （パース・再直列化による変化が無い）ことを検証します。
func TestRenderComputedViewsNoMarker(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>通常ページ</h1><p>本文</p>`
	req := httptest.NewRequest("GET", "/000060", nil)
	if out := RenderComputedViews(req, 60, body); out != body {
		t.Errorf("マーカーの無い本文が変化しました:\ngot  %q\nwant %q", out, body)
	}
}
