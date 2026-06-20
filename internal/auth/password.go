// Package auth は w-cms の認証・認可（authN/authZ）を担います。
// パスワードハッシュ（argon2id）、セッション、ユーザー/グループ、ミドルウェアを提供します。
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2idのパラメータ。本番VPS(2CPU/2GB)向けの初期値（認証認可設計.md 2.1節）。
// 実機で1ハッシュ ≈ 0.3〜0.5秒になるよう time を較正すること。
const (
	argonMemory  = 64 * 1024 // 64 MiB（KiB単位）
	argonTime    = 3         // 反復回数
	argonThreads = 1         // 並列度
	argonSaltLen = 16        // 塩のバイト長
	argonKeyLen  = 32        // 導出鍵のバイト長
)

// ErrInvalidHash は保存されたハッシュ文字列が不正な場合に返されます。
var ErrInvalidHash = errors.New("パスワードハッシュの形式が不正です")

// HashPassword は平文パスワードを argon2id でハッシュ化し、PHC string形式で返します。
// 例: $argon2id$v=19$m=65536,t=3,p=1$<salt>$<hash>
func HashPassword(plain string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	b64 := base64.RawStdEncoding
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key),
	), nil
}

// VerifyPassword は平文パスワードが PHC string と一致するかを定数時間で検証します。
// 保存時のパラメータをハッシュ文字列から読み取るため、将来パラメータを変更しても
// 既存ハッシュの検証は継続できます。
func VerifyPassword(plain, phc string) (bool, error) {
	parts := strings.Split(phc, "$")
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", "salt", "hash"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, ErrInvalidHash
	}
	if version != argon2.Version {
		return false, ErrInvalidHash
	}

	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, ErrInvalidHash
	}

	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHash
	}

	got := argon2.IDKey([]byte(plain), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
