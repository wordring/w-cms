package auth

import (
	"database/sql"
	"errors"
	"log"
	"net/netip"
	"os"
	"time"
	"unicode"

	"w-cms/internal/database"
	"w-cms/internal/keylock"
)

// 総当たり対策のしきい値（認証認可設計.md 2.3節）。
//
// **バックオフ**（2026-10-01 利用者:「バックオフも作る」——【要求】権限 §1 の「バックオフ／ロックアウト」のうち、それまで
// ロックアウトだけだった）: 続けて失敗するたびに、次を試せるまでの待ちを倍にします（1・2・4・8秒）。待ちのあいだの試行は
// パスワードを確かめずに断り、数えません。5回続けて失敗したら、それまでどおり15分のロックアウト。
// 数えるのは**打たれた利用者名ごと**（存在しない名前も同じ）なので、待ちの有無から利用者の有無は分かりません。
const (
	maxFailBeforeLock = 5                // この回数連続失敗でロックアウト
	lockoutDuration   = 15 * time.Minute // ロックアウト時間
	backoffBase       = 1 * time.Second  // 1回目の失敗のあとの待ち（以後、失敗のたびに倍）
	// maxFailPerAddr は、1つの接続元からの失敗（どの名前でも）がこの回数でその接続元をロックアウトする数です（2026-10-26）。
	maxFailPerAddr = 20
	// maxFailPerName は、1つの名前への失敗（どの接続元からでも）がこの回数でその名前をロックアウトする数です（2026-10-26）。
	// 多くの接続元から同じ名前を試す総当たりへの備え——ふつうの打ち間違いや、1つの接続元からの嫌がらせでは届かない高さ。
	maxFailPerName = 50
)

// ⚠ **ログインの失敗は、名前と接続元の組ごとに数えます**（2026-10-26 利用者:「接続元ごとにも数えてください」）。
// それまでは名前ごとだけで、社外から管理者の名前で5回失敗すれば、事務所の管理者も15分入れなかった。いまは:
//   - 名前と接続元の組: バックオフ（1・2・4・8秒）と、5回でその組の15分のロックアウト（ほかの接続元の同じ名前は止めない）。
//   - 接続元ごと（どの名前でも）: 20回でその接続元を15分止める（名前を替えて試すのを止める）。
//   - 名前ごと（どの接続元からでも）: 50回でその名前を15分止める（多くの接続元からの総当たりへの備え）。
// 接続元は clientIP（前段のプロキシは WCMS_TRUSTED_PROXIES に書いたときだけ信じる）。接続元が分からない呼び手（ip が空）は、
// それまでどおり名前ごとに数える。

// loginAttemptsKeep は、失敗の記録を残す長さです（ロックアウトより十分に長く）。
const loginAttemptsKeep = 24 * time.Hour

// nowFunc は今の時刻です（試験が時計を進めるため）。
var nowFunc = time.Now

// backoffFor は、続けて failCount 回失敗したあと、次を試せるまでの待ちです（ロックアウトの長さを超えない）。
func backoffFor(failCount int) time.Duration {
	if failCount <= 0 {
		return 0
	}
	if failCount >= maxFailBeforeLock {
		return lockoutDuration
	}
	d := backoffBase << (failCount - 1)
	if d > lockoutDuration {
		d = lockoutDuration
	}
	return d
}

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
	return AuthenticateFrom(username, password, "")
}

// AuthenticateFrom は Authenticate と同じで、失敗を接続元 ip ごとにも数えます（ip が空なら名前ごとだけ）。
func AuthenticateFrom(username, password, ip string) (*User, error) {
	// 同じ名前の試行は1つずつ通す——確かめ（0.3〜0.5秒）のあいだに並べて送ると、待ちとロックアウトを
	// 4つずつ抜けられた（2026-10-22 の見直し）。
	// ⚠ 名前に制御文字（\x00 など）があれば、確かめも数えもせずに断る（2026-10-27 の見直し）。失敗を数える鍵は \x00 で
	// 区切るので、名前に \x00 を入れると、ほかの人の「名前と接続元の組」の鍵と同じ文字列になり、社外から管理者のログインを
	// 止められた。作れる名前（badUsername）に制御文字は無いので、正しい人が困ることは無い。
	for _, c := range username {
		if unicode.IsControl(c) {
			return nil, ErrAuthFailed
		}
	}
	defer lockLoginName(username)()
	keys := attemptKeysFor(username, attemptAddr(ip))
	if keys.lockedOut() {
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
		keys.recordFailure()
		return nil, ErrAuthFailed
	}

	ok, err := VerifyPassword(password, hash.String)
	if err != nil || !ok {
		keys.recordFailure()
		return nil, ErrAuthFailed
	}

	keys.clear()
	return loadUser(username, isAdmin != 0, primaryGroup.String)
}

// loginNameLocks は、ログインの名前ごとのロックです（使っている人がいなくなったら捨てる——でたらめの名前で増え続けない・
// internal/keylock）。
var loginNameLocks keylock.Set

// lockLoginName は名前 name のロックを取り、外す関数を返します。
func lockLoginName(name string) func() { return loginNameLocks.Lock(name) }

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

// attemptKeys は、1回のログインの失敗を数える鍵です（login_attempts の username の列に入れる。名前には制御文字が入らない
// ——badUsername——ので、\x00 で区切った鍵がほかの名前と重ならない）。
type attemptKeys struct {
	pair string // 名前と接続元の組（接続元が分からなければ名前）——バックオフと5回のロックアウト
	addr string // 接続元（空なら数えない）——20回
	name string // 名前（接続元が分からなければ空——pair が名前なので）——50回
}

// attemptAddr は、接続元ごとに数える住所です。IPv6 は前の 64 ビット（/64）でまとめる——1つの回線は /64 を丸ごと持つので、
// 住所を替えながら送れば数えられずに済んだ（2026-10-27 の見直し）。
func attemptAddr(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is6() || a.Is4In6() {
		return ip
	}
	p, err := a.Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
}

func attemptKeysFor(username, ip string) attemptKeys {
	if ip == "" {
		return attemptKeys{pair: username}
	}
	return attemptKeys{pair: username + "\x00" + ip, addr: "\x00addr\x00" + ip, name: username}
}

func (k attemptKeys) lockedOut() bool {
	if locked, _ := isLockedOut(k.pair); locked {
		return true
	}
	if k.addr != "" && overLimit(k.addr, maxFailPerAddr) {
		return true
	}
	return k.name != "" && overLimit(k.name, maxFailPerName)
}

func (k attemptKeys) recordFailure() {
	recordFailure(k.pair)
	if k.addr != "" {
		recordFailure(k.addr)
	}
	if k.name != "" {
		recordFailure(k.name)
	}
}

// clear は、入れたときに名前と接続元の組と名前の数を消します（接続元の数は残す——自分の口座で入れる人が、ほかの名前を
// 試した数を消せないように。最後の失敗から lockoutDuration で明ける）。
func (k attemptKeys) clear() {
	clearFailures(k.pair)
	if k.name != "" {
		clearFailures(k.name)
	}
}

// overLimit は、鍵 key の失敗が limit 回以上で、最後の失敗から lockoutDuration たっていないかを返します。
func overLimit(key string, limit int) bool {
	var failCount int
	var lastFail sql.NullTime
	if err := database.AuthDB.QueryRow(
		`SELECT fail_count, last_fail FROM login_attempts WHERE username = ?`, key,
	).Scan(&failCount, &lastFail); err != nil || !lastFail.Valid {
		return false
	}
	return failCount >= limit && nowFunc().Before(lastFail.Time.Add(lockoutDuration))
}

func isLockedOut(username string) (bool, time.Time) {
	var failCount int
	var lastFail sql.NullTime
	err := database.AuthDB.QueryRow(
		`SELECT fail_count, last_fail FROM login_attempts WHERE username = ?`, username,
	).Scan(&failCount, &lastFail)
	if err != nil || failCount <= 0 || !lastFail.Valid {
		return false, time.Time{}
	}
	// 5回未満はバックオフの待ち、5回からはロックアウト（backoffFor）。
	until := lastFail.Time.Add(backoffFor(failCount))
	if nowFunc().Before(until) {
		return true, until
	}
	return false, time.Time{}
}

func recordFailure(username string) {
	// 古い失敗の記録を消す（2026-10-26。それまでは消しておらず、打たれた名前ごとに1行ずつ増え続けた——存在しない名前を
	// 次々に打てば、認証の DB をいくらでも太らせられた）。待ちもロックアウトも最後の失敗から lockoutDuration で明けるので、
	// それより十分に古い行は何も止めていない。
	database.AuthDB.Exec(`DELETE FROM login_attempts WHERE last_fail < ?`, nowFunc().Add(-loginAttemptsKeep))
	database.AuthDB.Exec(`
		INSERT INTO login_attempts (username, fail_count, last_fail) VALUES (?, 1, ?)
		ON CONFLICT(username) DO UPDATE SET fail_count = fail_count + 1, last_fail = excluded.last_fail
	`, username, nowFunc())
}

func clearFailures(username string) {
	database.AuthDB.Exec(`DELETE FROM login_attempts WHERE username = ?`, username)
}
