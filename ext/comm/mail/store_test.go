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

	"w-cms/internal/auth"
	"w-cms/internal/database"
)

// TestTokenStoreFollowsDataDir は、データの場所（WCMS_DATA_DIR・2026-10-14）を変えると、サインインの保管も
// その下の mail に置かれ、作業ディレクトリの data/ には書かないことを固定します（1台に2つ同居させたとき、
// 隣のインスタンスのサインインを読み書きしない）。
func TestTokenStoreFollowsDataDir(t *testing.T) {
	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })
	other := filepath.Join(t.TempDir(), "site", "data")
	database.SetDataDir(other)
	t.Cleanup(func() { database.SetDataDir("") })
	allowMail(t, map[string]string{"a": "a@example.com"}) // 管理者が決めたアドレス（2026-10-27）

	if err := saveToken("a", storedToken{RefreshToken: "r", Address: "a@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(other, "mail", "a.json")); err != nil {
		t.Errorf("保管がデータの場所の下にありません: %v", err)
	}
	if got := signedInAddresses(); len(got) != 1 || got[0] != "a@example.com" {
		t.Errorf("データの場所の保管を読めません: %v", got)
	}
	if _, err := os.Stat("data"); !os.IsNotExist(err) {
		t.Errorf("⚠ 作業ディレクトリの data/ に書きました（%v）", err)
	}
}

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
	allowMail(t, map[string]string{"山田 太郎": "yamada@example.com", "a": "a@example.com"})

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

// allowMail は、利用者の DB を作業ディレクトリの data に用意し、利用者ごとのサインインしてよいアドレスを決めます
// （2026-10-27・auth.SetMailAddresses——管理者が決めたアドレスの鍵だけを使う）。
func allowMail(t *testing.T, addrs map[string]string) {
	t.Helper()
	if err := os.MkdirAll(database.DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := database.InitAuthDB(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.AuthDB.Close(); database.AuthDB = nil })
	for u, a := range addrs {
		if err := auth.SetMailAddresses(u, []string{a}); err != nil {
			t.Fatal(err)
		}
	}
}

// TestTokenNeedsAllowedAddress は、管理者が決めたアドレスでない鍵は、保存してあっても使わないことを固定します
// （2026-10-27 利用者:「管理者が対応を決める」——それまでは、ほかの人に番号を入れてもらうと、その人の口座を自分に結べた）。
func TestTokenNeedsAllowedAddress(t *testing.T) {
	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })
	allowMail(t, map[string]string{"a": "a@example.com"})
	if err := saveToken("a", storedToken{RefreshToken: "r", Address: "boss@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadToken("a"); ok || SignedInAddress("a") != "" {
		t.Errorf("⚠ 決めていないアドレス（boss@）の鍵を使いました")
	}
	if !auth.MailAddressAllowed("a", " A@Example.COM ") || auth.MailAddressAllowed("a", "boss@example.com") {
		t.Errorf("アドレスの照らし方が違います（大小・前後の空白をそろえる）")
	}
	if err := auth.SetMailAddresses("a", []string{"not an address"}); err == nil {
		t.Errorf("アドレスでない値を通しました")
	}
}
