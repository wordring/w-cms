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
	if len(loadPushSubs()["alice"]) != 0 {
		t.Errorf("⚠ 「もう無い」と答えた受け口を捨てていません")
	}
	if got := PushSubscribers(); len(got) != 0 {
		t.Errorf("受け口の無い人が通知を受ける人に残っています: %v", got)
	}
}
