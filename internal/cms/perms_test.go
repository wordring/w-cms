package cms

import (
	"database/sql"
	"os"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/database"

	_ "modernc.org/sqlite"
)

// TestModePermissions は Unix風modeの実効権限判定（owner/group/other/admin）を検証します。
func TestModePermissions(t *testing.T) {
	// owner=alice, group=sales, mode="320"（owner rw, group r, other なし）
	p := PagePerms{Owner: "alice", Group: "sales", Mode: "320"}

	alice := &auth.User{Username: "alice", Groups: []string{"sales"}}       // 所有者
	bob := &auth.User{Username: "bob", Groups: []string{"sales"}}           // 同一グループ
	carol := &auth.User{Username: "carol", Groups: []string{"engineering"}} // 部外者
	root := &auth.User{Username: "root", IsAdmin: true}                     // 管理者

	cases := []struct {
		name             string
		u                *auth.User
		wantRead, wantWr bool
	}{
		{"owner", alice, true, true},
		{"group", bob, true, false},
		{"other", carol, false, false},
		{"admin", root, true, true},
	}
	for _, c := range cases {
		if got := p.CanRead(c.u); got != c.wantRead {
			t.Errorf("%s: CanRead=%v want %v", c.name, got, c.wantRead)
		}
		if got := p.CanWrite(c.u); got != c.wantWr {
			t.Errorf("%s: CanWrite=%v want %v", c.name, got, c.wantWr)
		}
	}
}

// TestOtherReadableMode は other に read を与えた mode（"302"等）の判定を検証します。
func TestOtherReadableMode(t *testing.T) {
	p := PagePerms{Owner: "admin", Group: "", Mode: "302"} // owner rw, other r
	stranger := &auth.User{Username: "x", Groups: []string{"y"}}
	if !p.CanRead(stranger) {
		t.Error("other=read のページを部外者が読めません")
	}
	if p.CanWrite(stranger) {
		t.Error("other に write が無いのに部外者が書けてしまいます")
	}
}

// TestSidecarAndPermsSync はサイドカー→SyncIndex→page_perms→GetPerms の経路を検証します。
func TestSidecarAndPermsSync(t *testing.T) {
	// data/master を一時ディレクトリに切り替える
	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	defer os.Chdir(origWd)

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	defer db.Close()
	database.DB = db
	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}

	// サイドカーを書いてから同期する
	if err := WriteSidecar("000007", PagePerms{Owner: "alice", Group: "sales", Mode: "320"}); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}
	if err := SyncIndex("000007", "<h1>権限テスト</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	// page_perms に反映され、GetPerms で取得できること
	p := GetPerms(7)
	if p.Owner != "alice" || p.Group != "sales" || p.Mode != "320" {
		t.Errorf("page_permsが期待と異なります: %+v", p)
	}

	// サイドカー往復
	got, ok := ReadSidecar("000007")
	if !ok || got.Owner != "alice" || got.Mode != "320" {
		t.Errorf("サイドカー読み戻しが不正: ok=%v %+v", ok, got)
	}
}

// TestGetPermsFailClosed はサイドカー/レコードが無いページが admin 所有・other不可で
// フェイルクローズすることを検証します。
func TestGetPermsFailClosed(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	defer db.Close()
	database.DB = db
	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}

	// page_perms にレコードが無いページ → admin所有の既定（mode "330", other なし）
	p := GetPerms(999)
	stranger := &auth.User{Username: "x"}
	if p.CanRead(stranger) || p.CanWrite(stranger) {
		t.Error("権限レコードが無いページを部外者が読み書きできてしまいます（フェイルクローズ違反）")
	}
	if !p.CanRead(&auth.User{Username: "root", IsAdmin: true}) {
		t.Error("adminが権限レコードの無いページを読めません")
	}
}
