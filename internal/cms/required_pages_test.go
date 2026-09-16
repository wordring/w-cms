package cms

// 拡張が要る置き場のテスト（2026-09-16）。
//
// 固定するのは**約束**です——冪等であること・本文に作業面が入ること・
// コアの置き場（テンプレート）は素の w-cms でも登録されること。

import (
	"strings"
	"testing"
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
		if !strings.Contains(body, "<section data-type=") {
			t.Errorf("%s: 本文に作業面がありません（見出しだけのページは行き止まりになります）: %s",
				p.Title, body)
		}
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
