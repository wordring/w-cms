package auth

import (
	"encoding/json"
	"net/http"
	"strings"
)

// 端末の登録の API です（device.go・画面は assets/devices.html）。どれもログインが要ります。

const deviceAddressRefusal = "数字の住所で開いているので登録できません。社外から使う名前で開き直してください（登録は開いた名前ごとに別です）。"

func deviceJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// readDeviceBody は小さな JSON の本文を読みます。
func readDeviceBody(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "リクエストが読めません", http.StatusBadRequest)
		return false
	}
	return true
}

// deviceUser は、ブラウザから来たログインを確かめます（業務サーバーの中の入口は端末を持たない）。
func deviceUser(w http.ResponseWriter, r *http.Request) *User {
	u := CurrentUser(r)
	if u == nil {
		http.Error(w, "認証が必要です", http.StatusUnauthorized)
		return nil
	}
	if _, inner := localSocketUser(r); inner {
		http.Error(w, "この入口では端末を扱えません", http.StatusForbidden)
		return nil
	}
	return u
}

// devicePostUser は、POST の方法とログインを確かめます。
func devicePostUser(w http.ResponseWriter, r *http.Request) *User {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return nil
	}
	return deviceUser(w, r)
}

// DevicesAPIHandler は端末の一覧といまの状態を返します（GET /api/devices・管理者は ?all=1 で全員の分）。
func DevicesAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u := deviceUser(w, r)
	if u == nil {
		return
	}
	sweepUnusedDevices()
	who := u.Username
	if u.IsAdmin && r.URL.Query().Get("all") == "1" {
		who = ""
	}
	list, err := listDevices(who)
	if err != nil {
		http.Error(w, "端末の一覧を読めませんでした", http.StatusInternalServerError)
		return
	}
	var thisID int64
	if d := deviceOfRequest(r); d != nil && d.Username == u.Username {
		thisID = d.ID
	}
	outside := IsFromTrustedProxy(r)
	deviceJSON(w, map[string]interface{}{
		"devices":         list,
		"this_device":     thisID,
		"outside":         outside,
		"required":        DeviceRequired(),
		"host_is_address": !outside && hostIsAddress(r),
		"can_issue":       deviceTrusted(r),
		"is_admin":        u.IsAdmin,
		"username":        u.Username,
	})
}

// DeviceRegisterAPIHandler は、いま開いている端末を登録します（POST /api/devices/register）。
// 社内からは番号なしで、社外からは一回限りの番号で登録します。
func DeviceRegisterAPIHandler(w http.ResponseWriter, r *http.Request) {
	u := devicePostUser(w, r)
	if u == nil {
		return
	}
	var req struct {
		Name string `json:"name"`
		Code string `json:"code"`
	}
	if !readDeviceBody(w, r, &req) {
		return
	}
	outside := IsFromTrustedProxy(r)
	if !outside && hostIsAddress(r) {
		http.Error(w, deviceAddressRefusal, http.StatusConflict)
		return
	}
	if d := deviceOfRequest(r); d != nil && d.Username == u.Username {
		http.Error(w, "この端末は登録済みです", http.StatusConflict)
		return
	}
	if outside {
		code := normalizeDeviceCode(req.Code)
		if !checkDeviceCode(u.Username, code) || !consumeDeviceCode(u.Username, code) {
			http.Error(w, "番号が違うか、切れています", http.StatusForbidden)
			return
		}
	}
	id, token, err := registerDevice(u.Username, cleanDeviceName(req.Name), clientIP(r))
	if err != nil {
		http.Error(w, "端末を登録できませんでした", http.StatusInternalServerError)
		return
	}
	setDeviceCookie(w, token)
	if err := bindSessionToDevice(SessionTokenOf(r), id); err != nil {
		http.Error(w, "端末は登録しましたが、いまのログインに結べませんでした。ログインし直してください", http.StatusInternalServerError)
		return
	}
	deviceJSON(w, map[string]interface{}{"success": true, "id": id})
}

// DeviceCodeAPIHandler は、ほかの端末を足すための一回限りの番号を出します（POST /api/devices/code）。
func DeviceCodeAPIHandler(w http.ResponseWriter, r *http.Request) {
	u := devicePostUser(w, r)
	if u == nil {
		return
	}
	if !deviceTrusted(r) {
		http.Error(w, "番号は、登録済みの端末か社内から出してください", http.StatusForbidden)
		return
	}
	code, err := issueDeviceCode(u.Username)
	if err != nil {
		http.Error(w, "番号を出せませんでした", http.StatusInternalServerError)
		return
	}
	Audit(u.Username, "device.code", clientIP(r))
	deviceJSON(w, map[string]interface{}{"code": code, "expires_in": int(deviceCodeTTL.Seconds())})
}

// DeviceRevokeAPIHandler は端末を取り消します（POST /api/devices/revoke・本人か管理者）。
func DeviceRevokeAPIHandler(w http.ResponseWriter, r *http.Request) {
	u := devicePostUser(w, r)
	if u == nil {
		return
	}
	if !deviceTrusted(r) {
		http.Error(w, "取り消しは、登録済みの端末か社内からしてください", http.StatusForbidden)
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if !readDeviceBody(w, r, &req) {
		return
	}
	list, err := listDevices("")
	if err != nil {
		http.Error(w, "端末の一覧を読めませんでした", http.StatusInternalServerError)
		return
	}
	var target *Device
	for i := range list {
		if list[i].ID == req.ID {
			target = &list[i]
		}
	}
	if target == nil || (target.Username != u.Username && !u.IsAdmin) {
		http.Error(w, "その端末はありません", http.StatusNotFound)
		return
	}
	if d := deviceOfRequest(r); d != nil && d.ID == target.ID {
		clearDeviceCookie(w)
	}
	if err := revokeDevice(target.ID); err != nil {
		http.Error(w, "取り消せませんでした", http.StatusInternalServerError)
		return
	}
	Audit(u.Username, "device.revoke", strings.TrimSpace(target.Username+" "+target.Name+" "+clientIP(r)))
	deviceJSON(w, map[string]interface{}{"success": true})
}
