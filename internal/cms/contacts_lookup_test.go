package cms

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// partnerPage は「取引先」の下に相手ページを1枚作ります。
func partnerPage(t *testing.T, id, boxID, title, relation string, addrs ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("<h1>" + title + "</h1><dl data-type=\"tags\">")
	WriteTag(&b, RelationTag, relation)
	for _, a := range addrs {
		WriteTag(&b, EmailTag, a)
	}
	b.WriteString("</dl>")
	newPage(t, id, b.String(), page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: boxID})
}

// setupPartnerBox は取引先の箱を作って返します。
func setupPartnerBox(t *testing.T) string {
	t.Helper()
	setupTemplateAPITest(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	const boxID = "000200"
	newPage(t, boxID, "<h1>"+PartnerBoxTitle+"</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})
	return boxID
}

// TestPartnerTitleForAddressUsesDomain は、**差出人のドメインで取引先ページを引ける**
// ことを固定します（2026-09-06）。
//
// これが社名の揺れを消す鍵です——整理の推奨値が「機械が読んだ名前」ではなく
// 「既にあるページの題」になるので、打ち写しで `株式会社` の有無が生まれません。
func TestPartnerTitleForAddressUsesDomain(t *testing.T) {
	box := setupPartnerBox(t)
	partnerPage(t, "000201", box, "トーアスポーツマシーン", RelationCustomer,
		"ozawa@toa-sports-machine.co.jp")

	u := &auth.User{Username: "alice", IsAdmin: true}
	// 登録されていない**別の窓口**からのメールでも、ドメインで当たる。
	title, ok := PartnerTitleForAddress(u, "yoshihara@toa-sports-machine.co.jp")
	if !ok || title != "トーアスポーツマシーン" {
		t.Fatalf("ドメインで引けていません: %q ok=%v", title, ok)
	}
}

// TestPartnerTitleForAddressPrefersExactAddress は、**完全一致がドメインより先**
// であることを固定します。
//
// 実データに「自社の工場長だけ別プロバイダのアドレス」という例があり、ここを
// 逆にすると、そのドメインの他社と取り違えます。
func TestPartnerTitleForAddressPrefersExactAddress(t *testing.T) {
	box := setupPartnerBox(t)
	partnerPage(t, "000201", box, "A社", RelationCustomer, "someone@shared-isp.ne.jp")
	partnerPage(t, "000202", box, "B社", RelationCustomer, "sato@shared-isp.ne.jp")

	u := &auth.User{Username: "alice", IsAdmin: true}
	title, ok := PartnerTitleForAddress(u, "sato@shared-isp.ne.jp")
	if !ok || title != "B社" {
		t.Fatalf("完全一致が優先されていません: %q ok=%v", title, ok)
	}
}

// TestPartnerTitleForAddressSkipsSelf は、**`取引：自社` を顧客名の推奨値にしない**
// ことを固定します。自社が出ると、人がそのまま押してしまいます。
func TestPartnerTitleForAddressSkipsSelf(t *testing.T) {
	box := setupPartnerBox(t)
	partnerPage(t, "000201", box, "東邦金属工業所", RelationSelf, "minami@i-toho.co.jp")

	u := &auth.User{Username: "alice", IsAdmin: true}
	if title, ok := PartnerTitleForAddress(u, "plt@i-toho.co.jp"); ok {
		t.Fatalf("自社を推奨値にしています: %q", title)
	}
}

// TestPartnerTitleForAddressUnknown は、**知らない相手では引けない**ことを固定します。
// 新しい顧客の1通目はこれが正常で、呼ぶ側は読めた名前へ戻ります。
func TestPartnerTitleForAddressUnknown(t *testing.T) {
	box := setupPartnerBox(t)
	partnerPage(t, "000201", box, "A社", RelationCustomer, "someone@a.example.jp")

	u := &auth.User{Username: "alice", IsAdmin: true}
	if title, ok := PartnerTitleForAddress(u, "hello@b.example.jp"); ok {
		t.Fatalf("知らない相手を引いています: %q", title)
	}
}

// TestAddContactAddressesMergesIntoExisting は、**2つ目のドメインが既存の相手へ
// 足せる**ことを固定します（2026-09-06）。
//
// ここが無いと、同じ会社が別ドメインから送ってきたときに相手ページが2枚になり、
// ドメインの逆引きで社名の揺れを消した意味が無くなります。
func TestAddContactAddressesMergesIntoExisting(t *testing.T) {
	box := setupPartnerBox(t)
	partnerPage(t, "000201", box, "A社", RelationCustomer, "ozawa@a-main.co.jp")

	added, err := AddContactAddresses("000201", "alice",
		[]string{"sales@a-sub.jp", "ozawa@a-main.co.jp"})
	if err != nil {
		t.Fatalf("足せません: %v", err)
	}
	// 既に載っているほうは足さない（二度押しでタグが並ばない）。
	if added != 1 {
		t.Fatalf("足した件数が違います: %d", added)
	}

	u := &auth.User{Username: "alice", IsAdmin: true}
	title, ok := PartnerTitleForAddress(u, "info@a-sub.jp")
	if !ok || title != "A社" {
		t.Fatalf("足したドメインで引けません: %q ok=%v", title, ok)
	}
}
