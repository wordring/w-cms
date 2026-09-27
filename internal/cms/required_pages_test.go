package cms

// 拡張が要る置き場のテスト（2026-09-16）。
//
// 固定するのは**約束**です——冪等であること・本文に作業面が入ること・
// コアの置き場（テンプレート）は素の w-cms でも登録されること。

import (
	"errors"
	"strings"
	"testing"

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

// TestBoxesAreMadeOnlyFromTemplates は、**置き場はテンプレートからだけ作られる**ことを固定します
// （2026-09-27 利用者:「コードにハードコーディングせず、テンプレート駆動にしたい」「テンプレートが
// 無ければ作れないまで行きます」）。
//
//   - 同じ題のテンプレート（テンプレート置き場の下の葉）を**純粋にコピー**して作る（ブロックIDは外す）
//   - テンプレートが無ければ作らない（`ErrNoBoxTemplate`）——管理画面の「足りない置き場を作る」も
//     ほかの置き場は作り続け、作れなかったものを返す
//   - 同じ題のテンプレートが2枚あれば、どちらで作るか決められないので作らない
//   - テンプレート置き場そのものはテンプレートの入れ物なので、見出しだけで作る
func TestBoxesAreMadeOnlyFromTemplates(t *testing.T) {
	setupSaveTest(t)
	branch := newTemplateTree(t) // トップ 000000・テンプレート置き場 000010・分類 000011
	newPage(t, "000012", `<h1 data-id="ab12">試しの箱</h1><p>説明</p>`+ViewMarkerHTML("child-list"),
		page.PageMeta{Owner: "alice", Mode: page.DefaultMode, ParentID: branch})
	orig := requiredPageRegistry
	requiredPageRegistry = map[string]RequiredPage{}
	for k, v := range orig {
		requiredPageRegistry[k] = v
	}
	t.Cleanup(func() { requiredPageRegistry = orig })
	requiredPageRegistry["試しの箱"] = RequiredPage{Title: "試しの箱"}
	requiredPageRegistry["雛形の無い箱"] = RequiredPage{Title: "雛形の無い箱"}

	if id, err := BoxTemplateID("試しの箱"); err != nil || id != "000012" {
		t.Fatalf("テンプレートを引けません: %q %v", id, err)
	}
	for _, st := range RequiredPageStatuses() {
		if st.Title == "試しの箱" && (st.Template != "000012" || st.NoTemplate) {
			t.Errorf("状態にテンプレートが出ていません: %+v", st)
		}
		if st.Title == "雛形の無い箱" && !st.NoTemplate {
			t.Errorf("テンプレートが無いことが状態に出ていません: %+v", st)
		}
	}

	created, err := CreateMissingRequiredPages("alice")
	if !errors.Is(err, ErrNoBoxTemplate) || !strings.Contains(err.Error(), "雛形の無い箱") {
		t.Errorf("テンプレートの無い置き場を知らせていません: %v", err)
	}
	var boxID string
	for _, c := range created {
		if c.Title == "雛形の無い箱" {
			t.Errorf("テンプレートが無いのに作りました: %+v", c)
		}
		if c.Title == "試しの箱" {
			boxID = c.PageID
		}
	}
	body, err := ReadPageBody(boxID)
	if err != nil {
		t.Fatalf("テンプレートのある置き場を作っていません: %v", err)
	}
	if !strings.Contains(body, `data-mirror="子ページ一覧"`) || !strings.Contains(body, "<p>説明</p>") {
		t.Errorf("テンプレートのコピーになっていません: %s", body)
	}
	if strings.Contains(body, `data-id="ab12"`) {
		t.Errorf("ブロックIDを外していません: %s", body)
	}
	if again, _ := EnsureTopLevelBox("試しの箱", "alice"); again != boxID {
		t.Errorf("既にある置き場を作り直しました: %s → %s", boxID, again)
	}
	if _, err := EnsureTopLevelBox("雛形の無い箱", "alice"); !errors.Is(err, ErrNoBoxTemplate) {
		t.Errorf("EnsureTopLevelBox がテンプレートの無い置き場を断っていません: %v", err)
	}

	// 同じ題のテンプレートが2枚
	newPage(t, "000013", `<h1>試しの箱</h1>`, page.PageMeta{Owner: "alice", Mode: page.DefaultMode, ParentID: branch})
	if _, err := BoxTemplateID("試しの箱"); err == nil || errors.Is(err, ErrNoBoxTemplate) {
		t.Errorf("同じ題のテンプレートが2枚あるのに引けてしまいました: %v", err)
	}

	// テンプレート置き場そのものは見出しだけで作れる
	if body, err := boxBodyFromTemplate(TemplateRootTitle); err != nil || body != "<h1>"+TemplateRootTitle+"</h1>" {
		t.Errorf("テンプレート置き場の本文が違います: %q %v", body, err)
	}
}
