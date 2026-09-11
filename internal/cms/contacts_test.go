package cms

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// TestPartnerBoxHasWorkSurface は、機械が作る「取引先」ページに**アドレス帳の作業面が
// 最初から載っている**ことを固定します。
//
// 2026-09-11 に実データで分かったこと: ここを空の見出しだけで作っていたために、
// **「未登録の連絡先」の一覧がどこにも存在しませんでした**。実メール100通のあいだ
// 誰も一覧を見ておらず、11ドメインのうち登録済みは1件だけ——しかもそれは整理が
// 作った側で、識別子（メールアドレス）を持っていませんでした。
//
// 通信箱は人が意図して置くページなので作業面も人が入れます。**取引先は機械が作る**
// ので、行き止まりのページを作らない責任はこちらにあります。
func TestPartnerBoxHasWorkSurface(t *testing.T) {
	setupTemplateAPITest(t)
	user := &auth.User{Username: "alice", IsAdmin: true}
	newPage(t, TopPageID, "<h1>トップ</h1>",
		page.PageMeta{Owner: "alice", Mode: page.DefaultMode})

	boxID, err := EnsurePartnerBox(user)
	if err != nil {
		t.Fatalf("EnsurePartnerBox: %v", err)
	}
	body, err := ReadPageBody(boxID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `data-type="unknown-contacts"`) {
		t.Errorf("作業面（未登録の連絡先）が載っていません: %s", body)
	}
	if !strings.Contains(body, "<h1>"+PartnerBoxTitle+"</h1>") {
		t.Errorf("題が %s になっていません: %s", PartnerBoxTitle, body)
	}

	// 2度目は作り直さない（同じページを返す）。
	again, err := EnsurePartnerBox(user)
	if err != nil {
		t.Fatal(err)
	}
	if again != boxID {
		t.Errorf("取引先ページが2枚できました: %s と %s", boxID, again)
	}
}
