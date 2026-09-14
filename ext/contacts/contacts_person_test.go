package contacts

// 連絡先を人ごとのページへ分けたときの振る舞い（2026-09-13）。
//
// ユーザー:「連絡先にはメールアドレス、電話番号、名前など様々なタグが必要なので、
// ページに分割する必要があります」。実データで、社名ページに `メールアドレス` が
// 6つ平らに並び、**誰のものか分からなくなっていました**（電話番号を足す先も無い）。
//
// 分けると**照合が壊れやすくなります**——アドレスが載っているのが社名ページでは
// なくなるので、「この差出人は誰か」に答えるには祖先を辿って会社へ丸める必要があります。
// ここはその、壊れると整理の顧客名が静かに空振りする部分を固定します。

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// setupPartnerTree は `取引先／社名` まで作って返します。
func setupPartnerTree(t *testing.T) (user *auth.User, companyID string) {
	t.Helper()
	setupTemplateAPITest(t)
	user = &auth.User{Username: "alice", IsAdmin: true}
	newPage(t, cms.TopPageID, "<h1>トップ</h1>",
		page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	boxID, err := EnsurePartnerBox(user)
	if err != nil {
		t.Fatalf("EnsurePartnerBox: %v", err)
	}
	companyID, err = ensureChildByTitle(user, boxID, "トーアスポーツマシーン")
	if err != nil {
		t.Fatalf("社名ページを作れません: %v", err)
	}
	return user, companyID
}

// TestEnsureContactPersonBuildsTree は `取引先／社名／担当者／氏名` ができることを固定します。
//
// **担当者の箱を1枚かませる**のは、社名ページの子に装置名称が並ぶからです
// （2026-09-05 決定）。人を直接ぶら下げると**人と装置が兄弟になり**、装置が増えるほど
// 人が埋もれます。
func TestEnsureContactPersonBuildsTree(t *testing.T) {
	user, companyID := setupPartnerTree(t)

	personID, err := EnsureContactPerson(user, companyID, "潮崎 光俊")
	if err != nil {
		t.Fatalf("EnsureContactPerson: %v", err)
	}
	meta, ok := page.ReadSidecar(personID)
	if !ok {
		t.Fatal("担当者ページのサイドカーが読めません")
	}
	boxMeta, ok := page.ReadSidecar(meta.ParentID)
	if !ok {
		t.Fatal("担当者の箱が読めません")
	}
	if got := cms.PageTitleByID(mustAtoiT(t, meta.ParentID)); got != ContactPersonBoxTitle {
		t.Errorf("担当者の箱を挟んでいません: 親の題=%q", got)
	}
	if boxMeta.ParentID != companyID {
		t.Errorf("担当者の箱が社名ページの下にありません: %s", boxMeta.ParentID)
	}

	// 2度目は作り直さない（同じ人が2枚にならない）。
	again, err := EnsureContactPerson(user, companyID, "潮崎 光俊")
	if err != nil {
		t.Fatal(err)
	}
	if again != personID {
		t.Errorf("担当者ページが2枚できました: %s と %s", personID, again)
	}
}

// TestPartnerOfPageWalksUpToCompany は、担当者ページから**会社へ戻れる**ことを固定します。
func TestPartnerOfPageWalksUpToCompany(t *testing.T) {
	user, companyID := setupPartnerTree(t)
	personID, err := EnsureContactPerson(user, companyID, "潮崎 光俊")
	if err != nil {
		t.Fatal(err)
	}

	gotID, gotTitle, ok := PartnerOfPage(mustAtoiT(t, personID))
	if !ok {
		t.Fatal("担当者ページから会社へ戻れません")
	}
	if want := mustAtoiT(t, companyID); gotID != want {
		t.Errorf("戻り先が違います: got %d, want %d", gotID, want)
	}
	if gotTitle != "トーアスポーツマシーン" {
		t.Errorf("会社の題が違います: %q", gotTitle)
	}

	// 社名ページ自身を渡せばそれ自身。
	if id, _, ok := PartnerOfPage(mustAtoiT(t, companyID)); !ok || id != mustAtoiT(t, companyID) {
		t.Errorf("社名ページ自身が返りません: id=%d ok=%v", id, ok)
	}
	// 取引先の外は false。
	if _, _, ok := PartnerOfPage(mustAtoiT(t, cms.TopPageID)); ok {
		t.Error("取引先の外のページで ok=true になりました")
	}
}

// TestPartnerTitleForAddressFindsPersonPage は、**担当者ページに載ったアドレスでも
// 会社名で答える**ことを固定します。
//
// ここが壊れると、整理の顧客名の推奨値が静かに空振りします（解析が読んだ生の値＝
// 誤記入りのまま出る）。2026-09-10 に実データで踏んだのと同じ壊れ方です。
func TestPartnerTitleForAddressFindsPersonPage(t *testing.T) {
	user, companyID := setupPartnerTree(t)
	personID, err := EnsureContactPerson(user, companyID, "潮崎 光俊")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddContactAddresses(personID, user.Username,
		[]string{"shiozaki@toa-sports-machine.co.jp"}); err != nil {
		t.Fatal(err)
	}

	// ① 完全一致——担当者ページに載っていても、答えるのは会社の名前。
	if title, ok := PartnerTitleForAddress(user, "shiozaki@toa-sports-machine.co.jp"); !ok ||
		title != "トーアスポーツマシーン" {
		t.Errorf("完全一致で会社名が返りません: %q ok=%v", title, ok)
	}
	// ② ドメイン一致——同じ会社の別の人からの初メールも、その会社に結びつく。
	if title, ok := PartnerTitleForAddress(user, "hirai@toa-sports-machine.co.jp"); !ok ||
		title != "トーアスポーツマシーン" {
		t.Errorf("ドメイン一致で会社名が返りません: %q ok=%v", title, ok)
	}
	// ③ 無関係なドメインは返さない。
	if title, ok := PartnerTitleForAddress(user, "someone@example.org"); ok {
		t.Errorf("無関係なアドレスに答えました: %q", title)
	}
}

// TestUnknownContactsSkipsPersonPageAddresses は、担当者ページへ入れたアドレスが
// **未登録の一覧から消える**ことを固定します。
//
// 消えないと、片付けたはずのものが翌日も並び、一覧が信用されなくなります。
func TestUnknownContactsSkipsPersonPageAddresses(t *testing.T) {
	user, companyID := setupPartnerTree(t)
	personID, err := EnsureContactPerson(user, companyID, "潮崎 光俊")
	if err != nil {
		t.Fatal(err)
	}
	const addr = "shiozaki@toa-sports-machine.co.jp"
	if _, err := AddContactAddresses(personID, user.Username, []string{addr}); err != nil {
		t.Fatal(err)
	}

	// 通信記録らしいページを1枚（差出人＝そのアドレス）。
	// **索引に載せます**——未登録の一覧は索引を引くので、本文を置くだけでは出てきません。
	const recID = "009001"
	recBody := "<h1>受信</h1><dl data-type=\"tags\">" +
		"<dt>差出人</dt><dd>潮崎 光俊 &lt;" + addr + "&gt;</dd></dl>"
	newPage(t, recID, recBody,
		page.PageMeta{ParentID: cms.TopPageID, Owner: "alice", Mode: page.DefaultMode})
	if err := cms.SyncIndex(recID, recBody); err != nil {
		t.Fatal(err)
	}

	list, err := UnknownContacts(user)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.Address == addr {
			t.Fatalf("担当者ページへ入れたアドレスが未登録に残っています: %+v", c)
		}
	}

	// 念のため、担当者ページに本当に載っていること（試験の前提の確認）。
	body, err := cms.ReadPageBody(personID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, addr) {
		t.Fatalf("担当者ページにアドレスがありません: %s", body)
	}
}

// TestUnknownContactsFollowsPageMove は、**ページを動かすと一覧が追従する**ことを
// 固定します（2026-09-13 ユーザー:「間違えたときに、アドレスに対応するページを移動したら、
// DBも追従しますか？これはアドレスに限らず重要な特性と思います」）。
//
// もとは `メールアドレス` タグを持つページがどこかに在れば登録済みと数えていました。
// すると連絡先ページを取引先の外へ動かしたとき、**未登録にも出てこないのに照合もできない**
// という行方不明の状態になります——片付いた顔をして効かない、いちばん気づきにくい形です。
func TestUnknownContactsFollowsPageMove(t *testing.T) {
	user, companyID := setupPartnerTree(t)
	personID, err := EnsureContactPerson(user, companyID, "潮崎 光俊")
	if err != nil {
		t.Fatal(err)
	}
	const addr = "shiozaki@toa-sports-machine.co.jp"
	if _, err := AddContactAddresses(personID, user.Username, []string{addr}); err != nil {
		t.Fatal(err)
	}
	// 索引に「まだページになっていないアドレス」の材料を置く（通信記録らしいページ）。
	const recID = "009002"
	recBody := "<h1>受信</h1><dl data-type=\"tags\">" +
		"<dt>差出人</dt><dd>潮崎 光俊 &lt;" + addr + "&gt;</dd></dl>"
	newPage(t, recID, recBody,
		page.PageMeta{ParentID: cms.TopPageID, Owner: "alice", Mode: page.DefaultMode})
	if err := cms.SyncIndex(recID, recBody); err != nil {
		t.Fatal(err)
	}

	inList := func() bool {
		list, err := UnknownContacts(user)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range list {
			if c.Address == addr {
				return true
			}
		}
		return false
	}

	if inList() {
		t.Fatal("取引先の下に在るのに未登録へ出ています")
	}
	// **取引先の外へ動かす**——間違えて動かしたときの形。
	if _, _, err := cms.SetPageParent(user, personID, cms.TopPageID); err != nil {
		t.Fatalf("移動できません: %v", err)
	}
	if !inList() {
		t.Error("取引先の外へ動かしたのに未登録へ戻ってきません（行方不明の状態）")
	}
	// 戻せば、また消える。
	box, err := ensureChildByTitle(user, companyID, ContactPersonBoxTitle)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cms.SetPageParent(user, personID, box); err != nil {
		t.Fatalf("戻せません: %v", err)
	}
	if inList() {
		t.Error("取引先の下へ戻したのに未登録に残っています")
	}
}

func mustAtoiT(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("ページIDが数字ではありません: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// TestEmailTagLinksToContactPage は、**登録済みのアドレスが連絡先ページへのリンクに
// なる**ことを固定します（2026-09-13 ユーザー:「表示するときにメールアドレスから
// アドレス帳のページへリンクがあると良いと思います」）。
//
// **本文は書き換えません**——値は届いたままの `名前 <アドレス>` で、描画のときだけ
// リンクを被せます。登録していない相手は素のまま出ます（壊れない）。
func TestEmailTagLinksToContactPage(t *testing.T) {
	user, companyID := setupPartnerTree(t)
	personID, err := EnsureContactPerson(user, companyID, "潮崎 光俊")
	if err != nil {
		t.Fatal(err)
	}
	const addr = "shiozaki@toa-sports-machine.co.jp"
	if _, err := AddContactAddresses(personID, user.Username, []string{addr}); err != nil {
		t.Fatal(err)
	}

	body := `<dl data-type="tags">` +
		`<dt>差出人</dt><dd>潮崎 光俊 &lt;` + addr + `&gt;</dd>` +
		`<dt>宛先</dt><dd>知らない人 &lt;nobody@example.invalid&gt;</dd></dl>`
	got := cms.RenderReferenceLinks(body)

	// ① 登録済み——アドレスだけがリンクになり、名前はそのまま残る。
	if !strings.Contains(got, `href="/`+personID+`"`) {
		t.Errorf("連絡先ページへのリンクがありません: %s", got)
	}
	if !strings.Contains(got, "潮崎 光俊 &lt;") {
		t.Errorf("表示名が消えています（見える文字を変えない約束）: %s", got)
	}
	if !strings.Contains(got, `>`+addr+`</a>`) {
		t.Errorf("押せるのがアドレスの部分になっていません: %s", got)
	}
	// ② 未登録——素のまま（壊さない・薄赤にもしない）。
	if strings.Contains(got, "nobody@example.invalid</a>") {
		t.Errorf("登録していない相手までリンクになっています: %s", got)
	}
	if !strings.Contains(got, "知らない人 &lt;nobody@example.invalid&gt;") {
		t.Errorf("未登録の値が壊れています: %s", got)
	}
}
