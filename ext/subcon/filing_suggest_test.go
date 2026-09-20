package subcon

import (
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"

	"w-cms/ext/comm/contacts"
)

// 顧客名の推奨は3段です（`suggestCustomer`）:
//
//	① アドレスの鎖（受信元 → 通信記録 → 差出人 → 組織 → その題）…完全一致・推測ゼロ
//	② 連絡帳の候補（法人格を落として1件に絞れたら）        …2026-09-20 に足した
//	③ 読んだ名前                                          …新しい客先はここ
//
// ここで固定するのは②です。**①が効くときは①のまま**であることも一緒に見ます。

// TestSuggestCustomerFallsBackToContactsOrg は、鎖が切れたときに連絡帳の題へ
// 寄せることを固定します。
//
// ⚠ **これは体裁の話ではありません。** 整理は `PartnerByTitle`（**完全一致**）で
// 加工製品の木と連絡帳を結びます（`linkPartner`）。人が `株式会社南北…` と打つと
// 連絡帳の `南北…` に当たらず、**2つの木が黙って結ばれません**——`linkPartner`
// は「結べないことは異常ではありません」と黙って戻るので、誰も気づけません。
func TestSuggestCustomerFallsBackToContactsOrg(t *testing.T) {
	setupFilingTest(t, "000100")
	user := &auth.User{Username: "alice", IsAdmin: true}

	// 連絡帳に「南北スポーツ機械」（法人格なし）が居る。
	boxID, err := contacts.EnsureContactsBox(user)
	if err != nil {
		t.Fatalf("連絡帳を作れません: %v", err)
	}
	newOrg(t, user, boxID, "南北スポーツ機械")

	// 由来の鎖が無い加工製品ページ（＝新しい客先の1通目・社内からの転送の形）。
	pageID := makeDrawingPage(t, "000100", "X1", "部品", "装置", "株式会社南北スポーツ機械")
	idInt := mustAtoi(t, pageID)

	got := suggestCustomer(user, idInt, "株式会社南北スポーツ機械")
	if got != "南北スポーツ機械" {
		t.Errorf("連絡帳の題へ寄せていません: %q\n"+
			"（このままだと linkPartner が完全一致で引けず、2つの木が黙って結ばれません）", got)
	}
}

// TestSuggestCustomerKeepsUnknownName は、連絡帳に居ない相手は**読んだまま**を
// 固定します。新しい客先は居ないのが正常で、推測で埋めてはいけません。
func TestSuggestCustomerKeepsUnknownName(t *testing.T) {
	setupFilingTest(t, "000100")
	user := &auth.User{Username: "alice", IsAdmin: true}
	if _, err := contacts.EnsureContactsBox(user); err != nil {
		t.Fatalf("連絡帳を作れません: %v", err)
	}

	pageID := makeDrawingPage(t, "000100", "X1", "部品", "装置", "株式会社まだ居ない商会")
	idInt := mustAtoi(t, pageID)

	const read = "株式会社まだ居ない商会"
	if got := suggestCustomer(user, idInt, read); got != read {
		t.Errorf("居ない相手を書き換えています: %q; want %q", got, read)
	}
}

// newOrg は連絡帳の直下へ組織ページを1枚作ります。
func newOrg(t *testing.T, user *auth.User, boxID, title string) string {
	t.Helper()
	id, err := cms.CreateChildPage(boxID, user.Username, "<h1>"+title+"</h1>")
	if err != nil {
		t.Fatalf("組織 %s を作れません: %v", title, err)
	}
	return id
}

func mustAtoi(t *testing.T, id string) int {
	t.Helper()
	n, ok := page.NormalizeID(id)
	if !ok {
		t.Fatalf("ページIDが不正です: %q", id)
	}
	v := 0
	for _, r := range n {
		v = v*10 + int(r-'0')
	}
	return v
}
