package mail

// トークンの保管先の名前（2026-10-02——日本語のログイン名でもサインインを保管できるように）。
//
// ⚠ ログイン名を連絡帳の人のページの題（「姓 名」）に合わせると決めたら、保管先の名前が英数字しか通さず、
// その人はメールを読めなかった。ASCII の名前はそのまま、それ以外は `~` ＋ UTF-8 の16進にする。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestTokenPathKeepsASCIINames は、⚠ **ASCII の名前の保管先が変わらない**ことを固定します
// （変えると、既にサインインしている人の保管が読めなくなり、全員がサインインし直しになる）。
func TestTokenPathKeepsASCIINames(t *testing.T) {
	if got, want := tokenPath("a"), filepath.Join("data", "mail", "a.json"); got != want {
		t.Errorf("tokenPath(a) = %q（%q のはず）", got, want)
	}
}

// TestTokenPathEncodesOtherNames は、⚠ **日本語の名前も保管先を持ち、保管先の外へ出ない**ことを固定します。
func TestTokenPathEncodesOtherNames(t *testing.T) {
	for _, name := range []string{"山田 太郎", "山田　太郎", "../evil", `..\evil`, "a/b"} {
		p := tokenPath(name)
		if p == "" {
			t.Errorf("tokenPath(%q) が空です（保管できない）", name)
			continue
		}
		if filepath.Dir(p) != filepath.Join("data", "mail") {
			t.Errorf("⚠ tokenPath(%q) = %q が保管先の外です", name, p)
		}
		base := strings.TrimSuffix(filepath.Base(p), ".json")
		if strings.Trim(base[1:], "0123456789abcdef") != "" || base[0] != '~' {
			t.Errorf("tokenPath(%q) の名前 %q が `~`＋16進ではありません", name, base)
		}
		if got := usernameOfTokenFile(base); got != name {
			t.Errorf("保管名 %q から %q が戻りました（%q のはず）", base, got, name)
		}
	}
	// 使えない名前は空（制御文字・空・長すぎ）。
	for _, name := range []string{"", "a\nb", strings.Repeat("あ", 40)} {
		if p := tokenPath(name); p != "" {
			t.Errorf("tokenPath(%q) = %q（空のはず）", name, p)
		}
	}
	// ⚠ ASCII の名前の `~` 形・16進でないものは読まない。
	for _, base := range []string{"~61", "~zz", "~"} {
		if got := usernameOfTokenFile(base); got != "" {
			t.Errorf("usernameOfTokenFile(%q) = %q（空のはず）", base, got)
		}
	}
}

// TestTokenStoreRoundTripsJapaneseName は、⚠ **日本語の名前で保管して読み出せ、サインインしている人の
// 一覧にも出る**ことを固定します（通信箱の取り込みが「自分が出したメール」を見分けるのに使う）。
func TestTokenStoreRoundTripsJapaneseName(t *testing.T) {
	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })

	if err := saveToken("山田 太郎", storedToken{RefreshToken: "r1", Address: "yamada@example.com"}); err != nil {
		t.Fatalf("日本語の名前で保管できません: %v", err)
	}
	if err := saveToken("a", storedToken{RefreshToken: "r2", Address: "a@example.com"}); err != nil {
		t.Fatal(err)
	}
	st, ok := loadToken("山田 太郎")
	if !ok || st.RefreshToken != "r1" {
		t.Fatalf("日本語の名前で読み出せません: %v %+v", ok, st)
	}
	got := signedInAddresses()
	sort.Strings(got)
	if strings.Join(got, ",") != "a@example.com,yamada@example.com" {
		t.Errorf("サインインしている人のアドレスが %v です", got)
	}
}
