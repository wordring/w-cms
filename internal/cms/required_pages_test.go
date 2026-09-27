package cms

// 拡張が要る置き場のテスト（2026-09-16）。
//
// 固定するのは**約束**です——冪等であること・本文に作業面が入ること・
// コアの置き場（テンプレート）は素の w-cms でも登録されること。

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// TestRequiredPagesIncludeCoreTemplate は、**コアだけでも表が空にならない**ことを
// 固定します。拡張が1つも無いビルド（`-tags minimal`）でこの仕組みが空っぽだと、
// 管理画面に「何もありません」とだけ出て、壊れているのか正常なのか分かりません。
func TestRequiredPagesIncludeCoreTemplate(t *testing.T) {
	found := false
	for _, p := range RequiredPages() {
		if p.Title == TemplateRootTitle {
			found = true
			if p.Extension != "" {
				t.Errorf("テンプレート置き場はコアの持ち物のはずです: extension=%q", p.Extension)
			}
			if strings.TrimSpace(p.Why) == "" {
				t.Error("理由が空です（押す人が判断できません）")
			}
		}
	}
	if !found {
		t.Errorf("テンプレート置き場が登録されていません: %d件", len(RequiredPages()))
	}
}

// TestRegisterRequiredPageRejectsDuplicate は、題の重複をその場で落とすことを
// 固定します。**本文が2通りになると、先に押したほうが勝つ**という説明のつかない
// 形になるためです。
func TestRegisterRequiredPageRejectsDuplicate(t *testing.T) {
	const title = "重複の試験用"
	defer delete(requiredPageRegistry, title)
	RegisterRequiredPage(RequiredPage{Title: title, Extension: "a"})

	defer func() {
		if recover() == nil {
			t.Error("同じ題を2度登録できてしまいました（取り付けの誤りは起動時に落とすこと）")
		}
	}()
	RegisterRequiredPage(RequiredPage{Title: title, Extension: "b"})
}

// TestRegisterRequiredPageRejectsEmptyTitle は、空の題を落とすことを固定します
// （題が機能を決めるので、空は「名前で引けないページ」になります）。
func TestRegisterRequiredPageRejectsEmptyTitle(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("空の題を登録できてしまいました")
		}
	}()
	RegisterRequiredPage(RequiredPage{Title: "   "})
}

// TestRequiredPageBodiesCarryWorkSurface は、**見出しだけの箱を作らない**ことを
// 固定します。
//
// 2026-09-11 に取引先ページが見出しだけで作られ、**アドレス帳の作業面がどこにも
// 存在しないまま実メール100通が過ぎました**（誰も一覧を見たことがなかった）。
// 同じことが 09-16 の一掃でも起き、今度は「登録しないと作業面が出ず、作業面が
// 無いと登録できない」という行き止まりになりました。
//
// **作業面を持つべき置き場**（`Body` を宣言しているもの）は、本文に計算ビューの
// マーカーを含むこと。⚠ 受注の箱だけは例外で、**見るビューがまだありません**
// （納期・受注残が未実装）——実装したら `Body` を足し、ここの表からも外すこと。
func TestRequiredPageBodiesCarryWorkSurface(t *testing.T) {
	for _, p := range RequiredPages() {
		if p.Body == nil {
			continue // 見出しだけの箱（受注・テンプレート置き場）
		}
		body := p.Body()
		if !strings.Contains(body, "<h1>") {
			t.Errorf("%s: 本文に h1 がありません（題が機能を決めるのに）", p.Title)
		}
		if !hasWorkSurface(body) {
			t.Errorf("%s: 本文に作業面がありません（見出しだけのページは行き止まりになります）: %s",
				p.Title, body)
		}
	}
}

// hasWorkSurface は本文に作業面（鏡の印）があるかを返します。印は data-mirror で名乗る
// （名前の見えない `<section data-type=…>` の印は 2026-09-27 に廃止）ので、
// 文字列ではなく形式の見分け方（vocabTypeOf）で探します。
func hasWorkSurface(body string) bool {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return false
	}
	found := false
	for _, n := range nodes {
		WalkElements(n, func(el *html.Node) {
			if def, ok := VocabDefByType(vocabTypeOf(el)); ok && def.View {
				found = true
			}
		})
	}
	return found
}

// TestRequiredPageBodyCheckSeesViewMarkers は、上の番人が**空振りしない**ことを固定します
// ——コアのパッケージでは本文を持つ置き場が登録されないので、上のループは何も確かめて
// いませんでした（2026-09-27 に分かった）。見分け方そのものをここで確かめ、拡張の置き場の
// 本文は拡張の試験（`TestBoxBodiesCarryViewMarkers`）が確かめます。
func TestRequiredPageBodyCheckSeesViewMarkers(t *testing.T) {
	if !hasWorkSurface("<h1>箱</h1>" + ViewMarkerHTML("child-list")) {
		t.Error("鏡の印（data-mirror）を作業面と見ていません")
	}
	if hasWorkSurface(`<h1>箱</h1><section data-type="child-list"></section>`) {
		t.Error("廃止した名前の見えない印を作業面と見ています")
	}
	if hasWorkSurface("<h1>箱</h1><p>説明だけ</p>") {
		t.Error("見出しだけの本文を作業面ありと見ています")
	}
}

// TestRequiredPageStatusReportsDuplicates は、**同じ題が2枚あることを知らせる**ことを
// 固定します（2026-09-16）。
//
// 題が機能を決めるので、2枚あると `TopLevelPageByTitle` は片方しか返さず、
// **もう片方は誰からも見えないまま残ります**——取引先で実際に起きました
// （E2Eが作った1枚と手で作った1枚）。**防げない**（題は人が自由に付けられる）ので、
// 知らせるしかありません。
//
// あわせて「**使われるのはいちばん古いほう**」も固定します。2026-09-16 まで
// `LIMIT 1` に `ORDER BY` が無く、**どちらが返るかは決まっていませんでした**。
func TestRequiredPageStatusReportsDuplicates(t *testing.T) {
	db := newTestFileDB(t)
	// トップと、同じ題のページを2枚（新しいほうを先に入れて、並びで拾わないことも見る）。
	for _, p := range []struct {
		id    int
		title string
	}{{0, "トップ"}, {900, "テンプレート"}, {800, "テンプレート"}} {
		if _, err := db.Exec(
			`INSERT INTO pages (id, title, parent_id, file_path) VALUES (?, ?, 0, '')`,
			p.id, p.title); err != nil {
			t.Fatalf("下ごしらえの挿入エラー: %v", err)
		}
	}

	var st RequiredPageStatus
	for _, s := range RequiredPageStatuses() {
		if s.Title == TemplateRootTitle {
			st = s
		}
	}
	if st.PageID != "000800" {
		t.Errorf("使われるのはいちばん古いページのはずです: %q（000800 を期待）", st.PageID)
	}
	if len(st.Duplicates) != 1 || st.Duplicates[0] != "000900" {
		t.Errorf("余りのページを知らせていません: %v（[000900] を期待）", st.Duplicates)
	}

	// 1枚だけなら知らせない（普通の状態で警告が出ると、見る人が慣れて無視します）。
	if _, err := db.Exec(`DELETE FROM pages WHERE id = 900`); err != nil {
		t.Fatalf("片付けエラー: %v", err)
	}
	for _, s := range RequiredPageStatuses() {
		if s.Title == TemplateRootTitle && len(s.Duplicates) != 0 {
			t.Errorf("1枚だけなのに警告が出ています: %v", s.Duplicates)
		}
	}
}

// TestEnsureTopLevelBoxUsesRegisteredBody は、**置き場を作る道が2本とも同じ本文になる**ことを
// 固定します（2026-09-27・テンプレート駆動の A）。整理などの途中で作る `EnsureTopLevelBox` は
// それまで見出しだけで作っていたので、管理画面の「足りない置き場を作る」と中身が違い、
// 整理で先に作られた「受注」には鏡が無いままでした。
func TestEnsureTopLevelBoxUsesRegisteredBody(t *testing.T) {
	setupSaveTest(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	orig := requiredPageRegistry
	requiredPageRegistry = map[string]RequiredPage{}
	for k, v := range orig {
		requiredPageRegistry[k] = v
	}
	t.Cleanup(func() { requiredPageRegistry = orig })
	requiredPageRegistry["試しの箱"] = RequiredPage{Title: "試しの箱",
		Body: func() string { return "<h1>試しの箱</h1>" + ViewMarkerHTML("child-list") }}

	id, err := EnsureTopLevelBox("試しの箱", "alice")
	if err != nil {
		t.Fatalf("EnsureTopLevelBox: %v", err)
	}
	body, err := ReadPageBody(id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `data-mirror="子ページ一覧"`) {
		t.Errorf("登録の本文（鏡の印入り）で作られていません: %s", body)
	}
	if again, _ := EnsureTopLevelBox("試しの箱", "alice"); again != id {
		t.Errorf("既にある箱を作り直しました: %s → %s", id, again)
	}

	id2, err := EnsureTopLevelBox("登録の無い箱", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := ReadPageBody(id2); strings.TrimSpace(body) != "<h1>登録の無い箱</h1>" {
		t.Errorf("登録の無い題は見出しだけのはず: %s", body)
	}
}
