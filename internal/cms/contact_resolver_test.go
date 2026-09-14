package cms

import (
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────
// 連絡先の解決係フック（2026-09-15）
//
// アドレス帳は `ext/contacts` へ出ることが決まっています（§5b 案B）。移設の本体は
// ファイルを動かすことではなく**依存の向きを裏返すこと**で、コアが
// `ContactPageForAddress` を名指ししていた1本がこのフックに変わりました。
//
// 裏返したあとに残る危険は1つだけです——**解決係が居ない構成で、描画が壊れないか**。
// 素の w-cms（`-tags minimal`）はアドレス帳を積まないので、そこで `email` 型のタグが
// 例外を投げたり空になったりすると、**業務語彙を抜いた瞬間に画面が死にます**。
// ここで固定しておきます。
// ─────────────────────────────────────────────────────────────────────────

// withoutContactResolver は解決係を外した状態を作ります（後始末つき）。
//
// **`RegisterContactResolver` は使いません**——重複登録で panic する作りなので、
// 戻すときに使えないためです。試験は同じパッケージなので変数を直に触れます。
func withoutContactResolver(t *testing.T) {
	t.Helper()
	saved := contactResolver
	contactResolver = nil
	t.Cleanup(func() { contactResolver = saved })
}

// TestEmailTagWithoutResolverStaysPlain は、**解決係が居なければ素のまま出る**ことを
// 固定します（例外も空欄も出さない）。
func TestEmailTagWithoutResolverStaysPlain(t *testing.T) {
	user, companyID := setupPartnerTree(t)
	personID, err := EnsureContactPerson(user, companyID, "山田 太郎")
	if err != nil {
		t.Fatal(err)
	}
	const addr = "yamada@example-sports.co.jp"
	if _, err := AddContactAddresses(personID, user.Username, []string{addr}); err != nil {
		t.Fatal(err)
	}

	body := `<dl data-type="tags">` +
		`<dt>差出人</dt><dd>山田 太郎 &lt;` + addr + `&gt;</dd></dl>`

	// ① 解決係が居るとき——リンクになる（前提の確認）。
	if got := RenderReferenceLinks(body); !strings.Contains(got, `href="/`+personID+`"`) {
		t.Fatalf("前提が崩れています: 解決係が居てもリンクになりません: %s", got)
	}

	// ② 解決係が居ないとき——**素のまま**。
	withoutContactResolver(t)
	got := RenderReferenceLinks(body)

	if strings.Contains(got, "<a ") {
		t.Errorf("解決係が居ないのにリンクが作られています: %s", got)
	}
	if !strings.Contains(got, addr) {
		t.Errorf("アドレスが消えています（素のまま出す約束）: %s", got)
	}
	if !strings.Contains(got, "山田 太郎") {
		t.Errorf("表示名が消えています: %s", got)
	}
}

// TestUnknownContactsViewIsRegisteredNotHardcoded は、**計算ビューが登録で入っている**
// ことを固定します。
//
// `view_render.go` の表から名指しを外したので、`RegisterView` が効いていないと
// 「この形式の中身を作る処理がまだ用意されていません」という告知に変わります
// ——画面は無言の空白にはなりませんが、作業面そのものが消えます。
// 2026-09-11 に**作業面が存在しないまま100通が過ぎた**のと同じ形の事故なので、
// 登録が外れたことに気づけるようにしておきます。
func TestUnknownContactsViewIsRegisteredNotHardcoded(t *testing.T) {
	if _, ok := viewRenderers["unknown-contacts"]; !ok {
		t.Fatal("未登録の連絡先の描画が登録されていません（contacts.go の init）")
	}
	// 語彙の側も、アドレス帳が持ち込んでいること。
	if _, ok := VocabDefByType("unknown-contacts"); !ok {
		t.Error("未登録の連絡先の語彙が登録されていません（RegisterVocab）")
	}
}
