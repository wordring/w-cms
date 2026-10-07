package cms

import (
	"os"
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// カルーセル（2026-10-07・vocab.go の "carousel"）の番人です。
// 利用者:「カルーセルを入れることは出来ますか？」→ 形は「『カルーセル』ブロックを挿す」。
// 見せ方は画面だけが持つので、サーバーが守るのは「形式として登録されている」と「保存で中身が落ちない」の2つ。

// TestCarouselIsRegistered は、カルーセルがスラッシュメニューの「基本」に出る形式として登録されていることを確かめます。
func TestCarouselIsRegistered(t *testing.T) {
	for _, d := range VocabDefs() {
		if d.Type != "carousel" {
			continue
		}
		if d.DisplayName != "カルーセル" || d.Category != "基本" || d.Element != "section" || d.Hidden || d.View || len(d.Columns) != 0 {
			t.Errorf("カルーセルの宣言 = %+v", d)
		}
		return
	}
	t.Fatal("カルーセルが登録されていません")
}

// TestCarouselSurvivesSave は、カルーセルの節と中の画像の段落が保存でそのまま残り、未知の形式として告知されない
// ことを確かめます。
func TestCarouselSurvivesSave(t *testing.T) {
	setupSaveTest(t)
	const id = "000059"
	if err := page.WriteSidecar(id, page.PageMeta{Owner: "tester", Mode: page.DefaultMode}); err != nil {
		t.Fatal(err)
	}
	body := `<h1>写真</h1><section data-type="carousel"><p data-id="ab12"><img src="/000059/ab12.png" alt="a.png"/></p>` +
		`<p data-id="cd34"><img src="/000059/cd34.jpg" alt="b.jpg"/></p></section>`
	resp := postSave(t, id, body)
	if u, _ := resp["unknown_types"].([]interface{}); len(u) != 0 {
		t.Errorf("カルーセルが未知の形式として告知されています: %v", u)
	}
	saved, err := os.ReadFile(page.BodyPath(id))
	if err != nil {
		t.Fatal(err)
	}
	s := string(saved)
	for _, want := range []string{`<section data-type="carousel">`, `src="/000059/ab12.png"`, `src="/000059/cd34.jpg"`} {
		if !strings.Contains(s, want) {
			t.Errorf("保存した本文に %s がありません: %s", want, s)
		}
	}
}
