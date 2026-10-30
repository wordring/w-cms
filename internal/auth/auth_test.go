package auth

import (
	"database/sql"
	"strings"
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
	// 時計は試験が進める（バックオフの待ち・2026-10-01）——止まった時計から始める。
	testNow = time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	nowFunc = func() time.Time { return testNow }
	t.Cleanup(func() { nowFunc = time.Now })
	return db
}

// testNow は試験の時計の今です（advanceClock で進める）。
var testNow time.Time

// advanceClock は試験の時計を d だけ進めます。
func advanceClock(d time.Duration) { testNow = testNow.Add(d) }

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

	// 誤ったパスワードを上限まで繰り返すとロックアウトされる（失敗のあいだはバックオフの待ちだけ時計を進める）
	for i := 0; i < maxFailBeforeLock; i++ {
		if _, err := Authenticate("dave", "wrong"); err != ErrAuthFailed {
			t.Fatalf("%d回目: ErrAuthFailedを期待: %v", i+1, err)
		}
		if i < maxFailBeforeLock-1 {
			advanceClock(backoffFor(i + 1))
		}
	}
	// ここでロックアウト。正しいパスワードでも弾かれる。
	if _, err := Authenticate("dave", "correct-horse"); err != ErrLockedOut {
		t.Errorf("ロックアウトを期待しましたが: %v", err)
	}
	// 15分たてば入れる。
	advanceClock(lockoutDuration)
	if _, err := Authenticate("dave", "correct-horse"); err != nil {
		t.Errorf("ロックアウトが明けても入れません: %v", err)
	}
}

// TestLoginBackoff は、続けて失敗するたびに次を試せるまでの待ちが倍になり（1・2・4秒）、待ちのあいだは正しいパスワードでも
// 断る（パスワードを確かめない・数えない）ことを固定します（2026-10-01 利用者:「バックオフも作る」）。
// 存在しない利用者名でも同じ待ちになる（待ちの有無から利用者の有無が分からない）。
func TestLoginBackoff(t *testing.T) {
	setupAuthDB(t)
	if err := CreateUser("erin", "right-pass", false, ""); err != nil {
		t.Fatalf("CreateUserエラー: %v", err)
	}
	for n, wait := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		if _, err := Authenticate("erin", "wrong"); err != ErrAuthFailed {
			t.Fatalf("%d回目の失敗: ErrAuthFailedを期待: %v", n+1, err)
		}
		// 待ちの直前は、正しいパスワードでも断る（数えない）。
		advanceClock(wait - time.Millisecond)
		if _, err := Authenticate("erin", "right-pass"); err != ErrLockedOut {
			t.Fatalf("%d回目の失敗のあと %v で断っていません: %v", n+1, wait-time.Millisecond, err)
		}
		advanceClock(time.Millisecond)
	}
	// 待ちを過ぎれば入れる（失敗の数は消える）。
	if _, err := Authenticate("erin", "right-pass"); err != nil {
		t.Errorf("待ちのあとで入れません: %v", err)
	}
	// 存在しない利用者名も同じ待ち。
	if _, err := Authenticate("nobody", "x"); err != ErrAuthFailed {
		t.Fatalf("存在しない利用者: ErrAuthFailedを期待: %v", err)
	}
	if _, err := Authenticate("nobody", "x"); err != ErrLockedOut {
		t.Errorf("存在しない利用者名で待ちがありません（利用者の有無が分かる）: %v", err)
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

// TestLoginAttemptsPruned は、失敗の記録が古くなったら消えること（打たれた名前ごとの行が増え続けない）を固定します
// （2026-10-26）。新しい失敗を記録するときに、loginAttemptsKeep より古い行を消す。
func TestLoginAttemptsPruned(t *testing.T) {
	setupAuthDB(t)
	for _, name := range []string{"ghost1", "ghost2", "ghost3"} {
		Authenticate(name, "x")
	}
	count := func() int {
		var n int
		database.AuthDB.QueryRow(`SELECT COUNT(*) FROM login_attempts`).Scan(&n)
		return n
	}
	if n := count(); n != 3 {
		t.Fatalf("失敗の記録が %d 行（3 のはず）", n)
	}
	advanceClock(loginAttemptsKeep + time.Minute)
	Authenticate("ghost4", "x")
	if n := count(); n != 1 {
		t.Errorf("古い失敗の記録が消えていません: %d 行（1 のはず）", n)
	}
}

// TestBadUsername は、作れない利用者名（前後の空白・制御文字・長すぎ）を断り、中の空白と日本語は通すことを固定します（2026-10-26）。
func TestBadUsername(t *testing.T) {
	for name, bad := range map[string]bool{
		"山田 太郎": false, "a": false, " a": true, "a ": true, "a\nb": true, "a\x00": true,
		strings.Repeat("あ", 64): false, strings.Repeat("あ", 65): true,
	} {
		if got := badUsername(name) != ""; got != bad {
			t.Errorf("%q: 断る=%v（%v のはず）", name, got, bad)
		}
	}
}

// TestLoginLockoutPerAddress は、⚠ **ログインの失敗を名前と接続元の組で数え、ほかの接続元の同じ名前は止めない**ことと、
// 1つの接続元が名前を替えて試すのを止めることを固定します（2026-10-26 利用者:「接続元ごとにも数えてください」——それまでは
// 社外から管理者の名前で5回失敗すれば、事務所の管理者も入れなかった）。
func TestLoginLockoutPerAddress(t *testing.T) {
	setupAuthDB(t)
	if err := CreateUser("boss", "right-pass", true, ""); err != nil {
		t.Fatal(err)
	}
	fail := func(name, ip string) error {
		advanceClock(30 * time.Second) // バックオフの待ちを越える
		_, err := AuthenticateFrom(name, "wrong", ip)
		return err
	}
	for i := 0; i < maxFailBeforeLock; i++ {
		fail("boss", "203.0.113.9")
	}
	advanceClock(30 * time.Second)
	if _, err := AuthenticateFrom("boss", "right-pass", "203.0.113.9"); err != ErrLockedOut {
		t.Errorf("社外の接続元が止まっていません: %v", err)
	}
	if _, err := AuthenticateFrom("boss", "right-pass", "192.168.1.20"); err != nil {
		t.Errorf("⚠ 社外からの失敗で、事務所の管理者が入れません: %v", err)
	}
	// 1つの接続元が名前を替えて試す——maxFailPerAddr 回でその接続元を止める（初めて打つ名前でも）。
	for i := 0; i < maxFailPerAddr; i++ {
		fail("ghost"+string(rune('a'+i)), "198.51.100.7")
	}
	advanceClock(30 * time.Second)
	if _, err := AuthenticateFrom("boss", "right-pass", "198.51.100.7"); err != ErrLockedOut {
		t.Errorf("名前を替えて試す接続元が止まっていません: %v", err)
	}
	// 15分たてば明ける。
	advanceClock(lockoutDuration + time.Minute)
	if _, err := AuthenticateFrom("boss", "right-pass", "198.51.100.7"); err != nil {
		t.Errorf("ロックアウトが明けません: %v", err)
	}
}

// TestLoginNameWithControlCharsCannotLockOthers は、名前に制御文字（NUL）を入れたログインが、ほかの人の「名前と接続元の組」の
// 鍵を数え上げられないことと、IPv6 は /64 でまとめて数えることを固定します（2026-10-27 の見直し）。
func TestLoginNameWithControlCharsCannotLockOthers(t *testing.T) {
	setupAuthDB(t)
	if err := CreateUser("boss", "right-pass", true, ""); err != nil {
		t.Fatal(err)
	}
	nul := string([]byte{0})
	for i := 0; i < maxFailBeforeLock+2; i++ {
		advanceClock(30 * time.Second)
		if _, err := AuthenticateFrom("boss"+nul+"192.168.1.2", "wrong", "203.0.113.9"); err == nil {
			t.Fatal("制御文字の名前で入れました")
		}
	}
	advanceClock(30 * time.Second)
	if _, err := AuthenticateFrom("boss", "right-pass", "192.168.1.2"); err != nil {
		t.Errorf("⚠ 名前に NUL を入れた失敗で、事務所の管理者が止められました: %v", err)
	}
	// IPv6 は /64 でまとめる——同じ /64 の中で住所を替えても、同じ接続元として止まる。
	for i := 0; i < maxFailPerAddr; i++ {
		advanceClock(30 * time.Second)
		AuthenticateFrom("ghost"+string(rune('a'+i)), "wrong", "2001:db8:1:2::"+string(rune('a'+i%6)))
	}
	advanceClock(30 * time.Second)
	if _, err := AuthenticateFrom("boss", "right-pass", "2001:db8:1:2::ffff"); err != ErrLockedOut {
		t.Errorf("⚠ 同じ /64 の中で住所を替えた試しが止まっていません: %v", err)
	}
	if _, err := AuthenticateFrom("boss", "right-pass", "2001:db8:1:3::1"); err != nil {
		t.Errorf("ほかの /64 まで止まりました: %v", err)
	}
}

// TestLockoutCountsAgainAfterItEnds は、ロックアウトが明けたら1回目から数え直すことを固定します（2026-10-30・【要求】権限 §1）。
// それまでは数が残り、明けたあとの1回の失敗でまた15分止まった——社外から15分に1回失敗させるだけで、接続元や管理者の名前を止め続けられた。
func TestLockoutCountsAgainAfterItEnds(t *testing.T) {
	setupAuthDB(t)
	if err := CreateUser("boss", "right-pass", true, ""); err != nil {
		t.Fatal(err)
	}
	const ip = "203.0.113.9"
	fail := func() error {
		advanceClock(30 * time.Second) // バックオフの待ちを越える
		_, err := AuthenticateFrom("boss", "wrong", ip)
		return err
	}
	for i := 0; i < maxFailBeforeLock; i++ {
		fail()
	}
	if _, err := AuthenticateFrom("boss", "right-pass", ip); err != ErrLockedOut {
		t.Fatalf("5回で止まるはず: %v", err)
	}
	// 明けたあとの1回の失敗では、待ちはバックオフの1秒だけ（15分ではない）。
	advanceClock(lockoutDuration + time.Minute)
	if err := fail(); err != ErrAuthFailed {
		t.Fatalf("明けたあとの失敗: %v", err)
	}
	advanceClock(2 * backoffBase)
	if _, err := AuthenticateFrom("boss", "right-pass", ip); err != nil {
		t.Errorf("⚠ 明けたあとの1回の失敗で、また止まっています: %v", err)
	}

	// 接続元の数も数え直す（入れても接続元の数は消えないので、ここが効く）。
	const nat = "198.51.100.7"
	for i := 0; i < maxFailPerAddr; i++ {
		advanceClock(30 * time.Second)
		AuthenticateFrom("ghost"+string(rune('a'+i)), "wrong", nat)
	}
	advanceClock(lockoutDuration + time.Minute)
	advanceClock(30 * time.Second)
	AuthenticateFrom("ghost-next", "wrong", nat)
	advanceClock(2 * backoffBase)
	if _, err := AuthenticateFrom("boss", "right-pass", nat); err != nil {
		t.Errorf("⚠ 同じ接続元のだれかの1回の間違いで、また全員が止まっています: %v", err)
	}
}

// TestLoginDoesNotHoldSlotWhileLockedOut は、止められている試行が argon2id の枠を使わないことを固定します（2026-10-30）。
func TestLoginDoesNotHoldSlotWhileLockedOut(t *testing.T) {
	setupAuthDB(t)
	if err := CreateUser("boss", "right-pass", true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := AuthenticateFrom("boss", "wrong", "203.0.113.9"); err != ErrAuthFailed {
		t.Fatal(err)
	}
	// 枠を全部ふさいでも、バックオフ中の試行は待たずに断られる。
	for i := 0; i < cap(loginSem); i++ {
		loginSem <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(loginSem); i++ {
			<-loginSem
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := AuthenticateFrom("boss", "right-pass", "203.0.113.9")
		done <- err
	}()
	select {
	case err := <-done:
		if err != ErrLockedOut {
			t.Errorf("バックオフのはず: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("⚠ 止められている試行が、枠を待っています")
	}
}

// TestExpiredSessionsAreSwept は、新しいログインのときに失効したセッションの行が消えることを固定します（2026-10-30）。
func TestExpiredSessionsAreSwept(t *testing.T) {
	setupAuthDB(t)
	if _, err := CreateSession("old"); err != nil {
		t.Fatal(err)
	}
	live, err := CreateSession("live")
	if err != nil {
		t.Fatal(err)
	}
	count := func() (n int) {
		database.AuthDB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
		return n
	}
	if count() != 2 {
		t.Fatalf("2行のはず: %d", count())
	}
	advanceClock(SessionIdleTTL - time.Minute)
	if _, ok := TouchSession(live); !ok { // live だけ操作で延びる
		t.Fatal("まだ有効のはず")
	}
	advanceClock(2 * time.Minute)
	if _, err := CreateSession("new"); err != nil {
		t.Fatal(err)
	}
	if count() != 2 { // old が消え、live と new が残る
		t.Errorf("失効した行が消えていません: %d 行", count())
	}
	if _, ok := ResolveSession(live); !ok {
		t.Error("⚠ 有効なセッションまで消えています")
	}
}
