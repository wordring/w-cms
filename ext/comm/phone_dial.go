package comm

// 業務サーバーのモデムで電話をかける（2026-10-28・cmd/w-cms-dial）。
//
//	GET  /api/phone/dial/info    … {available} モデムでかけられるか（道具のソケットがあるか）
//	POST /api/phone/dial         {number} … モデムが番号を回す。会社の電話機の受話器を上げて話す
//	POST /api/phone/dial/release … 受話器を上げたので、モデムを線から離す
//
// 道具（w-cms-dial）とは、環境変数 WCMS_DIAL_SOCK の Unix ソケットだけでやりとりする。無ければ、この かけ方 は出ない。
// かけられるのは通信箱に書ける人だけ（発信の記録を作るのと同じ）。料金の高い番号は、FAX と同じく管理者だけ。
//
// > 2026-10-28 利用者:「会社のパソコンから書ける場合は3、スマホかける場合は1は駄目ですか？」

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// dialSock は道具のソケットの場所です（空なら、モデムではかけない）。
func dialSock() string { return os.Getenv("WCMS_DIAL_SOCK") }

// dialAvailable は、モデムでかけられるか（ソケットの場所が決まっていて、そこにソケットがあるか）です。
// 試験で差し替える。
var dialAvailable = func() bool {
	s := dialSock()
	if s == "" {
		return false
	}
	st, err := os.Stat(s)
	return err == nil && st.Mode()&os.ModeSocket != 0
}

// callDialer は道具に頼みます（path は /dial か /release）。答えの error を返す。
var callDialer = func(ctx context.Context, path string, body any) (int, map[string]any, error) {
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", dialSock())
	}}}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://dialer"+path, bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out, nil
}

// PhoneDialInfoAPIHandler は GET /api/phone/dial/info です。
func PhoneDialInfoAPIHandler(w http.ResponseWriter, r *http.Request) {
	if auth.CurrentUser(r) == nil {
		cms.JSONFail(w, http.StatusUnauthorized, "ログインしてください")
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "available": dialAvailable()})
}

// PhoneDialAPIHandler は POST /api/phone/dial です。
func PhoneDialAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Number string `json:"number"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	if _, err := WritableMailBox(user); err != nil {
		cms.JSONFail(w, http.StatusForbidden, "通信箱に書けないので、かけられません: "+err.Error())
		return
	}
	number, ok := outboundNumber(w, user, req.Number, "電話番号", "かけられる") // 数字だけにする（モデムの命令に人の書いた文字を混ぜない）
	if !ok {
		return
	}
	if !dialAvailable() {
		cms.JSONFail(w, http.StatusServiceUnavailable, "業務サーバーのモデムでかける道具が動いていません")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	code, out, err := callDialer(ctx, "/dial", map[string]string{"number": number})
	if err != nil {
		cms.JSONFail(w, http.StatusServiceUnavailable, "モデムでかける道具に頼めませんでした: "+err.Error())
		return
	}
	if code != http.StatusOK {
		msg, _ := out["error"].(string)
		if msg == "" {
			msg = http.StatusText(code)
		}
		if code != http.StatusConflict {
			code = http.StatusServiceUnavailable
		}
		cms.JSONFail(w, code, "かけられませんでした: "+msg)
		return
	}
	auth.Audit(user.Username, "phone.dial", number)
	hold, _ := out["hold_sec"].(float64)
	cms.WriteJSON(w, map[string]any{"success": true, "number": number, "hold_sec": int(hold)})
}

// PhoneDialReleaseAPIHandler は POST /api/phone/dial/release です。
func PhoneDialReleaseAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	if _, err := WritableMailBox(user); err != nil {
		cms.JSONFail(w, http.StatusForbidden, "通信箱に書けません: "+err.Error())
		return
	}
	if !dialAvailable() {
		cms.WriteJSON(w, map[string]any{"success": true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, _, err := callDialer(ctx, "/release", map[string]string{}); err != nil {
		cms.JSONFail(w, http.StatusServiceUnavailable, "モデムを離せませんでした（しばらくすると自動で離れます）: "+err.Error())
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true})
}
