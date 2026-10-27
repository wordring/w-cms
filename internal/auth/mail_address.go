package auth

// 利用者ごとの、サインインしてよいメールのアドレス（2026-10-27）。
//
// メールのサインイン（拡張の ext/comm/mail・デバイスコード）は、Microsoft の画面で番号を入れた口座を、押した w-cms の利用者に
// 結びます。だれの口座でも結べたので、社員がほかの人（代表の口座の持ち主など）に番号を入れてもらうと、その人の受信箱を読み、
// その人として送れました（デバイスコードのなりすまし）。
//
// 利用者の答え（2026-10-27）:「管理者が対応を決める」——管理者が利用者ごとに、サインインしてよいアドレスを決めます。
// 決めたアドレスでなければ、サインインの鍵を保存しません。前に保存した鍵も、決めたアドレスでなければ使いません。
// アドレスを1つも決めていない利用者は、メールにサインインできません。
//
// 置き場は利用者の DB（auth.db）の表 user_mail_addresses（1行が1つのアドレス・小文字にそろえる）。

import (
	"errors"
	"net/mail"
	"strings"

	"w-cms/internal/database"
)

// normMailAddress はアドレスを比べる形（前後の空白を外し、小文字）にします。
func normMailAddress(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// MailAddressesOf は、利用者がサインインしてよいアドレスを返します（決めていなければ空）。
func MailAddressesOf(username string) []string {
	if database.AuthDB == nil {
		return nil
	}
	rows, err := database.AuthDB.Query(`SELECT address FROM user_mail_addresses WHERE username = ? ORDER BY address`, username)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if rows.Scan(&a) == nil {
			out = append(out, a)
		}
	}
	return out
}

// MailAddressAllowed は、利用者 username がアドレス addr でサインインしてよいかです。
func MailAddressAllowed(username, addr string) bool {
	a := normMailAddress(addr)
	if a == "" || database.AuthDB == nil {
		return false
	}
	var n int
	if err := database.AuthDB.QueryRow(`SELECT COUNT(*) FROM user_mail_addresses WHERE username = ? AND address = ?`, username, a).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// SetMailAddresses は、利用者がサインインしてよいアドレスを addrs に置き換えます（空なら全部外す）。
func SetMailAddresses(username string, addrs []string) error {
	var clean []string
	seen := map[string]bool{}
	for _, a := range addrs {
		a = normMailAddress(a)
		if a == "" || seen[a] {
			continue
		}
		if p, err := mail.ParseAddress(a); err != nil || p.Address != a {
			return errors.New("アドレスとして読めません: " + a)
		}
		seen[a] = true
		clean = append(clean, a)
	}
	tx, err := database.AuthDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM user_mail_addresses WHERE username = ?`, username); err != nil {
		return err
	}
	for _, a := range clean {
		if _, err := tx.Exec(`INSERT INTO user_mail_addresses (username, address) VALUES (?, ?)`, username, a); err != nil {
			return err
		}
	}
	return tx.Commit()
}
