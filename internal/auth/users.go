package auth

import (
	"database/sql"
	"errors"
	"log"
	"os"
	"time"

	"w-cms/internal/database"
)

// 総当たり対策のしきい値（認証認可設計.md 2.3節）。
const (
	maxFailBeforeLock = 5                // この回数連続失敗でロックアウト
	lockoutDuration   = 15 * time.Minute // ロックアウト時間
)

// User は認証済みユーザーの情報です。
type User struct {
	Username     string
	IsAdmin      bool
	PrimaryGroup string
	Groups       []string // 所属グループ（primary_group ＋ 補助グループ）
}

var (
	// ErrAuthFailed は認証失敗（ユーザー不在・パスワード不一致・無効化）を表します。
	// ユーザー列挙を防ぐため、原因を区別せず一様にこのエラーを返します。
	ErrAuthFailed = errors.New("ユーザー名またはパスワードが違います")
	// ErrLockedOut はログイン失敗が続いてロックアウト中であることを表します。
	ErrLockedOut = errors.New("ログイン試行が多すぎます。しばらくしてから再試行してください")
)

// Authenticate はユーザー名とパスワードを検証し、成功すれば User を返します。
// 失敗回数のロックアウト判定・記録も行います。
func Authenticate(username, password string) (*User, error) {
	if locked, _ := isLockedOut(username); locked {
		return nil, ErrLockedOut
	}

	var hash sql.NullString
	var isAdmin, disabled int
	var primaryGroup sql.NullString
	err := database.AuthDB.QueryRow(
		`SELECT password_hash, is_admin, primary_group, disabled FROM users WHERE username = ?`, username,
	).Scan(&hash, &isAdmin, &primaryGroup, &disabled)
	if err != nil || disabled != 0 || !hash.Valid {
		// ユーザー不在・無効化・ローカルパスワード未設定はすべて一様に失敗扱い。
		// 実在ユーザー（argon2検証を通る）との応答時間差でユーザー名を列挙されないよう、
		// ここでもダミーのargon2検証を実行して所要時間を均一化する（結果は捨てる）。
		if d := decoyHash(); d != "" {
			VerifyPassword(password, d)
		}
		recordFailure(username)
		return nil, ErrAuthFailed
	}

	ok, err := VerifyPassword(password, hash.String)
	if err != nil || !ok {
		recordFailure(username)
		return nil, ErrAuthFailed
	}

	clearFailures(username)
	return loadUser(username, isAdmin != 0, primaryGroup.String)
}

// loadUser は所属グループを解決して User を組み立てます。
func loadUser(username string, isAdmin bool, primaryGroup string) (*User, error) {
	u := &User{Username: username, IsAdmin: isAdmin, PrimaryGroup: primaryGroup}
	if primaryGroup != "" {
		u.Groups = append(u.Groups, primaryGroup)
	}
	for _, g := range GroupsFor(username) {
		if g != primaryGroup {
			u.Groups = append(u.Groups, g)
		}
	}
	return u, nil
}

// LookupUser はセッションから復元する際に使う、パスワード検証なしのユーザー取得です。
func LookupUser(username string) (*User, error) {
	var isAdmin, disabled int
	var primaryGroup sql.NullString
	err := database.AuthDB.QueryRow(
		`SELECT is_admin, primary_group, disabled FROM users WHERE username = ?`, username,
	).Scan(&isAdmin, &primaryGroup, &disabled)
	if err != nil {
		return nil, err
	}
	if disabled != 0 {
		return nil, ErrAuthFailed
	}
	return loadUser(username, isAdmin != 0, primaryGroup.String)
}

// GroupsFor はユーザーの補助グループ一覧を返します。
// v1ではローカルの group_members を読むだけ。将来は外部IdPのグループをマージできる接合点（認証認可設計.md 2.5節）。
func GroupsFor(username string) []string {
	rows, err := database.AuthDB.Query(`SELECT group_name FROM group_members WHERE username = ?`, username)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var groups []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err == nil {
			groups = append(groups, g)
		}
	}
	return groups
}

// CreateUser はユーザーを作成します（管理用）。password が空ならローカルパスワードなし。
func CreateUser(username, password string, isAdmin bool, primaryGroup string) error {
	var hash sql.NullString
	if password != "" {
		h, err := HashPassword(password)
		if err != nil {
			return err
		}
		hash = sql.NullString{String: h, Valid: true}
	}
	var pg sql.NullString
	if primaryGroup != "" {
		pg = sql.NullString{String: primaryGroup, Valid: true}
	}
	admin := 0
	if isAdmin {
		admin = 1
	}
	_, err := database.AuthDB.Exec(
		`INSERT INTO users (username, password_hash, is_admin, primary_group) VALUES (?, ?, ?, ?)`,
		username, hash, admin, pg,
	)
	return err
}

// BootstrapAdmin は users が空のとき、環境変数 WCMS_ADMIN_USER / WCMS_ADMIN_PASSWORD から
// 最初の管理者を作成します（認証認可設計.md §7、確定）。作成後は環境変数を外す運用。
func BootstrapAdmin() error {
	var count int
	if err := database.AuthDB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	user := os.Getenv("WCMS_ADMIN_USER")
	pass := os.Getenv("WCMS_ADMIN_PASSWORD")
	if user == "" || pass == "" {
		log.Println("認証: ユーザーが未登録です。WCMS_ADMIN_USER / WCMS_ADMIN_PASSWORD を設定して初期管理者を作成してください。")
		return nil
	}
	if err := CreateUser(user, pass, true, ""); err != nil {
		return err
	}
	log.Printf("認証: 初期管理者 %q を作成しました。作成後は環境変数を外してください。", user)
	return nil
}

// --- ログイン失敗のロックアウト管理 ---

func isLockedOut(username string) (bool, time.Time) {
	var failCount int
	var lastFail sql.NullTime
	err := database.AuthDB.QueryRow(
		`SELECT fail_count, last_fail FROM login_attempts WHERE username = ?`, username,
	).Scan(&failCount, &lastFail)
	if err != nil || failCount < maxFailBeforeLock || !lastFail.Valid {
		return false, time.Time{}
	}
	until := lastFail.Time.Add(lockoutDuration)
	if time.Now().Before(until) {
		return true, until
	}
	return false, time.Time{}
}

func recordFailure(username string) {
	database.AuthDB.Exec(`
		INSERT INTO login_attempts (username, fail_count, last_fail) VALUES (?, 1, ?)
		ON CONFLICT(username) DO UPDATE SET fail_count = fail_count + 1, last_fail = excluded.last_fail
	`, username, time.Now())
}

func clearFailures(username string) {
	database.AuthDB.Exec(`DELETE FROM login_attempts WHERE username = ?`, username)
}
