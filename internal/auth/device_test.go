package auth

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"w-cms/internal/database"
)

// 端末の登録の試験（device.go・docs/考察/【考察】端末の登録.md）。

const (
	devOutsideAddr = "10.8.0.1:40000"   // 前段のプロキシ（社外の要求はここから来る）
	devInsideAddr  = "192.168.1.5:40000" // 社内の端末（直に来る）
	devClientIP    = "203.0.113.7"
	devPublicHost  = "example.test"
)

func setupDeviceTest(t *testing.T, required bool) {
	t.Helper()
	setupAuthDB(t)
	t.Setenv("WCMS_TRUSTED_PROXIES", "10.8.0.1/32")
	if required {
		t.Setenv("WCMS_DEVICE_REQUIRED", "1")
	} else {
		t.Setenv("WCMS_DEVICE_REQUIRED", "")
	}
	refusedAudit.seen = map[string]time.Time{}
	if err := CreateUser("taro", "pw-taro", false, ""); err != nil {
		t.Fatal(err)
	}
}

// devRequest は社内か社外から来た要求を作ります。
func devRequest(method, path, body string, outside bool) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = devPublicHost
	if outside {
		r.RemoteAddr = devOutsideAddr
		r.Header.Set("X-Forwarded-For", devClientIP)
	} else {
		r.RemoteAddr = devInsideAddr
	}
	return r
}

type devCookies struct{ session, device string }

func (c devCookies) add(r *http.Request) {
	if c.session != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: c.session})
	}
	if c.device != "" {
		r.AddCookie(&http.Cookie{Name: deviceCookieName, Value: c.device})
	}
}

func cookieValue(rr *httptest.ResponseRecorder, name string) string {
	for _, c := range rr.Result().Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// devLogin は POST /api/login を流します。
func devLogin(user, pw string, outside bool, c devCookies, extra url.Values) *httptest.ResponseRecorder {
	v := url.Values{"username": {user}, "password": {pw}}
	for k, vs := range extra {
		v[k] = vs
	}
	r := devRequest(http.MethodPost, "/api/login", v.Encode(), outside)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.add(r)
	rr := httptest.NewRecorder()
	LoginAPIHandler(rr, r)
	return rr
}

// devAPI は、ログインの確かめを通して API を流します。
func devAPI(h http.HandlerFunc, method, path, body string, outside bool, c devCookies) *httptest.ResponseRecorder {
	r := devRequest(method, path, body, outside)
	c.add(r)
	rr := httptest.NewRecorder()
	RequireAuth(h).ServeHTTP(rr, r)
	return rr
}

func devOK(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }

func auditCount(t *testing.T, action string) int {
	t.Helper()
	var n int
	if err := database.AuthDB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// registerInside は社内でログインして端末を登録し、両方の Cookie を返します。
func registerInside(t *testing.T, user string) devCookies {
	t.Helper()
	rr := devLogin(user, "pw-"+user, false, devCookies{}, nil)
	session := cookieValue(rr, sessionCookieName)
	if session == "" {
		t.Fatalf("社内のログインに失敗: %v", rr.Header())
	}
	rr = devAPI(DeviceRegisterAPIHandler, http.MethodPost, "/api/devices/register", `{"name":"事務所"}`, false, devCookies{session: session})
	if rr.Code != http.StatusOK {
		t.Fatalf("登録に失敗 %d: %s", rr.Code, rr.Body)
	}
	device := cookieValue(rr, deviceCookieName)
	if device == "" {
		t.Fatal("端末の印が出ていません")
	}
	return devCookies{session: session, device: device}
}

func issueCode(t *testing.T, c devCookies) string {
	t.Helper()
	rr := devAPI(DeviceCodeAPIHandler, http.MethodPost, "/api/devices/code", "", false, c)
	if rr.Code != http.StatusOK {
		t.Fatalf("番号を出せません %d: %s", rr.Code, rr.Body)
	}
	var got struct {
		Code string `json:"code"`
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Code) != deviceCodeDigits {
		t.Fatalf("番号の形が違います: %q", got.Code)
	}
	return got.Code
}

func TestOutsideUnregisteredRefusedBeforePassword(t *testing.T) {
	setupDeviceTest(t, true)
	for _, pw := range []string{"pw-taro", "wrong"} {
		rr := devLogin("taro", pw, true, devCookies{}, nil)
		if loc := rr.Header().Get("Location"); !strings.Contains(loc, "error=device") {
			t.Errorf("パスワード %q: 断っていません: %s", pw, loc)
		}
		if cookieValue(rr, sessionCookieName) != "" {
			t.Errorf("パスワード %q: セッションが出ました", pw)
		}
	}
	// パスワードを確かめていないので、失敗にも数えない。
	if n := auditCount(t, "login.fail"); n != 0 {
		t.Errorf("断ったログインが失敗に数えられています: %d", n)
	}
	// 監査は接続元ごとに抑える（2回断っても1行）。
	if n := auditCount(t, "device.refused"); n != 1 {
		t.Errorf("断った記録が %d 行です（1行のはず）", n)
	}
}

func TestOutsideRecordOnlyPassesAndAudits(t *testing.T) {
	setupDeviceTest(t, false)
	rr := devLogin("taro", "pw-taro", true, devCookies{}, nil)
	session := cookieValue(rr, sessionCookieName)
	if session == "" {
		t.Fatal("断らない設定なのに入れません")
	}
	if n := auditCount(t, "login.unregistered-device"); n != 1 {
		t.Errorf("登録のない端末の印が %d 行です", n)
	}
	if rr := devAPI(devOK, http.MethodGet, "/x", "", true, devCookies{session: session}); rr.Code != http.StatusNoContent {
		t.Errorf("断らない設定で通りません: %d", rr.Code)
	}
}

func TestInsideLoginNeedsNoDevice(t *testing.T) {
	setupDeviceTest(t, true)
	rr := devLogin("taro", "pw-taro", false, devCookies{}, nil)
	session := cookieValue(rr, sessionCookieName)
	if session == "" {
		t.Fatal("社内から入れません")
	}
	if rr := devAPI(devOK, http.MethodGet, "/x", "", false, devCookies{session: session}); rr.Code != http.StatusNoContent {
		t.Errorf("社内で通りません: %d", rr.Code)
	}
	if n := auditCount(t, "login.unregistered-device"); n != 0 {
		t.Errorf("社内のログインに印が付きました: %d", n)
	}
}

func TestRegisteredDeviceLogsInFromOutside(t *testing.T) {
	setupDeviceTest(t, true)
	c := registerInside(t, "taro")
	rr := devLogin("taro", "pw-taro", true, devCookies{device: c.device}, nil)
	session := cookieValue(rr, sessionCookieName)
	if session == "" {
		t.Fatalf("登録した端末で入れません: %s", rr.Header().Get("Location"))
	}
	if rr := devAPI(devOK, http.MethodGet, "/x", "", true, devCookies{session: session, device: c.device}); rr.Code != http.StatusNoContent {
		t.Errorf("登録した端末で通りません: %d", rr.Code)
	}
	// 端末があっても、パスワードは要る。
	if rr := devLogin("taro", "wrong", true, devCookies{device: c.device}, nil); cookieValue(rr, sessionCookieName) != "" {
		t.Error("パスワードなしで入れました")
	}
}

func TestRegisterRefusedOnAddress(t *testing.T) {
	setupDeviceTest(t, false)
	session := cookieValue(devLogin("taro", "pw-taro", false, devCookies{}, nil), sessionCookieName)
	r := devRequest(http.MethodPost, "/api/devices/register", `{"name":"x"}`, false)
	r.Host = "192.168.1.100:8443"
	devCookies{session: session}.add(r)
	rr := httptest.NewRecorder()
	RequireAuth(http.HandlerFunc(DeviceRegisterAPIHandler)).ServeHTTP(rr, r)
	if rr.Code != http.StatusConflict {
		t.Errorf("数字の住所で登録できました: %d", rr.Code)
	}
}

// 社内で入ったパソコンを、そのまま社外へ持ち出しても通らない。
func TestInsideSessionCarriedOutsideRefused(t *testing.T) {
	setupDeviceTest(t, true)
	session := cookieValue(devLogin("taro", "pw-taro", false, devCookies{}, nil), sessionCookieName)
	if rr := devAPI(devOK, http.MethodGet, "/api/x", "", true, devCookies{session: session}); rr.Code != http.StatusUnauthorized {
		t.Errorf("持ち出したセッションが通りました: %d", rr.Code)
	}
}

// セッションの Cookie だけを持ち出しても通らない。
func TestSessionWithoutDeviceCookieRefused(t *testing.T) {
	setupDeviceTest(t, true)
	c := registerInside(t, "taro")
	if rr := devAPI(devOK, http.MethodGet, "/api/x", "", true, devCookies{session: c.session}); rr.Code != http.StatusUnauthorized {
		t.Errorf("印なしで通りました: %d", rr.Code)
	}
	if rr := devAPI(devOK, http.MethodGet, "/api/x", "", true, devCookies{session: c.session, device: "other"}); rr.Code != http.StatusUnauthorized {
		t.Errorf("違う印で通りました: %d", rr.Code)
	}
	if rr := devAPI(devOK, http.MethodGet, "/api/x", "", true, c); rr.Code != http.StatusNoContent {
		t.Errorf("正しい組が通りません: %d", rr.Code)
	}
}

func TestRevokeCutsSessionImmediately(t *testing.T) {
	setupDeviceTest(t, true)
	c := registerInside(t, "taro")
	list, _ := listDevices("taro")
	body := `{"id":` + jsonInt(list[0].ID) + `}`
	if rr := devAPI(DeviceRevokeAPIHandler, http.MethodPost, "/api/devices/revoke", body, false, c); rr.Code != http.StatusOK {
		t.Fatalf("取り消せません %d: %s", rr.Code, rr.Body)
	}
	if rr := devAPI(devOK, http.MethodGet, "/api/x", "", false, c); rr.Code != http.StatusUnauthorized {
		t.Errorf("取り消した端末のログインが残っています: %d", rr.Code)
	}
	if rr := devLogin("taro", "pw-taro", true, devCookies{device: c.device}, nil); !strings.Contains(rr.Header().Get("Location"), "error=device") {
		t.Errorf("取り消した端末で入れます: %s", rr.Header().Get("Location"))
	}
}

func jsonInt(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestCodeRegistersDeviceFromOutside(t *testing.T) {
	setupDeviceTest(t, true)
	c := registerInside(t, "taro")
	code := issueCode(t, c)

	// 番号が合っていても、パスワードが違えば入れず、番号も残る。
	rr := devLogin("taro", "wrong", true, devCookies{}, url.Values{"device_code": {code}})
	if cookieValue(rr, sessionCookieName) != "" || cookieValue(rr, deviceCookieName) != "" {
		t.Fatal("パスワードなしで登録されました")
	}
	testNow = testNow.Add(5 * time.Second) // 間違えたパスワードのあとの待ち

	rr = devLogin("taro", "pw-taro", true, devCookies{}, url.Values{"device_code": {code[:4] + " " + code[4:]}, "device_name": {"家のパソコン"}})
	session, device := cookieValue(rr, sessionCookieName), cookieValue(rr, deviceCookieName)
	if session == "" || device == "" {
		t.Fatalf("番号で登録できません: %s", rr.Header().Get("Location"))
	}
	if rr := devAPI(devOK, http.MethodGet, "/x", "", true, devCookies{session: session, device: device}); rr.Code != http.StatusNoContent {
		t.Errorf("番号で登録した端末で通りません: %d", rr.Code)
	}
	if list, _ := listDevices("taro"); len(list) != 2 {
		t.Errorf("端末が %d 個です（2個のはず）", len(list))
	}

	// 同じ番号は2度使えない。
	rr = devLogin("taro", "pw-taro", true, devCookies{}, url.Values{"device_code": {code}})
	if !strings.Contains(rr.Header().Get("Location"), "error=code") {
		t.Errorf("番号を2度使えました: %s", rr.Header().Get("Location"))
	}
}

func TestCodeDiscardedAfterRepeatedFailures(t *testing.T) {
	setupDeviceTest(t, true)
	code := issueCode(t, registerInside(t, "taro"))
	wrong := "00000000"
	if code == wrong {
		wrong = "00000001"
	}
	for i := 0; i < deviceCodeMaxFails; i++ {
		devLogin("taro", "pw-taro", true, devCookies{}, url.Values{"device_code": {wrong}})
	}
	rr := devLogin("taro", "pw-taro", true, devCookies{}, url.Values{"device_code": {code}})
	if !strings.Contains(rr.Header().Get("Location"), "error=code") {
		t.Errorf("続けて間違えたあとも番号が効きます: %s", rr.Header().Get("Location"))
	}
}

func TestCodeExpires(t *testing.T) {
	setupDeviceTest(t, true)
	code := issueCode(t, registerInside(t, "taro"))
	testNow = testNow.Add(deviceCodeTTL + time.Second)
	rr := devLogin("taro", "pw-taro", true, devCookies{}, url.Values{"device_code": {code}})
	if !strings.Contains(rr.Header().Get("Location"), "error=code") {
		t.Errorf("切れた番号が効きます: %s", rr.Header().Get("Location"))
	}
}

// 登録のない端末は、断らない設定のあいだも番号を出せない・取り消せない・番号なしで登録できない
// （パスワードだけを盗んだ人が端末を足せないように）。
func TestOutsideUnregisteredCannotManage(t *testing.T) {
	setupDeviceTest(t, false)
	c := registerInside(t, "taro")
	session := cookieValue(devLogin("taro", "pw-taro", true, devCookies{}, nil), sessionCookieName)
	out := devCookies{session: session}
	if rr := devAPI(DeviceCodeAPIHandler, http.MethodPost, "/api/devices/code", "", true, out); rr.Code != http.StatusForbidden {
		t.Errorf("番号を出せました: %d", rr.Code)
	}
	if rr := devAPI(DeviceRegisterAPIHandler, http.MethodPost, "/api/devices/register", `{"name":"x"}`, true, out); rr.Code != http.StatusForbidden {
		t.Errorf("番号なしで登録できました: %d", rr.Code)
	}
	list, _ := listDevices("taro")
	if rr := devAPI(DeviceRevokeAPIHandler, http.MethodPost, "/api/devices/revoke", `{"id":`+jsonInt(list[0].ID)+`}`, true, out); rr.Code != http.StatusForbidden {
		t.Errorf("取り消せました: %d", rr.Code)
	}
	_ = c
}

func TestDeviceBelongsToItsUser(t *testing.T) {
	setupDeviceTest(t, true)
	if err := CreateUser("jiro", "pw-jiro", false, ""); err != nil {
		t.Fatal(err)
	}
	if err := CreateUser("boss", "pw-boss", true, ""); err != nil {
		t.Fatal(err)
	}
	taro := registerInside(t, "taro")
	// 太郎の端末で次郎は社外から入れない。
	if rr := devLogin("jiro", "pw-jiro", true, devCookies{device: taro.device}, nil); !strings.Contains(rr.Header().Get("Location"), "error=device") {
		t.Errorf("ほかの人の端末で入れました: %s", rr.Header().Get("Location"))
	}
	list, _ := listDevices("taro")
	body := `{"id":` + jsonInt(list[0].ID) + `}`
	jiro := devCookies{session: cookieValue(devLogin("jiro", "pw-jiro", false, devCookies{}, nil), sessionCookieName)}
	if rr := devAPI(DeviceRevokeAPIHandler, http.MethodPost, "/api/devices/revoke", body, false, jiro); rr.Code != http.StatusNotFound {
		t.Errorf("ほかの人の端末を取り消せました: %d", rr.Code)
	}
	// 一覧も本人の分だけ（all=1 は管理者だけ）。
	rr := devAPI(DevicesAPIHandler, http.MethodGet, "/api/devices?all=1", "", false, jiro)
	if strings.Contains(rr.Body.String(), "事務所") {
		t.Errorf("ほかの人の端末が見えます: %s", rr.Body)
	}
	boss := devCookies{session: cookieValue(devLogin("boss", "pw-boss", false, devCookies{}, nil), sessionCookieName)}
	rr = devAPI(DevicesAPIHandler, http.MethodGet, "/api/devices?all=1", "", false, boss)
	if !strings.Contains(rr.Body.String(), "事務所") {
		t.Errorf("管理者に全員の端末が見えません: %s", rr.Body)
	}
	if rr := devAPI(DeviceRevokeAPIHandler, http.MethodPost, "/api/devices/revoke", body, false, boss); rr.Code != http.StatusOK {
		t.Errorf("管理者が取り消せません: %d", rr.Code)
	}
}

func TestUnusedDeviceIsRevoked(t *testing.T) {
	setupDeviceTest(t, true)
	c := registerInside(t, "taro")
	testNow = testNow.Add(deviceUnusedTTL + time.Hour)
	rr := devLogin("taro", "pw-taro", true, devCookies{device: c.device}, nil)
	if !strings.Contains(rr.Header().Get("Location"), "error=device") {
		t.Errorf("長く使っていない端末で入れます: %s", rr.Header().Get("Location"))
	}
	if list, _ := listDevices("taro"); len(list) != 0 {
		t.Errorf("取り消されていません: %d", len(list))
	}
	if n := auditCount(t, "device.expired"); n != 1 {
		t.Errorf("監査に残っていません: %d", n)
	}
}

// 使っていれば、180日を過ぎても切れない。
func TestUsedDeviceStaysAndCookieIsRenewed(t *testing.T) {
	setupDeviceTest(t, true)
	c := registerInside(t, "taro")
	testNow = testNow.Add(100 * 24 * time.Hour)
	session := cookieValue(devLogin("taro", "pw-taro", true, devCookies{device: c.device}, nil), sessionCookieName)
	testNow = testNow.Add(2 * time.Minute)
	rr := devAPI(devOK, http.MethodGet, "/x", "", true, devCookies{session: session, device: c.device})
	if cookieValue(rr, deviceCookieName) != c.device {
		t.Error("使ったのに印を出し直していません")
	}
	testNow = testNow.Add(100 * 24 * time.Hour)
	if rr := devLogin("taro", "pw-taro", true, devCookies{device: c.device}, nil); cookieValue(rr, sessionCookieName) == "" {
		t.Errorf("使っている端末が切れました: %s", rr.Header().Get("Location"))
	}
}

func TestLoginPageShowsCodeFieldOnlyWhenNeeded(t *testing.T) {
	setupDeviceTest(t, false)
	page := func(outside bool, c devCookies) string {
		r := devRequest(http.MethodGet, "/login", "", outside)
		c.add(r)
		rr := httptest.NewRecorder()
		LoginPageHandler(rr, r)
		return rr.Body.String()
	}
	if strings.Contains(page(false, devCookies{}), "device_code") {
		t.Error("社内のログインに番号の欄が出ています")
	}
	if !strings.Contains(page(true, devCookies{}), "device_code") {
		t.Error("社外の登録のない端末に番号の欄がありません")
	}
	c := registerInside(t, "taro")
	if strings.Contains(page(true, devCookies{device: c.device}), "device_code") {
		t.Error("登録した端末に番号の欄が出ています")
	}
	if strings.Contains(page(false, devCookies{}), "%device%") {
		t.Error("置き換えの印が残っています")
	}
}

func TestLocalSocketCannotManageDevices(t *testing.T) {
	setupDeviceTest(t, false)
	r := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	rr := httptest.NewRecorder()
	LocalSocket(RequireAuth(http.HandlerFunc(DevicesAPIHandler)), "taro").ServeHTTP(rr, r)
	if rr.Code != http.StatusForbidden {
		t.Errorf("中の入口で端末を扱えました: %d", rr.Code)
	}
}

// 中の入口は、断る設定でも今までどおり通る。
func TestLocalSocketUnaffected(t *testing.T) {
	setupDeviceTest(t, true)
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.RemoteAddr = "@"
	rr := httptest.NewRecorder()
	LocalSocket(RequireAuth(http.HandlerFunc(devOK)), "taro").ServeHTTP(rr, r)
	if rr.Code != http.StatusNoContent {
		t.Errorf("中の入口が止まりました: %d", rr.Code)
	}
}

func TestSessionDeviceColumnIsAddedToOldDB(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE sessions (token_hash TEXT PRIMARY KEY, username TEXT NOT NULL, created_at DATETIME NOT NULL, expires_at DATETIME NOT NULL, last_seen DATETIME NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // 2度流しても落ちない
		if err := database.CreateAuthTables(db); err != nil {
			t.Fatalf("%d 回目: %v", i+1, err)
		}
	}
	if _, err := db.Exec(`SELECT device_id FROM sessions`); err != nil {
		t.Errorf("device_id がありません: %v", err)
	}
}

func TestDeviceSmallParts(t *testing.T) {
	for host, want := range map[string]bool{
		"192.168.1.100:8443": true, "192.168.1.100": true, "[::1]:8443": true, "[fe80::1]": true,
		"example.test": false, "example.test:8443": false, "localhost:9443": false,
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = host
		if got := hostIsAddress(r); got != want {
			t.Errorf("hostIsAddress(%q) = %v", host, got)
		}
	}
	if got := normalizeDeviceCode(" １２34-56 78"); got != "12345678" {
		t.Errorf("番号の整え: %q", got)
	}
	if got := cleanDeviceName("  家の\nPC\x00 "); got != "家のPC" {
		t.Errorf("名前の整え: %q", got)
	}
	if got := []rune(cleanDeviceName(strings.Repeat("あ", 100))); len(got) != deviceNameMaxRunes {
		t.Errorf("名前の長さ: %d", len(got))
	}
}
