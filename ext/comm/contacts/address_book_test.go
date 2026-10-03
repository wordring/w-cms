package contacts

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// TestAddressBookListsContacts は送る欄の宛先の候補（2026-10-03・address_book.go）を固定します——連絡帳の組織のアドレスは
// 会社名だけ、その下の人は「名前（会社）」、`名前 <アドレス>` の形も素のアドレスに、連絡帳の外のページのアドレスは出さない。
// 利用者:「宛先は連絡帳から候補を取得してコンボボックスで出して欲しい」。
func TestAddressBookListsContacts(t *testing.T) {
	box := setupPartnerBox(t)
	partnerPage(t, "000201", box, "みなと商店", "info@minato.example")
	newPage(t, "000202", `<h1>山田</h1><dl data-type="tags"><dt>`+EmailTag+`</dt><dd>山田 &lt;Yamada@Minato.example&gt;</dd></dl>`,
		page.PageMeta{Owner: "alice", Mode: page.DefaultMode, ParentID: "000201"})
	newPage(t, "000203", `<h1>通信記録</h1><dl data-type="tags"><dt>`+EmailTag+`</dt><dd>stranger@other.example</dd></dl>`,
		page.PageMeta{Owner: "alice", Mode: page.DefaultMode, ParentID: cms.TopPageID})

	var got []string
	for _, e := range AddressBook(&auth.User{Username: "alice", IsAdmin: true}) {
		got = append(got, e.Org+"/"+e.Name+"/"+e.Address)
	}
	if strings.Join(got, ",") != "みなと商店//info@minato.example,みなと商店/山田/yamada@minato.example" {
		t.Errorf("宛先の候補が %v です", got)
	}
}
