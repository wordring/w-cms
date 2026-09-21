package subcon

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// 「推奨業者」の入力候補（2026-09-21）。ユーザー選択:「**文字列だが、候補を出す**」。
//
// ⚠ **候補は縛りではありません**——採らずに手で打てます。試験が固定するのは
// 「**出るべきものが出る**」「**出してはいけないものが出ない**」の2つです。

func seedPartners(t *testing.T, secretOwner string) {
	t.Helper()
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 50, 0, CustomerBoxTitle, "root", "302", true)
	addPage(t, 51, 50, "ひかり加工", "root", "302", true)
	addPage(t, 52, 50, "ふじ鍍金", "root", "302", true)
	addPage(t, 53, 50, "秘密の外注先", secretOwner, "300", false)
	addPage(t, 54, 0, "よそのページ", "root", "302", true) // 取引先の下ではない
	for _, p := range []struct {
		id    int
		title string
	}{{50, CustomerBoxTitle}, {51, "ひかり加工"}, {52, "ふじ鍍金"},
		{53, "秘密の外注先"}, {54, "よそのページ"}} {
		syncBody(t, p.id, "<h1>"+p.title+"</h1>")
	}
}

// TestSuggestPartnersReturnsPartnerBoxChildren は、**取引先の下の社名だけ**が
// 候補になることを固定します。
func TestSuggestPartnersReturnsPartnerBoxChildren(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedPartners(t, "root")

	got := suggestPartners(&auth.User{Username: "root", IsAdmin: true}, "")
	joined := strings.Join(got, " / ")
	for _, want := range []string{"ひかり加工", "ふじ鍍金"} {
		if !strings.Contains(joined, want) {
			t.Errorf("候補に %q がありません: %s", want, joined)
		}
	}
	// ⚠ **取引先の下でないページは候補にしません**（誰でも作れる普通のページ）。
	if strings.Contains(joined, "よそのページ") {
		t.Errorf("⚠ 取引先の下でないページが候補に出ています: %s", joined)
	}
	// ⚠ **箱そのものも候補にしません。**
	if strings.Contains(joined, CustomerBoxTitle) {
		t.Errorf("⚠ 取引先の箱そのものが候補に出ています: %s", joined)
	}
}

// TestSuggestPartnersFiltersByQuery は、打ちかけの文字で絞ることを固定します。
//
// ⚠ **畳んで比べます**——全角・半角カナの揺れを越えるため。
func TestSuggestPartnersFiltersByQuery(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedPartners(t, "root")
	root := &auth.User{Username: "root", IsAdmin: true}

	if got := suggestPartners(root, "レーザー"); len(got) != 1 || got[0] != "ひかり加工" {
		t.Errorf("絞り込みが効いていません: %v", got)
	}
	if got := suggestPartners(root, "ﾚｰｻﾞｰ"); len(got) != 1 {
		t.Errorf("⚠ 半角カナが畳めていません: %v", got)
	}
	if got := suggestPartners(root, "存在しない"); len(got) != 0 {
		t.Errorf("当たらないはずが当たっています: %v", got)
	}
}

// TestSuggestPartnersHidesUnreadable は、⚠ **読めない相手は候補にしない**ことを
// 固定します。**題そのものが情報**です（誰と取引しているか）。
func TestSuggestPartnersHidesUnreadable(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedPartners(t, "alice")

	mallory := &auth.User{Username: "mallory"}
	if page.GetPerms(53).CanRead(mallory) {
		t.Fatal("前提が崩れています: mallory は秘密の外注先を読めてはいけません")
	}
	got := strings.Join(suggestPartners(mallory, ""), " / ")
	if strings.Contains(got, "秘密の外注先") {
		t.Fatalf("⚠ 読めない相手が候補に出ています: %s", got)
	}
	if !strings.Contains(got, "ふじ鍍金") {
		t.Errorf("読める相手まで落としています: %s", got)
	}
}
