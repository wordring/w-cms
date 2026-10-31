package auth

// 端末の登録（2026-10-31・docs/考察/【考察】端末の登録.md）。
//
// > 2026-10-31 利用者:「ログイン元についてはよくわかりません。パソコンやスマホを登録することについてどう思いますか？」
// > 2026-10-31 利用者:「勧めどおりで作る」
//
// 社外からのログインを、登録した端末（ブラウザ）だけに絞ります。パスワードも今までどおり要ります。
//   - 社内か社外かは IsFromTrustedProxy で見ます（監査の接続元と同じ判定）。
//   - 登録は社内から。社外で足すときは、登録済みの端末か社内で出した一回限りの番号を使います。
//   - 端末の印は、セッションとは別の Cookie です。DB にはハッシュだけを置きます。
//   - 断るのは環境変数 WCMS_DEVICE_REQUIRED=1 のときだけです。無ければ、登録のない端末のログインを監査に残して通します
//     （全部の端末を登録してから切り替えるため・§5）。
//   - ⚠ Cookie は開いた名前ごとに別です。数字の住所で登録しても社外の名前では効かないので、数字の住所では登録を断ります。

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"w-cms/internal/database"
)

const (
	deviceCookieName = "wcms_device"
	// deviceCookieTTL はブラウザに置く印の寿命です（Chrome の上限の400日）。使うと出し直します。
	deviceCookieTTL = 400 * 24 * time.Hour
	// deviceUnusedTTL は、これだけ使わなかった端末を取り消す長さです（問い5）。
	deviceUnusedTTL = 180 * 24 * time.Hour
	// deviceTouchEvery は「最後に使った時刻」を書き直す間隔です。
	deviceTouchEvery = 24 * time.Hour

	deviceCodeTTL      = 10 * time.Minute
	deviceCodeDigits   = 8
	deviceCodeMaxFails = 5 // 間違えるとその番号を捨てる回数
	deviceNameMaxRunes = 60
)

// OnDeviceRegistered は、新しい端末を登録したときに呼ばれます（本人への通知・internal/cms/device_notice.go）。
var OnDeviceRegistered func(username, name, ip string)

// DeviceRequired は、社外からの登録のない端末を断るかを返します（WCMS_DEVICE_REQUIRED=1）。
func DeviceRequired() bool { return os.Getenv("WCMS_DEVICE_REQUIRED") == "1" }

// Device は登録した端末1つです。
type Device struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	LastIP    string    `json:"last_ip"`
}

// deviceCookie は端末の印の Cookie の型紙です（発行も破棄も同じ属性で書く）。
func deviceCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     deviceCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}

func setDeviceCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, deviceCookie(token, int(deviceCookieTTL.Seconds())))
}

func clearDeviceCookie(w http.ResponseWriter) {
	c := deviceCookie("", -1)
	c.Expires = time.Unix(0, 0)
	http.SetCookie(w, c)
}

// hostIsAddress は、数字の住所で開いているかを返します（Cookie が社外の名前へ届かない）。
func hostIsAddress(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return net.ParseIP(strings.Trim(host, "[]")) != nil
}

// cleanDeviceName は端末の名前を整えます（制御文字を落とし、長さを抑え、空なら日付の名前）。
func cleanDeviceName(name string) string {
	name = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "名前のない端末 " + nowFunc().Format("2006-01-02")
	}
	if rs := []rune(name); len(rs) > deviceNameMaxRunes {
		name = string(rs[:deviceNameMaxRunes])
	}
	return name
}

// registerDevice は端末を登録し、ID と印を返します（呼び手が Cookie を置く）。
func registerDevice(username, name, ip string) (int64, string, error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return 0, "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := nowFunc()
	res, err := database.AuthDB.Exec(
		`INSERT INTO devices (username, token_hash, name, created_at, last_seen, last_ip) VALUES (?, ?, ?, ?, ?, ?)`,
		username, hashToken(token), name, now, now, ip)
	if err != nil {
		return 0, "", err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, "", err
	}
	Audit(username, "device.register", fmt.Sprintf("%d %s %s", id, name, ip))
	if OnDeviceRegistered != nil {
		OnDeviceRegistered(username, name, ip)
	}
	return id, token, nil
}

// deviceByToken は印の端末を返します（無い・長く使っていないなら nil）。
func deviceByToken(token string) *Device {
	if token == "" {
		return nil
	}
	var d Device
	err := database.AuthDB.QueryRow(
		`SELECT id, username, name, created_at, last_seen, last_ip FROM devices WHERE token_hash = ?`, hashToken(token),
	).Scan(&d.ID, &d.Username, &d.Name, &d.CreatedAt, &d.LastSeen, &d.LastIP)
	if err != nil || nowFunc().Sub(d.LastSeen) > deviceUnusedTTL {
		return nil
	}
	return &d
}

// deviceOfRequest は、要求の Cookie の端末を返します（無ければ nil）。
func deviceOfRequest(r *http.Request) *Device {
	c, err := r.Cookie(deviceCookieName)
	if err != nil {
		return nil
	}
	return deviceByToken(c.Value)
}

// sessionOnThisDevice は、セッションが登録した端末に結ばれていて、その端末の印が要求に付いているかを返します。
// セッションの Cookie だけを持ち出しても、社外からは通りません。
func sessionOnThisDevice(sessionToken string, r *http.Request) bool {
	c, err := r.Cookie(deviceCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	var hash string
	var lastSeen time.Time
	err = database.AuthDB.QueryRow(
		`SELECT d.token_hash, d.last_seen FROM sessions s JOIN devices d ON d.id = s.device_id AND d.username = s.username
		 WHERE s.token_hash = ?`, hashToken(sessionToken),
	).Scan(&hash, &lastSeen)
	if err != nil {
		return false
	}
	return hash == hashToken(c.Value) && nowFunc().Sub(lastSeen) <= deviceUnusedTTL
}

// deviceGateOK は、その要求のセッションを社外で通してよいかを返します（断る設定のときの毎回の確かめ）。
func deviceGateOK(sessionToken string, r *http.Request) bool {
	if !DeviceRequired() || !IsFromTrustedProxy(r) {
		return true
	}
	return sessionOnThisDevice(sessionToken, r)
}

// deviceTrusted は、端末の管理（番号を出す・取り消す）を許してよい要求かを返します。
// 社内か、社外なら登録した端末からだけです。登録のない端末では、断らない設定のあいだも許しません
// （パスワードだけを盗んだ人が番号を出して端末を足せないように）。
func deviceTrusted(r *http.Request) bool {
	if !IsFromTrustedProxy(r) {
		return true
	}
	return sessionOnThisDevice(SessionTokenOf(r), r)
}

// touchDevice は、端末の「最後に使った時刻」を書き直し、印の Cookie を出し直します（1日に1回まで）。
func touchDevice(w http.ResponseWriter, r *http.Request, username string) {
	c, err := r.Cookie(deviceCookieName)
	if err != nil {
		return
	}
	d := deviceByToken(c.Value)
	if d == nil || d.Username != username || nowFunc().Sub(d.LastSeen) < deviceTouchEvery {
		return
	}
	if _, err := database.AuthDB.Exec(`UPDATE devices SET last_seen = ?, last_ip = ? WHERE id = ?`,
		nowFunc(), clientIP(r), d.ID); err == nil {
		setDeviceCookie(w, c.Value)
	}
}

// bindSessionToDevice は、いまのセッションを端末に結びます。
func bindSessionToDevice(sessionToken string, deviceID int64) error {
	_, err := database.AuthDB.Exec(`UPDATE sessions SET device_id = ? WHERE token_hash = ?`, deviceID, hashToken(sessionToken))
	return err
}

// revokeDevice は端末を取り消し、その端末のセッションも消します（なくした端末のログインをすぐに切る）。
func revokeDevice(id int64) error {
	if _, err := database.AuthDB.Exec(`DELETE FROM sessions WHERE device_id = ?`, id); err != nil {
		return err
	}
	_, err := database.AuthDB.Exec(`DELETE FROM devices WHERE id = ?`, id)
	return err
}

// sweepUnusedDevices は、長く使っていない端末を取り消します（ログインと一覧のときに呼ぶ）。
func sweepUnusedDevices() {
	rows, err := database.AuthDB.Query(`SELECT id, username, name FROM devices WHERE last_seen < ?`, nowFunc().Add(-deviceUnusedTTL))
	if err != nil {
		return
	}
	type old struct {
		id         int64
		user, name string
	}
	var olds []old
	for rows.Next() {
		var o old
		if rows.Scan(&o.id, &o.user, &o.name) == nil {
			olds = append(olds, o)
		}
	}
	rows.Close()
	for _, o := range olds {
		if revokeDevice(o.id) == nil {
			Audit(o.user, "device.expired", fmt.Sprintf("%d %s", o.id, o.name))
		}
	}
}

// listDevices は端末の一覧です（username が空なら全員）。
func listDevices(username string) ([]Device, error) {
	q := `SELECT id, username, name, created_at, last_seen, last_ip FROM devices`
	var args []interface{}
	if username != "" {
		q += ` WHERE username = ?`
		args = append(args, username)
	}
	rows, err := database.AuthDB.Query(q+` ORDER BY username, last_seen DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Username, &d.Name, &d.CreatedAt, &d.LastSeen, &d.LastIP); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// issueDeviceCode は一回限りの番号を出します（1人に1つ・出し直すと前の番号は効かない）。
func issueDeviceCode(username string) (string, error) {
	max := big.NewInt(1)
	for i := 0; i < deviceCodeDigits; i++ {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%0*d", deviceCodeDigits, n)
	_, err = database.AuthDB.Exec(
		`INSERT INTO device_codes (username, code_hash, expires_at, fails) VALUES (?, ?, ?, 0)
		 ON CONFLICT(username) DO UPDATE SET code_hash = excluded.code_hash, expires_at = excluded.expires_at, fails = 0`,
		username, deviceCodeHash(username, code), nowFunc().Add(deviceCodeTTL))
	if err != nil {
		return "", err
	}
	return code, nil
}

func deviceCodeHash(username, code string) string {
	sum := sha256.Sum256([]byte(username + "\x00" + code))
	return hex.EncodeToString(sum[:])
}

// normalizeDeviceCode は、打った番号から数字だけを取り出します（空白・ハイフン・全角の数字を許す）。
func normalizeDeviceCode(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			b.WriteRune(c)
		case c >= '０' && c <= '９':
			b.WriteRune('0' + (c - '０'))
		}
	}
	return b.String()
}

// checkDeviceCode は番号を確かめます（消しません）。間違いを数え、続けて間違えると番号を捨てます。
// 総当たりは、番号の桁数とこの回数で止まります。
func checkDeviceCode(username, code string) bool {
	var hash string
	var expires time.Time
	err := database.AuthDB.QueryRow(`SELECT code_hash, expires_at FROM device_codes WHERE username = ?`, username).Scan(&hash, &expires)
	if err != nil {
		return false
	}
	if nowFunc().After(expires) {
		database.AuthDB.Exec(`DELETE FROM device_codes WHERE username = ?`, username)
		return false
	}
	if code != "" && hash == deviceCodeHash(username, code) {
		return true
	}
	database.AuthDB.Exec(`UPDATE device_codes SET fails = fails + 1 WHERE username = ?`, username)
	database.AuthDB.Exec(`DELETE FROM device_codes WHERE username = ? AND fails >= ?`, username, deviceCodeMaxFails)
	Audit(username, "device.code-fail", "")
	return false
}

// consumeDeviceCode は番号を使い切ります（同時に2つ登録できないよう、消せたときだけ true）。
func consumeDeviceCode(username, code string) bool {
	res, err := database.AuthDB.Exec(`DELETE FROM device_codes WHERE username = ? AND code_hash = ? AND expires_at > ?`,
		username, deviceCodeHash(username, code), nowFunc())
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// refusedAudit は、断ったログインを監査に残す回数を接続元ごとに抑えます（外から監査の行を増やされないように）。
var refusedAudit = struct {
	sync.Mutex
	seen map[string]time.Time
}{seen: map[string]time.Time{}}

func auditDeviceRefused(username, ip string) {
	now := nowFunc()
	refusedAudit.Lock()
	if len(refusedAudit.seen) > 10000 {
		refusedAudit.seen = map[string]time.Time{}
	}
	last, ok := refusedAudit.seen[ip]
	if ok && now.Sub(last) < time.Hour {
		refusedAudit.Unlock()
		return
	}
	refusedAudit.seen[ip] = now
	refusedAudit.Unlock()
	Audit(username, "device.refused", ip)
}
