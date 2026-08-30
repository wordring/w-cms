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

	// 手配集計の材料: SHAFT-01 は鋼材1本が要る。受注10本・発注済み4本 → 残6
	seedPageTag(t, 3, "部品番号", "SHAFT-01")
	seedVocabTable(t, 3, "part-materials", 0, map[string]string{
		"部材名": "鋼材", "単価": "2500", "仕入先": "東邦", "数量": "1",
	})
	seedVocabBlock(t, 60, "client-order", 0, map[string]string{
		"発注書番号": "PO-V1", "発注元": "トーア",
	})
	seedVocabTable(t, 60, "client-order-items", 0, map[string]string{
		"品番": "SHAFT-01", "品名": "シャフト", "単価": "8000", "数量": "10", "状態": "加工中",
	})
	seedVocabBlock(t, 60, "our-order", 0, map[string]string{
		"発注書番号": "PO-OUR-V1", "発注先": "東邦",
	})
	seedVocabTable(t, 60, "our-order-items", 0, map[string]string{
		"品名": "鋼材", "単価": "2500", "数量": "4", "状態": "未納品",
	})

	body := `<h1>受注</h1>` +
		`<section data-type="child-list" data-id="v1"><p>紛れ込んだ内容</p></section>` +
		`<section data-type="required-materials" data-id="v2"></section>`

	req := httptest.NewRequest("GET", "/000060", nil)
	req = auth.WithUser(req, &auth.User{Username: "tester", IsAdmin: true})
	out := RenderComputedViews(req, 60, body)

	for _, want := range []string{
		`data-type="child-list"`, `data-id="v1"`, // マーカーと data-id は保存内容のまま
		`class="vocab-chrome"`, `contenteditable="false"`,
		`href="/000061"`, `&lt;加工&gt;記録`, // 子リンク＋タイトルのエスケープ
		`data-type="required-materials"`,
		`部材手配・発注進捗状況`, `鋼材`, `東邦`,
		`<td class="num">10</td>`, `<td class="num">4</td>`, `<td class="num">6</td>`,
		`要手配 (6)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("描画結果に %q がありません:\n%s", want, out)
		}
	}
	if strings.Contains(out, "紛れ込んだ内容") {
		t.Errorf("マーカー内の紛れ込み内容が表示に乗っています")
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

	body := `<section data-type="child-list"></section>`
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
