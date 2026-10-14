package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"w-cms/internal/database"
)

// セッションの期限の決まり（【要求】権限 §1・2026-10-14）を固定します。
//
//   - 最後の操作から8時間で切れる（アイドル基準へ一本化）。
//   - 操作で延びる。ログインからの絶対期限は無い（操作し続ければ8時間を過ぎても切れない）。
//   - 裏で自動に飛ぶ要求（知らせの購読・キャレットの知らせ・印の付いた要求・残りを見る GET /api/session）では延びない。
//   - Cookie の寿命も最後の操作から8時間で、延びたときに出し直す。

// sessionRequest は、セッションの Cookie を付けた要求を RequireAuth に通し、応答を返します。
func sessionRequest(t *testing.T, token, method, path string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc(SessionAPIPath, SessionAPIHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	RequireAuth(mux).ServeHTTP(rr, req)
	return rr
}

// newSessionUser は利用者を作ってログインし、セッションのトークンを返します。
func newSessionUser(t *testing.T, name string) string {
	t.Helper()
	if err := CreateUser(name, "pw-"+name, false, ""); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	token, err := CreateSession(name)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return token
}

// sessionCookieOf は応答が出したセッションの Cookie を返します（無ければ nil）。
func sessionCookieOf(rr *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	return nil
}

// TestSessionExpiresAfterIdle8h は、最後の操作から8時間で切れ、それより前は切れないことを固定します。
func TestSessionExpiresAfterIdle8h(t *testing.T) {
	setupAuthDB(t)
	token, _ := CreateSession("bob")

	advanceClock(8*time.Hour - time.Second)
	if _, ok := ResolveSession(token); !ok {
		t.Fatal("最後の操作から8時間たたないのに切れました")
	}
	advanceClock(2 * time.Second)
	if _, ok := ResolveSession(token); ok {
		t.Fatal("最後の操作から8時間を過ぎても切れません")
	}
	// 期限切れのセッションは消える（あとで時計を戻しても生き返らない）。
	var n int
	database.AuthDB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	if n != 0 {
		t.Errorf("期限切れのセッションが残っています: %d", n)
	}
}

// TestSessionNoLongerExpiresAfter30Minutes は、30分のアイドルで切れなくなったことを固定します（旧い決まりの後戻りの番人）。
func TestSessionNoLongerExpiresAfter30Minutes(t *testing.T) {
	setupAuthDB(t)
	token, _ := CreateSession("carol")
	advanceClock(31 * time.Minute)
	if _, ok := ResolveSession(token); !ok {
		t.Fatal("31分の放置で切れました（アイドルは8時間のはず）")
	}
}

// TestSessionOperationExtendsWithoutAbsoluteLimit は、操作で延び、ログインから8時間を過ぎても切れないことを固定します。
func TestSessionOperationExtendsWithoutAbsoluteLimit(t *testing.T) {
	setupAuthDB(t)
	token := newSessionUser(t, "dave")

	// 7時間ごとに操作（ページを開く・保存）——ログインから21時間たっても切れない。
	for i, op := range []struct{ method, path string }{
		{"GET", "/000123"},
		{"POST", "/api/sync"},
		{"GET", "/api/load"},
	} {
		advanceClock(7 * time.Hour)
		if rr := sessionRequest(t, token, op.method, op.path, nil); rr.Code != http.StatusNoContent {
			t.Fatalf("%d回目の操作（%s %s）が通りません: %d", i+1, op.method, op.path, rr.Code)
		}
	}
	advanceClock(8*time.Hour - time.Second)
	if _, ok := ResolveSession(token); !ok {
		t.Fatal("最後の操作から8時間たたないのに切れました（ログインからの絶対期限が残っている？）")
	}
	advanceClock(2 * time.Second)
	if _, ok := ResolveSession(token); ok {
		t.Fatal("最後の操作から8時間を過ぎても切れません")
	}
}

// TestSessionBackgroundRequestsDoNotExtend は、裏で自動に飛ぶ要求では延びないことを固定します。
func TestSessionBackgroundRequestsDoNotExtend(t *testing.T) {
	cases := []struct {
		name, method, path string
		header             map[string]string
	}{
		{"ページの更新の知らせの購読", "GET", "/api/page-events", nil},
		{"編集ロックの知らせの購読", "GET", "/api/lock-events", nil},
		{"キャレットのあるブロックの知らせ", "POST", "/api/presence", nil},
		{"残り時間を見る", "GET", SessionAPIPath, nil},
		{"裏の印の付いた要求（知らせを受けての読み直し）", "GET", "/api/load", map[string]string{BackgroundHeader: "1"}},
		{"裏の印の付いた要求（メールの読み込みの様子）", "GET", "/api/mail/fetch-new/status", map[string]string{BackgroundHeader: "1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setupAuthDB(t)
			token := newSessionUser(t, "erin")
			// 7時間59分のあいだ、自動の要求だけが飛び続ける。
			for i := 0; i < 7; i++ {
				advanceClock(time.Hour)
				rr := sessionRequest(t, token, c.method, c.path, c.header)
				if rr.Code == http.StatusUnauthorized {
					t.Fatalf("%d時間目で切れました", i+1)
				}
				if sessionCookieOf(rr) != nil {
					t.Fatalf("自動の要求で Cookie を出し直しました（寿命が延びる）")
				}
			}
			advanceClock(time.Hour + time.Second)
			if _, ok := ResolveSession(token); ok {
				t.Fatal("自動の要求だけで寿命が延びています（最後の操作から8時間を過ぎても切れない）")
			}
		})
	}
}

// TestSessionCookieFollowsLastOperation は、Cookie の寿命が最後の操作から8時間で、延びたときに出し直すことを固定します。
func TestSessionCookieFollowsLastOperation(t *testing.T) {
	setupAuthDB(t)
	token := newSessionUser(t, "frank")

	// 前の記録から1分たたない操作では、書き直さず Cookie も出さない（auth.db へ書きすぎない）。
	advanceClock(30 * time.Second)
	if c := sessionCookieOf(sessionRequest(t, token, "GET", "/000123", nil)); c != nil {
		t.Errorf("1分たたない操作で Cookie を出し直しました")
	}

	advanceClock(2 * time.Hour)
	c := sessionCookieOf(sessionRequest(t, token, "GET", "/000123", nil))
	if c == nil {
		t.Fatal("操作で寿命が延びたのに Cookie を出し直していません（ブラウザの Cookie が先に消える）")
	}
	if c.MaxAge != int((8 * time.Hour).Seconds()) {
		t.Errorf("Cookie の寿命が8時間ではありません: MaxAge=%d", c.MaxAge)
	}
	if c.Value != token || !c.HttpOnly || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("出し直した Cookie の属性がログインのときと違います: %+v", c)
	}
}

// TestSessionAPIRemaining は、残り時間の口が残りを返し、GET では延びず、POST（続ける）で延びることを固定します。
func TestSessionAPIRemaining(t *testing.T) {
	setupAuthDB(t)
	token := newSessionUser(t, "grace")

	advanceClock(7*time.Hour + 56*time.Minute)
	rr := sessionRequest(t, token, "GET", SessionAPIPath, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/session: %d", rr.Code)
	}
	var got struct {
		ExpiresIn  int `json:"expires_in"`
		IdleTTL    int `json:"idle_ttl"`
		WarnBefore int `json:"warn_before"`
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.ExpiresIn != 4*60 || got.IdleTTL != 8*3600 || got.WarnBefore != 5*60 {
		t.Errorf("残り時間が違います: %+v（期待 expires_in=240 idle_ttl=28800 warn_before=300）", got)
	}

	// 告知の「続ける」——POST は操作なので延びる。
	rr = sessionRequest(t, token, "POST", SessionAPIPath, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/session: %d", rr.Code)
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.ExpiresIn != 8*3600 {
		t.Errorf("「続ける」のあとの残りが8時間ではありません: %d", got.ExpiresIn)
	}
	if sessionCookieOf(rr) == nil {
		t.Error("「続ける」で Cookie を出し直していません")
	}

	// 失効したら 401（画面はログインへ）。
	advanceClock(8*time.Hour + time.Second)
	if rr := sessionRequest(t, token, "GET", SessionAPIPath, nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("失効したあとの GET /api/session が 401 ではありません: %d", rr.Code)
	}
}

// TestSessionIgnoresOldAbsoluteExpiry は、2026-10-14 より前のセッション（expires_at がログインから8時間）も、
// 最後の操作から8時間で決まることを固定します——入れ替えた日に、操作中の人が絶対期限で切られないこと。
func TestSessionIgnoresOldAbsoluteExpiry(t *testing.T) {
	setupAuthDB(t)
	token, _ := CreateSession("heidi")
	database.AuthDB.Exec(`UPDATE sessions SET expires_at = ?`, testNow.Add(-time.Minute))
	if _, ok := ResolveSession(token); !ok {
		t.Error("最後の操作が今なのに、古い絶対期限で切れました")
	}
}
