package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"time"

	"w-cms/internal/database"
)

// セッションの有効期限（【要求】権限.md §1・2026-10-14 に要求へ合わせた）。
//
// **「最後の操作から8時間」で失効します**（アイドル基準へ一本化——利用者:「操作が8時間無ければ
// ログアウトにしては？ログアウト数分前に告知するのも良いと思います。」）。それまでの
// 「ログインから8時間の絶対期限」と「30分のアイドル」はやめました。操作しているかぎり延び、
// ログインからの時間では切れません。
const (
	// SessionIdleTTL は、最後の操作からこれだけ経つと失効する時間です。
	SessionIdleTTL = 8 * time.Hour

	// SessionWarnBefore は、失効の何分前に画面で告知するかです（要求は「数分前」・値は未決なので5分）。
	SessionWarnBefore = 5 * time.Minute

	// sessionTouchEvery は「最後の操作」を書き直す間隔です。操作のたびに auth.db へ書くと
	// 書き込みがぶつかりやすい（database/auth.go の SQLITE_BUSY の件）ので、前の記録から
	// 1分以上経った操作のときだけ書き直します。失効は最大でこの分だけ早まります。
	// Cookie もこのときに出し直すので、Cookie の寿命とサーバーの期限はいつも揃います。
	sessionTouchEvery = time.Minute

	sessionTokenBytes = 32 // 256bit
)

// hashToken はセッショントークンをSHA-256でハッシュ化して16進文字列にします。
// 平文トークンはCookieにのみ存在し、DBにはハッシュだけを保存します。
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateSession は新しいセッションを発行し、平文トークンを返します。
// ログインは操作なので、最後の操作の時刻はログインの時刻です。
func CreateSession(username string) (string, error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	now := nowFunc()
	_, err := database.AuthDB.Exec(
		`INSERT INTO sessions (token_hash, username, created_at, expires_at, last_seen) VALUES (?, ?, ?, ?, ?)`,
		hashToken(token), username, now, now.Add(SessionIdleTTL), now,
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

// ResolveSession はトークンから有効なセッションのユーザー名を返します。
// **寿命は延ばしません**（延ばすのは操作のときだけ——resolveSession の touch）。
// 無効・期限切れの場合は ok=false を返し、期限切れセッションは削除します。
func ResolveSession(token string) (username string, ok bool) {
	username, _, _, ok = resolveSession(token, false)
	return username, ok
}

// SessionRemaining は、そのセッションが失効するまでの残り時間を返します（寿命は延ばしません）。
// 画面の告知（GET /api/session）が使います。
func SessionRemaining(token string) (time.Duration, bool) {
	_, remaining, _, ok := resolveSession(token, false)
	return remaining, ok
}

// resolveSession はセッションを確かめ、touch なら「最後の操作」を今にします（前の記録から
// sessionTouchEvery 以上経っているときだけ書く）。
//
// 失効は last_seen（最後の操作）だけで決めます。expires_at は「last_seen＋8時間」の写しとして
// 書き直しますが、判定には使いません——2026-10-14 より前のセッションはログインからの絶対期限を
// 持っているので、それに引きずられないためです。
//
// renewed は、このとき last_seen を書き直したか（呼び手が Cookie を出し直す合図）です。
func resolveSession(token string, touch bool) (username string, remaining time.Duration, renewed, ok bool) {
	if token == "" {
		return "", 0, false, false
	}
	th := hashToken(token)

	var lastSeen time.Time
	err := database.AuthDB.QueryRow(
		`SELECT username, last_seen FROM sessions WHERE token_hash = ?`, th,
	).Scan(&username, &lastSeen)
	if err != nil {
		// **拒否は変えません**（認証はフェイルクローズが正しい）。ただし
		// 「そんなセッションは無い」と「DBが読めなかった」を**ログでは区別**します
		// ——後者は利用者から見ると**理由の分からない突然のログアウト**で、
		// 黙って起きると原因に辿り着けません（2026-09-03。auth.db に
		// busy_timeout が無く SQLITE_BUSY がここへ落ちていた実例がある）。
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("セッションの照会に失敗しました（認証は拒否・DBの状態を確認してください）: %v", err)
		}
		return "", 0, false, false
	}

	now := nowFunc()
	if now.Sub(lastSeen) > SessionIdleTTL {
		// 期限切れ：掃除して拒否
		database.AuthDB.Exec(`DELETE FROM sessions WHERE token_hash = ?`, th)
		return "", 0, false, false
	}

	if touch && now.Sub(lastSeen) >= sessionTouchEvery {
		if _, err := database.AuthDB.Exec(`UPDATE sessions SET last_seen = ?, expires_at = ? WHERE token_hash = ?`,
			now, now.Add(SessionIdleTTL), th); err == nil {
			lastSeen = now
			renewed = true
		}
	}
	return username, lastSeen.Add(SessionIdleTTL).Sub(now), renewed, true
}

// DeleteSession はトークンに対応するセッションを削除します（ログアウト）。
func DeleteSession(token string) {
	if token == "" {
		return
	}
	database.AuthDB.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
}

// BackgroundHeader は、画面が裏で自動に送る要求（知らせを受けての読み直し・メールの読み込みの様子 など）に
// 付ける印です。この印の付いた要求は**セッションの寿命を延ばしません**——利用者が何もしていないのに
// 画面が開いているだけで延び続けると、「最後の操作から8時間」が成り立たないためです。
// 印は寿命を延ばさない向きにしか効かないので、偽っても得はありません。
const BackgroundHeader = "X-Wcms-Background"

// SessionAPIPath は残り時間を知る口です（GET は寿命を延ばさない・POST は「続ける」＝操作）。
const SessionAPIPath = "/api/session"

// passivePaths は、印が無くても寿命を延ばさない口です——画面が裏で自動につなぐもので、
// EventSource のように印（ヘッダ）を付けられないものも入ります。
var passivePaths = map[string]bool{
	"/api/page-events": true, // ページの更新の知らせの購読（タブを開いているあいだずっと）
	"/api/lock-events": true, // 編集ロックの知らせの購読
	"/api/presence":    true, // キャレットのあるブロックの知らせ（キャレットが動くと自動に飛ぶ）
}

// isOperation は、その要求が「利用者の操作」として寿命を延ばすかを返します。
// ページを開く・保存・業務のボタン など、利用者の要求に当たるものは延ばします。
// 裏で自動に飛ぶもの（passivePaths・BackgroundHeader の付いた要求・残り時間を見る GET /api/session）は延ばしません。
func isOperation(r *http.Request) bool {
	if r.Header.Get(BackgroundHeader) == "1" {
		return false
	}
	if r.URL.Path == SessionAPIPath {
		return r.Method == http.MethodPost
	}
	return !passivePaths[r.URL.Path]
}
