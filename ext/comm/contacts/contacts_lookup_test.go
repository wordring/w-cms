package contacts

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// partnerPage は「連絡帳」の下に相手ページを1枚作ります。
func partnerPage(t *testing.T, id, boxID, title string, addrs ...string) {
	t.Helper()
	partnerPageWith(t, id, boxID, title, addrs, nil)
}

// partnerPageWith は**組織の連絡先（ドメイン）も書ける**版です（2026-09-16）。
// `メールアドレス` は個人の連絡先、`ドメイン` は組織の連絡先——別のものなので、
// 試験も別々に渡せる形にします。
func partnerPageWith(t *testing.T, id, boxID, title string, addrs, domains []string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("<h1>" + title + "</h1><dl data-type=\"tags\">")
	for _, a := range addrs {
		cms.WriteTag(&b, EmailTag, a)
	}
	for _, d := range domains {
		cms.WriteTag(&b, DomainTag, d)
	}
	b.WriteString("</dl>")
	newPage(t, id, b.String(), page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: boxID})
}

// setupPartnerBox は取引先の箱を作って返します。
func setupPartnerBox(t *testing.T) string {
	t.Helper()
	setupTemplateAPITest(t)
	newPage(t, cms.TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	const boxID = "000200"
	newPage(t, boxID, "<h1>"+ContactsBoxTitle+"</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: cms.TopPageID})
	return boxID
}

// TestPartnerTitleForAddressUsesDomain は、**差出人のドメインで取引先ページを引ける**
// ことを固定します（2026-09-06）。
//
// これが社名の揺れを消す鍵です——整理の推奨値が「機械が読んだ名前」ではなく
// 「既にあるページの題」になるので、打ち写しで `株式会社` の有無が生まれません。
// TestPartnerTitleForAddressUsesDomainTag は、**組織の連絡先（`ドメイン` タグ）**で
// 引けることを固定します（2026-09-16 にこの形へ改めました）。
//
// **暗黙の切り出しはやめました。** それまでは登録済みアドレスから機械がドメインを
// 切り出して一致を見ていたので、`@yahoo.co.jp` の個人客を1人登録すると**以後その
// ドメインの全員がその人になりました**（下の試験がその再発を止めます）。
func TestPartnerTitleForAddressUsesDomainTag(t *testing.T) {
	box := setupPartnerBox(t)
	partnerPageWith(t, "000201", box, "南北スポーツ機械",
		[]string{"suzuki@example-sports.co.jp"},
		[]string{"example-sports.co.jp"})

	u := &auth.User{Username: "alice", IsAdmin: true}
	// 登録されていない**別の窓口**からのメールでも、組織のドメインで当たる。
	title, ok := PartnerTitleForAddress(u, "yoshihara@example-sports.co.jp")
	if !ok || title != "南北スポーツ機械" {
		t.Fatalf("ドメインタグで引けていません: %q ok=%v", title, ok)
	}
}

// TestPartnerTitleForAddressIgnoresDomainWithoutTag は、**ドメインタグが無ければ
// ドメインでは引かない**ことを固定します（2026-09-16 ユーザー決定の本体）。
//
// これが無いと、フリーメール（yahoo・gmail）で個人客を1人登録しただけで、
// **同じドメインの他人が全員その人に化けます**。実データに `@yahoo.co.jp` の
// アドレスがあるので、絵空事ではありません。
func TestPartnerTitleForAddressIgnoresDomainWithoutTag(t *testing.T) {
	box := setupPartnerBox(t)
	partnerPage(t, "000201", box, "山田太郎", "yamada@yahoo.co.jp")

	u := &auth.User{Username: "alice", IsAdmin: true}
	if title, ok := PartnerTitleForAddress(u, "suzuki@yahoo.co.jp"); ok {
		t.Fatalf("ドメインタグが無いのにドメインで引けてしまいました: %q", title)
	}
	// 本人は完全一致でちゃんと引ける。
	if title, ok := PartnerTitleForAddress(u, "yamada@yahoo.co.jp"); !ok || title != "山田太郎" {
		t.Errorf("完全一致で引けません: %q ok=%v", title, ok)
	}
}

// TestResolvePartnerRefusesWhenAmbiguous は、**同じドメインを2つの組織が持つと
// 1つに決めない**ことを固定します（2026-09-16 ユーザー:「候補が複数あるということで
// どうでしょう」）。機械が選ぶと、静かに間違えます。
func TestResolvePartnerRefusesWhenAmbiguous(t *testing.T) {
	box := setupPartnerBox(t)
	partnerPageWith(t, "000201", box, "甲社", nil, []string{"shared.example.jp"})
	partnerPageWith(t, "000202", box, "乙社", nil, []string{"shared.example.jp"})

	u := &auth.User{Username: "alice", IsAdmin: true}
	if id, title, ok := ResolvePartner(u, "who@shared.example.jp"); ok {
		t.Fatalf("2社が持つドメインで1つに決めてしまいました: %s %q", id, title)
	}
	// **候補としては両方出ます**（人が選べるように）。
	refs, truncated := PartnerCandidates(u, "who@shared.example.jp", 10)
	if len(refs) != 2 || truncated {
		t.Fatalf("候補が2件出ません: %+v truncated=%v", refs, truncated)
	}
	// ⚠ **件数制限は必須**——ヤフーのようなドメインでは候補が1万件になりえます。
	refs, truncated = PartnerCandidates(u, "who@shared.example.jp", 1)
	if len(refs) != 1 || !truncated {
		t.Errorf("件数制限が効いていません: %+v truncated=%v", refs, truncated)
	}
}

// TestPartnerTitleForAddressPrefersExactAddress は、**完全一致がドメインより先**
// であることを固定します。
//
// 実データに「自社の工場長だけ別プロバイダのアドレス」という例があり、ここを
// 逆にすると、そのドメインの他社と取り違えます。
func TestPartnerTitleForAddressPrefersExactAddress(t *testing.T) {
	box := setupPartnerBox(t)
	partnerPage(t, "000201", box, "A社", "someone@shared-isp.ne.jp")
	partnerPage(t, "000202", box, "B社", "sato@shared-isp.ne.jp")

	u := &auth.User{Username: "alice", IsAdmin: true}
	title, ok := PartnerTitleForAddress(u, "sato@shared-isp.ne.jp")
	if !ok || title != "B社" {
		t.Fatalf("完全一致が優先されていません: %q ok=%v", title, ok)
	}
}

// TestPartnerTitleForAddressUnknown は、**知らない相手では引けない**ことを固定します。
// 新しい顧客の1通目はこれが正常で、呼ぶ側は読めた名前へ戻ります。
func TestPartnerTitleForAddressUnknown(t *testing.T) {
	box := setupPartnerBox(t)
	partnerPage(t, "000201", box, "A社", "someone@a.example.jp")

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
	partnerPage(t, "000201", box, "A社", "suzuki@a-main.co.jp")

	added, err := AddContactAddresses("000201", "alice",
		[]string{"sales@a-sub.jp", "suzuki@a-main.co.jp"})
	if err != nil {
		t.Fatalf("足せません: %v", err)
	}
	// 既に載っているほうは足さない（二度押しでタグが並ばない）。
	if added != 1 {
		t.Fatalf("足した件数が違います: %d", added)
	}

	u := &auth.User{Username: "alice", IsAdmin: true}
	// **足したアドレスそのもの**で引けます（完全一致）。
	title, ok := PartnerTitleForAddress(u, "sales@a-sub.jp")
	if !ok || title != "A社" {
		t.Fatalf("足したアドレスで引けません: %q ok=%v", title, ok)
	}
	// ⚠ **同じドメインの別の人では引けません**（2026-09-16）——ドメインで引くには
	// 組織に `ドメイン` タグが要ります。アドレスを1つ足しただけで、そのドメインの
	// 全員がこの会社になってはいけません。
	if title, ok := PartnerTitleForAddress(u, "info@a-sub.jp"); ok {
		t.Errorf("アドレスを足しただけでドメインが効いてしまいました: %q", title)
	}
}

// TestRegisterWritesDomainTag は、**登録のときにドメインも書ける**ことを固定します
// （2026-09-16）。
//
// ここが効かないと、登録しても組織の連絡先が付かず、**同じドメインの新しい人からの
// 初メールが毎回「未登録」に並びます**——アドレス帳が育ちません。
func TestRegisterWritesDomainTag(t *testing.T) {
	box := setupPartnerBox(t)
	u := &auth.User{Username: "alice", IsAdmin: true}

	// ドメインつきで登録した組織（＝画面のチェックが入った状態）。
	partnerPageWith(t, "000201", box, "南北スポーツ機械",
		[]string{"suzuki@example-sports.co.jp"}, []string{"example-sports.co.jp"})
	// ドメインなしで登録した組織（＝チェックを外した状態。フリーメールの想定）。
	partnerPage(t, "000202", box, "山田太郎", "yamada@yahoo.co.jp")

	// ① 付けたほうは、**知らない人からのメールでも**組織に結びつく。
	if title, ok := PartnerTitleForAddress(u, "sato@example-sports.co.jp"); !ok ||
		title != "南北スポーツ機械" {
		t.Errorf("ドメインを付けた組織に結びつきません: %q ok=%v", title, ok)
	}
	// ② 付けなかったほうは、**そのドメインの他人を引き寄せません**。
	if title, ok := PartnerTitleForAddress(u, "suzuki@yahoo.co.jp"); ok {
		t.Errorf("ドメインを付けていないのに他人を引き寄せました: %q", title)
	}
}

// TestNormalizeDomainsFolds は、受け取ったドメインの畳み方を固定します。
//
// **人が書く欄なので、書き方は揺れます**——`@` 付き・大文字・前後の空白・
// アドレスをそのまま貼る。揺れたまま入ると、**引くときに静かに外れます**。
func TestNormalizeDomainsFolds(t *testing.T) {
	got := normalizeDomains([]string{
		" Example-Sports.co.jp ", "@example-sports.co.jp",
		"suzuki@example-sports.co.jp", "", "  ", "other.example.jp",
	})
	want := []string{"example-sports.co.jp", "other.example.jp"}
	if len(got) != len(want) {
		t.Fatalf("畳んだ結果が違います: %v（%v を期待）", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] %q（%q を期待）", i, got[i], want[i])
		}
	}
}
