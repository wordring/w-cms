package cms

import (
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────
// 連絡先の解決係フック（2026-09-15）
//
// アドレス帳は `ext/comm/contacts` にあり、**コアはアドレス帳を知りません**。`email` 型の
// タグを描くとき「このアドレスのページはどれか」だけを、登録された解決係へ尋ねます。
//
// ここで確かめるのは**フックそのもの**なので、本物のアドレス帳は呼びません
// （呼べません——依存が逆向きになります）。偽の解決係を差して、コア側の振る舞いだけを
// 見ます。本物との繋がりは `ext/comm/contacts` の試験が見ています。
//
// いちばん大事なのは**解決係が居ないとき**です。素の w-cms（`-tags minimal`）は
// アドレス帳を積まないので、そこで `email` 型のタグが例外を投げたり空になったりすると、
// **業務語彙を抜いた瞬間に画面が死にます**。
// ─────────────────────────────────────────────────────────────────────────

// withContactResolver は解決係を差し替えます（後始末つき）。
//
// **`RegisterContactResolver` は使いません**——重複登録で panic する作りなので、
// 戻すときに使えないためです。試験は同じパッケージなので変数を直に触れます。
func withContactResolver(t *testing.T, r ContactResolver) {
	t.Helper()
	saved := contactResolver
	contactResolver = r
	t.Cleanup(func() { contactResolver = saved })
}

const resolverTestBody = `<dl data-type="tags">` +
	`<dt>差出人</dt><dd>山田 太郎 &lt;yamada@example-sports.co.jp&gt;</dd></dl>`

// TestEmailTagWithoutResolverStaysPlain は、**解決係が居なければ素のまま出る**ことを
// 固定します（例外も空欄も出さない）。素の w-cms が壊れない条件そのものです。
func TestEmailTagWithoutResolverStaysPlain(t *testing.T) {
	withContactResolver(t, nil)

	got := RenderReferenceLinks(resolverTestBody)

	if strings.Contains(got, "<a ") {
		t.Errorf("解決係が居ないのにリンクが作られています: %s", got)
	}
	if !strings.Contains(got, "yamada@example-sports.co.jp") {
		t.Errorf("アドレスが消えています（素のまま出す約束）: %s", got)
	}
	if !strings.Contains(got, "山田 太郎") {
		t.Errorf("表示名が消えています: %s", got)
	}
}

// TestEmailTagUsesRegisteredResolver は、**登録された解決係が使われる**ことと、
// **見える文字を変えない**ことを固定します。
func TestEmailTagUsesRegisteredResolver(t *testing.T) {
	var asked string
	withContactResolver(t, func(addr string) (string, string, bool) {
		asked = addr
		return "010999", "株式会社南北スポーツ機械", true
	})

	got := RenderReferenceLinks(resolverTestBody)

	// ① 解決係には**畳んだアドレスだけ**が渡る（表示名は付かない）。
	if asked != "yamada@example-sports.co.jp" {
		t.Errorf("解決係へ渡った値が違います: %q", asked)
	}
	// ② 返ってきたページIDがリンクになる。
	if !strings.Contains(got, `href="/010999"`) {
		t.Errorf("解決係の答えがリンクになっていません: %s", got)
	}
	// ③ **押せるのはアドレスの部分だけ**で、表示名はそのまま残る。
	if !strings.Contains(got, `>yamada@example-sports.co.jp</a>`) {
		t.Errorf("押せるのがアドレスの部分になっていません: %s", got)
	}
	if !strings.Contains(got, "山田 太郎 &lt;") {
		t.Errorf("表示名が消えています（見える文字を変えない約束）: %s", got)
	}
}

// TestEmailTagResolverMissIsPlain は、解決係が「知らない」と答えたときに
// 素のまま出ることを固定します（薄赤にもしない——未登録は異常ではない）。
func TestEmailTagResolverMissIsPlain(t *testing.T) {
	withContactResolver(t, func(string) (string, string, bool) { return "", "", false })

	if got := RenderReferenceLinks(resolverTestBody); strings.Contains(got, "<a ") {
		t.Errorf("知らない相手までリンクになっています: %s", got)
	}
}
