package cms

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"w-cms/internal/database"
)

// TestEncryptPushMatchesRFC8291 は、通知の中身の暗号が RFC 8291 の例（5章・付録A）と1バイトも違わないことを固定します
// （2026-10-21 夜）。手で書いた暗号が、本物のブラウザの通知の道と食い違わないための試験です。
func TestEncryptPushMatchesRFC8291(t *testing.T) {
	d := func(s string) []byte {
		b, err := base64.RawURLEncoding.DecodeString(strings.ReplaceAll(s, " ", ""))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	asPriv, err := ecdh.P256().NewPrivateKey(d("yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	uaPublic := d("BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	authSecret := d("BTBZMqHH6r4Tts7J_aSIgg")
	salt := d("DGv6ra1nlYgDCS1FRnbzlw")
	got, err := encryptPush(uaPublic, authSecret, []byte("When I grow up, I want to be a watermelon"), asPriv, salt)
	if err != nil {
		t.Fatal(err)
	}
	want := d("DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml" +
		"mlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPT" +
		"pK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN")
	if string(got) != string(want) {
		t.Errorf("⚠ 暗号文が RFC 8291 の例と違います:\n got  %s\n want %s", base64.RawURLEncoding.EncodeToString(got), base64.RawURLEncoding.EncodeToString(want))
	}
}

// TestVAPIDHeaderVerifies は、送り手の名乗り（VAPID の JWT）が、公開鍵で確かめられる ES256 の署名になっていることを固定します。
func TestVAPIDHeaderVerifies(t *testing.T) {
	database.SetDataDir(t.TempDir())
	t.Cleanup(func() { database.SetDataDir("") })
	k, err := loadVAPID()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { vapidKey.Lock(); vapidKey.key = nil; vapidKey.Unlock() }()
	h, err := vapidHeader(k, "https://fcm.googleapis.com/fcm/send/abc", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	tPart, kPart, ok := strings.Cut(strings.TrimPrefix(h, "vapid t="), ", k=")
	if !ok {
		t.Fatalf("形が違います: %s", h)
	}
	parts := strings.Split(tPart, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT が3つに分かれません: %s", tPart)
	}
	var claims map[string]any
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	json.Unmarshal(cb, &claims)
	if claims["aud"] != "https://fcm.googleapis.com" {
		t.Errorf("aud が %v です", claims["aud"])
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(&k.PublicKey, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Errorf("⚠ 署名が公開鍵で確かめられません")
	}
	if pub, _ := vapidPublic(k); pub != kPart {
		t.Errorf("k が公開鍵と違います")
	}
}

// TestSendPushPostsAndDropsGone は、通知を送ると届け先へ暗号の頭（aes128gcm）と名乗り（vapid）を付けて届け、
// 届け先が「もう無い」（410）と答えた受け口は捨てることを固定します。
func TestSendPushPostsAndDropsGone(t *testing.T) {
	database.SetDataDir(t.TempDir())
	t.Cleanup(func() { database.SetDataDir("") })
	defer func() { vapidKey.Lock(); vapidKey.key = nil; vapidKey.Unlock() }()

	status := http.StatusCreated
	var gotEnc, gotAuth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEnc, gotAuth = r.Header.Get("Content-Encoding"), r.Header.Get("Authorization")
		w.WriteHeader(status)
	}))
	defer srv.Close()
	orig := pushHTTP
	pushHTTP = srv.Client()
	t.Cleanup(func() { pushHTTP = orig })

	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	authSecret := make([]byte, 16)
	rand.Read(authSecret)
	if err := savePushSubs(map[string][]pushSubscription{"alice": {{Endpoint: srv.URL + "/push/abc",
		P256dh: enc.EncodeToString(ua.PublicKey().Bytes()), Auth: enc.EncodeToString(authSecret)}}}); err != nil {
		t.Fatal(err)
	}
	if n := SendPush("alice", PushMessage{Title: "t", Body: "b", URL: "/000001"}); n != 1 {
		t.Fatalf("送れた端末が %d です（1 を期待）", n)
	}
	if gotEnc != "aes128gcm" || !strings.HasPrefix(gotAuth, "vapid t=") {
		t.Errorf("⚠ 頭が違います: Content-Encoding %q・Authorization %q", gotEnc, gotAuth)
	}
	status = http.StatusGone
	SendPush("alice", PushMessage{Title: "t"})
	if all, err := loadPushSubs(); err != nil || len(all["alice"]) != 0 {
		t.Errorf("⚠ 「もう無い」と答えた受け口を捨てていません")
	}
	if got := PushSubscribers(); len(got) != 0 {
		t.Errorf("受け口の無い人が通知を受ける人に残っています: %v", got)
	}
}

// 届け先が内側のアドレスなら、つながない（利用者が渡す endpoint で、社内の機械へ POST させないため・2026-10-22 の見直し）。
func TestPushRefusesInsideAddresses(t *testing.T) {
	for _, a := range []string{"127.0.0.1:443", "[::1]:443", "10.0.0.5:443", "192.168.179.4:8443", "172.16.0.1:443", "169.254.1.1:443", "100.64.0.1:443", "0.0.0.0:443", "[::ffff:127.0.0.1]:443", "[fe80::1]:443"} {
		if err := refuseInsideAddress("tcp", a, nil); err == nil {
			t.Errorf("⚠ 内側のアドレス %s へつなごうとします", a)
		}
	}
	for _, a := range []string{"142.250.196.106:443", "[2404:6800:4004:80a::200a]:443"} {
		if err := refuseInsideAddress("tcp", a, nil); err != nil {
			t.Errorf("外のアドレス %s を断りました: %v", a, err)
		}
	}
}

// 受け口の覚えが壊れていたら、空として読まない（読んで書き戻すと、ほかの人の受け口が全部消える）。
func TestPushSubsCorruptFileIsAnError(t *testing.T) {
	database.SetDataDir(t.TempDir())
	if err := os.MkdirAll(pushDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pushDir(), "subscriptions.json"), []byte("{壊れた"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPushSubs(); err == nil {
		t.Fatal("⚠ 壊れた覚えを空として読みました")
	}
	if PushSubscribers() != nil {
		t.Error("⚠ 壊れた覚えから、通知を受ける人を返しました")
	}
	if b, _ := os.ReadFile(filepath.Join(pushDir(), "subscriptions.json")); string(b) != "{壊れた" {
		t.Errorf("⚠ 壊れた覚えを書き換えました: %q", b)
	}
}

// 受け口の鍵は、決まった長さのときだけ覚える（壊れた受け口は、送るたびに失敗して残り続けるため）。
func TestPushKeyLen(t *testing.T) {
	k := base64.RawURLEncoding.EncodeToString(make([]byte, 65))
	if !pushKeyLen(k, 65) || !pushKeyLen(k+"=", 65) {
		t.Errorf("65バイトの鍵を断りました")
	}
	if pushKeyLen("abc", 65) || pushKeyLen(k, 16) || pushKeyLen("", 16) {
		t.Errorf("⚠ 長さの違う鍵を通しました")
	}
}
