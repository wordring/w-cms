package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"w-cms/internal/database"
)

// セッションの有効期限（認証認可設計.md 2.2節、確定値）。
const (
	sessionAbsoluteTTL = 8 * time.Hour    // 絶対期限
	sessionIdleTTL     = 30 * time.Minute // アイドル期限
	sessionTokenBytes  = 32               // 256bit
)

// hashToken はセッショントークンをSHA-256でハッシュ化して16進文字列にします。
// 平文トークンはCookieにのみ存在し、DBにはハッシュだけを保存します。
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateSession は新しいセッションを発行し、平文トークンを返します。
func CreateSession(username string) (string, error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	now := time.Now()
	_, err := database.AuthDB.Exec(
		`INSERT INTO sessions (token_hash, username, created_at, expires_at, last_seen) VALUES (?, ?, ?, ?, ?)`,
		hashToken(token), username, now, now.Add(sessionAbsoluteTTL), now,
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

// ResolveSession はトークンから有効なセッションのユーザー名を返します。
// 絶対期限・アイドル期限を検証し、有効なら last_seen を更新します。
// 無効・期限切れの場合は ok=false を返し、期限切れセッションは削除します。
func ResolveSession(token string) (username string, ok bool) {
	if token == "" {
		return "", false
	}
	th := hashToken(token)

	var expiresAt, lastSeen time.Time
	err := database.AuthDB.QueryRow(
		`SELECT username, expires_at, last_seen FROM sessions WHERE token_hash = ?`, th,
	).Scan(&username, &expiresAt, &lastSeen)
	if err != nil {
		return "", false
	}

	now := time.Now()
	if now.After(expiresAt) || now.Sub(lastSeen) > sessionIdleTTL {
		// 期限切れ：掃除して拒否
		database.AuthDB.Exec(`DELETE FROM sessions WHERE token_hash = ?`, th)
		return "", false
	}

	// アイドルタイマーを更新
	database.AuthDB.Exec(`UPDATE sessions SET last_seen = ? WHERE token_hash = ?`, now, th)
	return username, true
}

// DeleteSession はトークンに対応するセッションを削除します（ログアウト）。
func DeleteSession(token string) {
	if token == "" {
		return
	}
	database.AuthDB.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
}
