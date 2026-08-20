package cms

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// TestLoadAPISanitizes は GET /api/load が**サニタイズを通す**ことを検証します。
//
// /api/load の中身は populateEditor が属性を濾さずDOMへ入れるため、生のまま返すと
// 保存経路を通っていない本文（手動配置・バックアップ復元・取り込みAPIが直接書いたページ）に
// 仕込まれた `id="w-…"` が**殻の要素を乗っ取ります**（getElementById は文書順で最初を返し、
// 本文の挿入点より後ろに権限UIの入力欄がある）。描画経路（RootHandler）には
// サニタイズ二層目があるのに、ここだけ抜けていました。
func TestLoadAPISanitizes(t *testing.T) {
	setupSaveTest(t)
	// 保存経路を通さずファイルへ直接書いた状況をつくる。
	newPage(t, "000030",
		`<p data-id="zz02" id="w-html-preview">乗っ取りを狙う本文</p><p onclick="alert(1)">危険</p>`,
		page.PageMeta{Owner: "alice", Mode: page.DefaultMode})

	req := httptest.NewRequest("GET", "/api/load?id=000030", nil)
	req = auth.WithUser(req, &auth.User{Username: "alice"})
	rr := httptest.NewRecorder()
	LoadAPIHandler(rr, req)

	body := rr.Body.String()
	if strings.Contains(body, `id="w-html-preview"`) {
		t.Errorf("殻の接頭辞つき id がそのまま返っています: %s", body)
	}
	if !strings.Contains(body, `id="html-preview"`) {
		t.Errorf("接頭辞を剥がした id が残っていません（id ごと落ちた？）: %s", body)
	}
	if strings.Contains(body, "onclick") {
		t.Errorf("危険な属性が残っています: %s", body)
	}
}

// TestRenderAnchorsSkipsChrome は、サーバーが埋めた編集クローム（.vocab-chrome）の中には
// アンカーを合成しないことを検証します。クロームは本文ではないので、
// アンカー名を消費させない（保存もされないので付けても無駄）。
func TestRenderAnchorsSkipsChrome(t *testing.T) {
	in := `<section data-type="required-materials">` +
		`<div class="vocab-chrome"><h3 class="materials-title">📊 部材手配・発注進捗状況</h3></div>` +
		`</section><h2 data-id="a1">本物の見出し</h2>`
	got := RenderAnchors(in)

	if strings.Contains(got, "部材手配・発注進捗状況\"") || strings.Contains(got, `id="📊-部材手配・発注進捗状況"`) {
		t.Errorf("クロームの見出しにアンカーが付いています: %s", got)
	}
	if !strings.Contains(got, `id="本物の見出し"`) {
		t.Errorf("本文の見出しにアンカーが付いていません: %s", got)
	}
}

// TestDeletePageWithMissingDir は、正本フォルダが失われたページでも削除できることを
// 検証します。ここで止めると、DBに残ったページを画面から永久に消せなくなります。
func TestDeletePageWithMissingDir(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000031", "<h1>フォルダが消えたページ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	if err := os.RemoveAll(page.GetPageDir("000031")); err != nil {
		t.Fatalf("フォルダ削除エラー: %v", err)
	}

	token := lockAs(t, 31, "alice")
	rr := postDelete(t, "000031", token, &auth.User{Username: "alice"})
	if rr.Code != 200 {
		t.Fatalf("フォルダの無いページを削除できません: code=%d body=%s", rr.Code, rr.Body.String())
	}
	var n int
	database.DB.QueryRow("SELECT COUNT(*) FROM pages WHERE id = ?", 31).Scan(&n)
	if n != 0 {
		t.Errorf("pages から消えていません: %d 件", n)
	}
	editlock.Locks.ForceRelease(31)
}
