package auth

import (
	"database/sql"
	"testing"
	"time"

	"w-cms/internal/database"

	_ "modernc.org/sqlite"
)

// setupAuthDB はインメモリの auth.db を用意し、グローバル接続に差し替えます。
func setupAuthDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	if err := database.CreateAuthTables(db); err != nil {
		t.Fatalf("認証テーブル作成エラー: %v", err)
	}
	database.AuthDB = db
	t.Cleanup(func() { db.Close() })
	return db
}

func TestHashAndVerifyPassword(t *testing.T) {
	// 日本語パスワード（72バイト超）も扱えること（bcryptと異なりargon2idは上限なし）
	pw := "とても長い日本語パスワードです１２３４５６７８９０あいうえお"

	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPasswordエラー: %v", err)
	}
	if hash == pw {
		t.Fatal("ハッシュが平文と同一です")
	}

	ok, err := VerifyPassword(pw, hash)
	if err != nil || !ok {
		t.Errorf("正しいパスワードの検証に失敗: ok=%v err=%v", ok, err)
	}

	ok, _ = VerifyPassword("ちがうパスワード", hash)
	if ok {
		t.Error("誤ったパスワードが通ってしまいました")
	}

	if _, err := VerifyPassword(pw, "$argon2id$broken"); err == nil {
		t.Error("不正なハッシュ形式でエラーになりませんでした")
	}
}

func TestSessionLifecycle(t *testing.T) {
	setupAuthDB(t)

	token, err := CreateSession("alice")
	if err != nil {
		t.Fatalf("CreateSessionエラー: %v", err)
	}

	user, ok := ResolveSession(token)
	if !ok || user != "alice" {
		t.Errorf("セッション解決に失敗: user=%q ok=%v", user, ok)
	}

	// ログアウト相当
	DeleteSession(token)
	if _, ok := ResolveSession(token); ok {
		t.Error("削除後のセッションが有効のままです")
	}
}

func TestSessionExpiry(t *testing.T) {
	setupAuthDB(t)

	// 絶対期限切れ
	token, _ := CreateSession("bob")
	database.AuthDB.Exec(`UPDATE sessions SET expires_at = ?`, time.Now().Add(-time.Minute))
	if _, ok := ResolveSession(token); ok {
		t.Error("絶対期限切れのセッションが有効と判定されました")
	}

	// アイドル期限切れ
	token2, _ := CreateSession("carol")
	database.AuthDB.Exec(`UPDATE sessions SET last_seen = ? WHERE username = 'carol'`, time.Now().Add(-31*time.Minute))
	if _, ok := ResolveSession(token2); ok {
		t.Error("アイドル期限切れのセッションが有効と判定されました")
	}
}

func TestAuthenticateAndLockout(t *testing.T) {
	setupAuthDB(t)

	if err := CreateUser("dave", "correct-horse", false, "sales"); err != nil {
		t.Fatalf("CreateUserエラー: %v", err)
	}

	// 正しいパスワードで成功し、グループが解決されること
	u, err := Authenticate("dave", "correct-horse")
	if err != nil {
		t.Fatalf("正しい認証情報で失敗: %v", err)
	}
	if u.Username != "dave" || u.PrimaryGroup != "sales" {
		t.Errorf("ユーザー情報が不正: %+v", u)
	}

	// 誤ったパスワードを上限まで繰り返すとロックアウトされる
	for i := 0; i < maxFailBeforeLock; i++ {
		if _, err := Authenticate("dave", "wrong"); err != ErrAuthFailed {
			t.Fatalf("%d回目: ErrAuthFailedを期待: %v", i+1, err)
		}
	}
	// ここでロックアウト。正しいパスワードでも弾かれる。
	if _, err := Authenticate("dave", "correct-horse"); err != ErrLockedOut {
		t.Errorf("ロックアウトを期待しましたが: %v", err)
	}
}

func TestAuthenticateUnknownUser(t *testing.T) {
	setupAuthDB(t)
	// 存在しないユーザーはユーザー列挙を防ぐため一様に ErrAuthFailed
	if _, err := Authenticate("ghost", "whatever"); err != ErrAuthFailed {
		t.Errorf("ErrAuthFailedを期待: %v", err)
	}
}

func TestBootstrapAdmin(t *testing.T) {
	setupAuthDB(t)

	t.Setenv("WCMS_ADMIN_USER", "root")
	t.Setenv("WCMS_ADMIN_PASSWORD", "s3cret-pass")

	if err := BootstrapAdmin(); err != nil {
		t.Fatalf("BootstrapAdminエラー: %v", err)
	}

	u, err := Authenticate("root", "s3cret-pass")
	if err != nil {
		t.Fatalf("作成した管理者で認証に失敗: %v", err)
	}
	if !u.IsAdmin {
		t.Error("作成したユーザーが管理者になっていません")
	}

	// 既にユーザーがいる場合は何もしない（2人目を作らない）
	t.Setenv("WCMS_ADMIN_USER", "root2")
	t.Setenv("WCMS_ADMIN_PASSWORD", "another")
	if err := BootstrapAdmin(); err != nil {
		t.Fatalf("2回目のBootstrapAdminエラー: %v", err)
	}
	if _, err := Authenticate("root2", "another"); err != ErrAuthFailed {
		t.Error("ユーザーが存在するのに2人目の管理者が作成されました")
	}
}
