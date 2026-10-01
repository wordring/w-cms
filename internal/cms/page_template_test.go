package cms

import (
	"errors"
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// 機械が作るページのテンプレート（page_template.go）の番人です。
// 利用者:「テンプレートにはスラッシュメニューから表などの印を置き、コードはそれを
// 埋めてはどうでしょう？問題になりそうなのは、繰り返しの場合だと思います」——
// **テンプレートは1つ分の形、数は機械がその場で決める**を確かめます。

// タグは同じ名前をその場で要るだけ繰り返し、並びはテンプレートの位置のまま。
func TestDraftRepeatsTagsInPlace(t *testing.T) {
	d := NewPageDraft("受信メール", `<h1>受信メール</h1><dl data-type="tags">`+
		`<dt>向き</dt><dd><br/></dd><dt>宛先</dt><dd><br/></dd><dt>件名メモ</dt><dd><br/></dd></dl>`)
	d.SetTitle("お見積りのお願い")
	d.SetTag("向き", "受信")
	d.SetTag("宛先", "a@example.jp", " ", "b@example.jp", "c@example.jp")
	got := d.HTML()
	want := `<h1>お見積りのお願い</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd>` +
		`<dt>宛先</dt><dd>a@example.jp</dd><dt>宛先</dt><dd>b@example.jp</dd><dt>宛先</dt><dd>c@example.jp</dd>` +
		`<dt>件名メモ</dt><dd><br/></dd></dl>`
	if got != want {
		t.Errorf("タグの繰り返しが違います:\n got %s\nwant %s", got, want)
	}
}

// テンプレートに無いタグは、値があれば足し（利用者の選択「足す」）、空なら足さない。
// 値が空ならテンプレートの欄をそのまま残す。
func TestDraftAddsMissingTagsOnlyWithValue(t *testing.T) {
	d := NewPageDraft("t", `<h1>t</h1><dl data-type="tags"><dt>発注元</dt><dd><br/></dd></dl><p>x</p>`)
	d.SetTag("発注元", "")
	d.SetTag("受信元", "000123-ab12")
	d.SetTag("CC")
	got := d.HTML()
	want := `<h1>t</h1><dl data-type="tags"><dt>発注元</dt><dd><br/></dd><dt>受信元</dt><dd>000123-ab12</dd></dl><p>x</p>`
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}

	// 可変タグが無いテンプレートには、h1 の直後に作る。
	d2 := NewPageDraft("t", `<h1>t</h1><p>電話番号：</p>`)
	d2.SetTag("メールアドレス", "a@example.jp")
	if got := d2.HTML(); got != `<h1>t</h1><dl data-type="tags"><dt>メールアドレス</dt><dd>a@example.jp</dd></dl><p>電話番号：</p>` {
		t.Errorf("可変タグを h1 の直後に作っていません: %s", got)
	}
}

// 表は見出しの言葉で列を合わせ、行をその場で要るだけ作る。テンプレートで列を並べ替えても・
// 足しても崩れない。無い列は値があれば右端へ足し、見本の行は消す。
func TestDraftFillsTableByHeaderWords(t *testing.T) {
	tmpl := `<h1>t</h1><table><caption>受注明細</caption><tbody>` +
		`<tr><th>品名</th><th>弊社品番</th><th>社内メモ</th><th>数量</th></tr>` +
		`<tr><td>見本</td><td></td><td></td><td></td></tr></tbody></table>`
	d := NewPageDraft("受注ページ", tmpl)
	cols := []string{"弊社品番", "品番", "品名", "数量", "単位", "状態"}
	rows, err := d.FillTable("受注明細", cols, [][]string{
		{"", "", "ブラケット", "4", "", "未着手"},
		{"", "", "台座", "2", "", "未着手"},
		{"", "", "カバー", "1", "", "未着手"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Errorf("行の数が違います: %d", len(rows))
	}
	got := d.HTML()
	// 品番・単位は値が無いので足さない。状態は値があるので右端へ足す。
	want := `<h1>t</h1><table><caption>受注明細</caption><tbody>` +
		`<tr><th>品名</th><th>弊社品番</th><th>社内メモ</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td>ブラケット</td><td></td><td></td><td>4</td><td>未着手</td></tr>` +
		`<tr><td>台座</td><td></td><td></td><td>2</td><td>未着手</td></tr>` +
		`<tr><td>カバー</td><td></td><td></td><td>1</td><td>未着手</td></tr></tbody></table>`
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}

	// 0件なら見本の行を残す（人が書けるように）。
	d0 := NewPageDraft("受注ページ", tmpl)
	if _, err := d0.FillTable("受注明細", cols, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d0.HTML(), "<td>見本</td>") {
		t.Errorf("0件のとき見本の行を消しました: %s", d0.HTML())
	}
}

// 値を入れる器（表・見出しの節）が無ければ、作らずに断る。
func TestDraftRefusesWithoutSlot(t *testing.T) {
	d := NewPageDraft("受注ページ", `<h1>t</h1><table><caption>別の表</caption><tbody><tr><th>a</th></tr></tbody></table>`)
	_, err := d.FillTable("受注明細", []string{"品名"}, [][]string{{"x"}})
	var se *TemplateSlotError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "受注明細") || !strings.Contains(err.Error(), "受注ページ") {
		t.Errorf("表の無いテンプレートを断っていません: %v", err)
	}
	if _, err := d.RequireContainer("本文"); !errors.As(err, &se) {
		t.Errorf("節の無いテンプレートを断っていません: %v", err)
	}
}

// 中身は名前の見える入れ物（見出しの節・畳める枠）に入れ、見出しは残す。
func TestDraftFillsNamedContainers(t *testing.T) {
	d := NewPageDraft("t", `<h1>t</h1><section><h2>本文</h2><p><br/></p></section>`+
		`<details><summary>顧客の発注書（読んだまま）</summary><p>ここに表</p></details>`)
	body, ok := d.Container("本文")
	if !ok {
		t.Fatal("見出しの節を見つけられません")
	}
	body.SetContent(`<pre>こんにちは</pre>`)
	src, ok := d.Container("顧客の発注書（読んだまま）")
	if !ok {
		t.Fatal("畳める枠を見つけられません")
	}
	src.SetContent(`<table><caption>顧客の発注書（読んだまま）</caption><tbody><tr><th>図番</th></tr></tbody></table>`)
	want := `<h1>t</h1><section><h2>本文</h2><pre>こんにちは</pre></section>` +
		`<details><summary>顧客の発注書（読んだまま）</summary><table><caption>顧客の発注書（読んだまま）</caption><tbody><tr><th>図番</th></tr></tbody></table></details>`
	if got := d.HTML(); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// 節の中の値は節の中へ入れる（加工製品の図面ブロック）。ファイル表示は未配線のものへ。
// 写すときにブロックIDを外し、定義リスト・表の中の字下げを落とす。
func TestDraftScopesToSectionAndCleansLayout(t *testing.T) {
	tmpl := "<h1 data-id=\"0gg4\">加工製品</h1>\n<dl data-id=\"vui2\" data-type=\"tags\">\n  <dt>品番</dt>\n  <dd><br/></dd>\n</dl>\n" +
		"<section data-id=\"oyut\">\n  <h2>図面</h2>\n  <dl data-type=\"tags\">\n    <dt>図面番号</dt>\n    <dd><br/></dd>\n    <dt>客先</dt>\n    <dd><br/></dd>\n  </dl>\n" +
		"  <section data-id=\"jcan\" data-ref=\"\" data-type=\"file-view\"></section>\n</section>"
	d := NewPageDraft("加工製品", tmpl)
	dr, err := d.RequireContainer("図面")
	if err != nil {
		t.Fatal(err)
	}
	dr.SetTag("図面番号", "A100-B01-003")
	dr.SetTag("受信元", "000123-ab12")
	dr.SetTag("対応DXF", "000123-cd34", "000123-ef56")
	if !dr.SetFileView("000123-ab12") {
		t.Error("未配線のファイル表示へ配線していません")
	}
	id := d.AssignBlockID(dr.Node())
	got := d.HTML()
	for _, want := range []string{
		`<dl data-type="tags"><dt>品番</dt><dd><br/></dd></dl>`,
		`<dt>図面番号</dt><dd>A100-B01-003</dd><dt>客先</dt><dd><br/></dd><dt>受信元</dt><dd>000123-ab12</dd><dt>対応DXF</dt><dd>000123-cd34</dd><dt>対応DXF</dt><dd>000123-ef56</dd></dl>`,
		`<section data-ref="000123-ab12" data-type="file-view"></section>`,
		`<section data-id="` + id + `">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%s が入っていません:\n%s", want, got)
		}
	}
	for _, old := range []string{"0gg4", "vui2", "oyut", "jcan"} {
		if strings.Contains(got, old) {
			t.Errorf("テンプレートのブロックID %s が残っています", old)
		}
	}
	// 配線済みのファイル表示は触らない。
	d2 := NewPageDraft("t", `<section data-type="file-view" data-ref="000001-aaaa"></section>`)
	if d2.SetFileView("000002-bbbb") || !strings.Contains(d2.HTML(), "000001-aaaa") {
		t.Errorf("配線済みのファイル表示を書き換えました: %s", d2.HTML())
	}
}

// テンプレートは題で引き、無ければ作らない。管理画面の状態にも出る。
func TestPageTemplatesAreLookedUpByTitle(t *testing.T) {
	setupSaveTest(t)
	branch := newTemplateTree(t) // トップ 000000・テンプレート置き場 000010・分類 000011
	newPage(t, "000012", `<h1 data-id="ab12">試しの記録</h1><dl data-type="tags"><dt>向き</dt><dd><br/></dd></dl>`,
		page.PageMeta{Owner: "alice", Mode: page.DefaultMode, ParentID: branch})
	orig := pageTemplateRegistry
	pageTemplateRegistry = map[string]PageTemplate{}
	t.Cleanup(func() { pageTemplateRegistry = orig })
	RegisterPageTemplate(PageTemplate{Title: "試しの記録", Why: "試し"})
	RegisterPageTemplate(PageTemplate{Title: "雛形の無い記録", Why: "試し"})

	d, err := DraftFromTemplate("試しの記録")
	if err != nil {
		t.Fatal(err)
	}
	d.SetTitle("本物の記録")
	d.SetTag("向き", "受信")
	if got := d.HTML(); got != `<h1>本物の記録</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd></dl>` {
		t.Errorf("テンプレートの写しになっていません: %s", got)
	}
	if _, err := DraftFromTemplate("雛形の無い記録"); !errors.Is(err, ErrNoPageTemplate) ||
		!strings.Contains(err.Error(), "雛形の無い記録") {
		t.Errorf("テンプレートの無いページを作ろうとしました: %v", err)
	}
	for _, st := range PageTemplateStatuses() {
		switch st.Title {
		case "試しの記録":
			if st.PageID != "000012" || st.Problem != "" {
				t.Errorf("在るテンプレートが状態に出ていません: %+v", st)
			}
		case "雛形の無い記録":
			if st.PageID != "" || st.Problem == "" {
				t.Errorf("無いテンプレートが状態に出ていません: %+v", st)
			}
		}
	}
}

// TestDraftDropsUnwiredFileView は、配線しないファイル表示を**包んでいる折りたたみごと**消し、ほかの中身がある
// 入れ物は残すことを固定します（2026-10-01・メールの本文から作る受注ページ——原本の PDF が無い）。
func TestDraftDropsUnwiredFileView(t *testing.T) {
	d := NewPageDraft("受注ページ", `<h1>受注ページ</h1>`+
		`<details><summary>原本（PDF）</summary><section data-type="file-view" data-ref=""></section></details>`+
		`<details><summary>資料</summary><p>説明</p><section data-type="file-view" data-ref=""></section></details>`)
	if !d.DropFileView() {
		t.Fatal("消すファイル表示が見つかりません")
	}
	got := d.HTML()
	if strings.Contains(got, "原本（PDF）") {
		t.Errorf("中身の無くなった折りたたみが残っています:\n%s", got)
	}
	if !strings.Contains(got, "<summary>資料</summary><p>説明</p><section") {
		t.Errorf("2つ目（まだ消していない）の入れ物を壊しています:\n%s", got)
	}
	if !d.DropFileView() || !strings.Contains(d.HTML(), "<summary>資料</summary><p>説明</p></details>") {
		t.Errorf("ほかの中身がある入れ物は残すはず:\n%s", d.HTML())
	}
	if d.DropFileView() {
		t.Error("もう無いのに消したと言っています")
	}
}
