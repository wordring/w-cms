package cms

import "testing"

// 法人格の畳み込みは**候補を探すためだけ**のものです（normalize_company.go の冒頭）。
// ここで固定するのは3つ——落ちること・落とし過ぎないこと・空にしないこと。

func TestFoldCompanyName(t *testing.T) {
	withCompanyForms(t, func(s *Settings) {
		s.CompanyForms = []string{"株式会社", "(株)", "有限会社", "(有)", "合同会社"}
	})

	same := []string{
		"株式会社南北スポーツ機械",  // 前株
		"南北スポーツ機械(株)",   // 後株
		"㈱南北スポーツ機械",     // NFKC が (株) へ開いてから落ちる
		"株式会社　南北スポーツ機械", // 全角空白つき
		"南北スポーツ機械",      // 素
	}
	want := "南北スポーツ機械"
	for _, s := range same {
		if got := FoldCompanyName(s); got != want {
			t.Errorf("FoldCompanyName(%q) = %q; want %q", s, got, want)
		}
	}

	// ⚠ **真ん中は落としません**——社名そのものに含まれることがあるためです。
	if got := FoldCompanyName("日本株式会社工業"); got != "日本株式会社工業" {
		t.Errorf("真ん中まで落としています: %q", got)
	}
	// ⚠ **空にしません**——空の鍵は他の全部と一致してしまいます。
	if got := FoldCompanyName("株式会社"); got == "" {
		t.Error("法人格だけの値が空になりました（全部と一致する鍵になる）")
	}
}

// TestSameCompanyNeedsBothSides は、**別会社を1つに潰さない**ことを固定します。
//
// 法人格を落とすと `株式会社あさひ` と `有限会社あさひ` が同じ鍵になります。
// **実在しうる別会社**なので、この関数の出口は判定ではなく**提案**です
// ——ここが「同じ」と答えるのは承知のうえで、決めるのは人。
func TestSameCompany(t *testing.T) {
	withCompanyForms(t, func(s *Settings) {
		s.CompanyForms = []string{"株式会社", "有限会社"}
	})

	if !SameCompany("株式会社あさひ", "あさひ") {
		t.Error("法人格の有無で同じ会社と見なせていません")
	}
	// 空どうしは同じにしない（空の鍵で全部が一致するのを防ぐ）。
	if SameCompany("", "") {
		t.Error("空どうしを同じ会社と見なしています")
	}
	if SameCompany("あさひ", "あさひ工業") {
		t.Error("別の社名を同じと見なしています")
	}
}

// TestFoldCompanyNameWithoutSettings は、**設定が空なら何も落とさない**ことを固定します。
// 既定の表をコードに持たない、という決定（設定が唯一の正本）がここに出ます。
func TestFoldCompanyNameWithoutSettings(t *testing.T) {
	withCompanyForms(t, func(s *Settings) { s.CompanyForms = nil })
	if got := FoldCompanyName("株式会社南北"); got != "株式会社南北" {
		t.Errorf("設定が空なのに落としています: %q", got)
	}
}

// withCompanyForms は設定を差し替え、**必ず戻します**。
//
// ⚠ `withSettings`（webdav_test.go）は後始末をしません。この漏れで、前の subtest の
// 設定が次へ流れて別の試験が落ちたことがあります（2026-09-07）——同じ轍を踏まないよう、
// ここは自分で戻す形にしています。
func withCompanyForms(t *testing.T, apply func(*Settings)) {
	t.Helper()
	settingsMu.Lock()
	saved := settings
	cp := *settings
	apply(&cp)
	settings = &cp
	settingsMu.Unlock()
	t.Cleanup(func() {
		settingsMu.Lock()
		settings = saved
		settingsMu.Unlock()
	})
}
