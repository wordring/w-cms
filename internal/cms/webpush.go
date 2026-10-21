package cms

// ブラウザの通知（Web Push・2026-10-21 夜・docs/考察/【考察】通信箱の改善.md §7.5）。
//
// > 2026-10-21 利用者:「スマホに知らせて貰えれば最高です。」
//
// 外のサービスと契約せず、外部のライブラリも使わず、標準の決まりで作ります。
//
//   - 中身の暗号: RFC 8291（aes128gcm・P-256 の ECDH・HKDF・AES-GCM）。
//   - 送り手の名乗り: RFC 8292（VAPID・ES256 の JWT）。鍵は初めて使うときに作り、data/webpush/vapid.pem に置く。
//   - 受け口（購読）は利用者ごと・端末ごとに data/webpush/subscriptions.json へ。届け先が「もう無い」（404・410）と答えたら捨てる。
//
// 通知を届けるのは、スマホやパソコンのブラウザの会社の通知の道（Google・Apple など）です。中身は暗号にして渡すので、道の
// 会社には読めません。画面の側（assets/push.js・assets/sw.js）が受け口を作り、届いた通知を出し、押すとそのページを開きます。
//
// ⚠ ブラウザが受け口を作れるのは、正しい証明書の HTTPS で開いたページだけです（自分で作った証明書を信頼していない端末では作れない）。
//    iPhone は、w-cms をホーム画面に追加したときだけ受けられます。
//
// 口:
//
//	GET  /api/push/key          … 送り手の公開鍵（受け口を作るときに使う）
//	POST /api/push/subscribe    … この端末の受け口を覚える {endpoint, keys:{p256dh, auth}}
//	POST /api/push/unsubscribe  … この端末の受け口を捨てる {endpoint}
//	POST /api/push/test         … 自分の端末へ試しの通知を送る
//
// 送るのは SendPush（拡張が呼ぶ——通信の拡張は、電話・FAX・メールが届いたときに）。

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// PushMessage は通知1つです（端末の通知に出る題・本文と、押したときに開く場所）。
type PushMessage struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag,omitempty"` // 同じ印の通知は、端末で1つにまとまる
}

// pushSubscription は端末1つの受け口です。
type pushSubscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
	Added    string `json:"added"`
	Agent    string `json:"agent,omitempty"` // どの端末か（ブラウザの名乗り・表示用）
}

var pushMu sync.Mutex

func pushDir() string { return database.DataPath("webpush") }

// ── 中身の暗号（RFC 8291）──────────────────────────────────────────────

// encryptPush は plaintext を、受け口の公開鍵 uaPublic（65バイト）と auth（16バイト）に向けて暗号にし、送る本文を返します。
// asPriv（送り手の使い捨ての鍵）と salt は、試験で RFC の例の値を渡すために引数にしてある（ふだんは乱数）。
func encryptPush(uaPublic, authSecret, plaintext []byte, asPriv *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	curve := ecdh.P256()
	ua, err := curve.NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("受け口の鍵が読めません: %w", err)
	}
	if len(authSecret) != 16 || len(salt) != 16 {
		return nil, errors.New("auth と salt は16バイトです")
	}
	shared, err := asPriv.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPublic := asPriv.PublicKey().Bytes()
	// IKM = HKDF(auth_secret, ecdh_secret, "WebPush: info" || 0x00 || ua_public || as_public, 32)
	prkKey, err := hkdf.Extract(sha256.New, shared, authSecret)
	if err != nil {
		return nil, err
	}
	keyInfo := append(append([]byte("WebPush: info\x00"), uaPublic...), asPublic...)
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// 1レコードだけ——最後のレコードの区切り 0x02 を足す。
	ct := gcm.Seal(nil, nonce, append(append([]byte{}, plaintext...), 0x02), nil)
	var body bytes.Buffer
	body.Write(salt)
	binary.Write(&body, binary.BigEndian, uint32(4096))
	body.WriteByte(byte(len(asPublic)))
	body.Write(asPublic)
	body.Write(ct)
	return body.Bytes(), nil
}

// ── 送り手の名乗り（RFC 8292・VAPID）─────────────────────────────────

var vapidKey struct {
	sync.Mutex
	key *ecdsa.PrivateKey
}

// loadVAPID は送り手の鍵を読みます（無ければ作って data/webpush/vapid.pem に置く）。
func loadVAPID() (*ecdsa.PrivateKey, error) {
	vapidKey.Lock()
	defer vapidKey.Unlock()
	if vapidKey.key != nil {
		return vapidKey.key, nil
	}
	path := filepath.Join(pushDir(), "vapid.pem")
	if b, err := os.ReadFile(path); err == nil {
		if blk, _ := pem.Decode(b); blk != nil {
			if k, err := x509.ParseECPrivateKey(blk.Bytes); err == nil {
				vapidKey.key = k
				return k, nil
			}
		}
		return nil, errors.New("送り手の鍵 " + path + " が読めません")
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(pushDir(), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	vapidKey.key = k
	return k, nil
}

// vapidPublic は送り手の公開鍵（65バイト・base64url）です。
func vapidPublic(k *ecdsa.PrivateKey) (string, error) {
	pub, err := k.PublicKey.ECDH()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(pub.Bytes()), nil
}

// vapidHeader は届け先 endpoint への Authorization の値です（JWT は12時間有効）。
func vapidHeader(k *ecdsa.PrivateKey, endpoint string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("届け先が読めません")
	}
	sub := strings.TrimSpace(os.Getenv("WCMS_PUSH_SUBJECT"))
	if sub == "" {
		sub = "mailto:w-cms@example.com"
	}
	enc := base64.RawURLEncoding
	head := enc.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{"aud": u.Scheme + "://" + u.Host, "exp": now.Add(12 * time.Hour).Unix(), "sub": sub})
	signing := head + "." + enc.EncodeToString(claims)
	h := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, k, h[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	pub, err := vapidPublic(k)
	if err != nil {
		return "", err
	}
	return "vapid t=" + signing + "." + enc.EncodeToString(sig) + ", k=" + pub, nil
}

// ── 受け口の覚え ───────────────────────────────────────────────────────

func loadPushSubs() map[string][]pushSubscription {
	out := map[string][]pushSubscription{}
	b, err := os.ReadFile(filepath.Join(pushDir(), "subscriptions.json"))
	if err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

func savePushSubs(m map[string][]pushSubscription) error {
	if err := os.MkdirAll(pushDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return page.WriteFileAtomic(filepath.Join(pushDir(), "subscriptions.json"), b, 0o600)
}

// ── 送る ──────────────────────────────────────────────────────────────

// pushHTTP は送るときの HTTP の口です（試験で差し替える）。
var pushHTTP = &http.Client{Timeout: 20 * time.Second}

// SendPush は利用者 username の全部の端末へ通知を送ります（届け先が「もう無い」と答えた受け口は捨てる）。送れた端末の数を返します。
func SendPush(username string, m PushMessage) int {
	pushMu.Lock()
	subs := append([]pushSubscription{}, loadPushSubs()[username]...)
	pushMu.Unlock()
	if len(subs) == 0 {
		return 0
	}
	k, err := loadVAPID()
	if err != nil {
		log.Printf("通知を送れません（送り手の鍵）: %v", err)
		return 0
	}
	payload, _ := json.Marshal(m)
	sent := 0
	var gone []string
	for _, s := range subs {
		code, err := sendOnePush(k, s, payload)
		switch {
		case err == nil && code >= 200 && code < 300:
			sent++
		case code == http.StatusNotFound || code == http.StatusGone:
			gone = append(gone, s.Endpoint)
		default:
			log.Printf("通知を送れません（%s・%d）: %v", username, code, err)
		}
	}
	if len(gone) > 0 {
		pushMu.Lock()
		all := loadPushSubs()
		var keep []pushSubscription
		for _, s := range all[username] {
			drop := false
			for _, g := range gone {
				drop = drop || s.Endpoint == g
			}
			if !drop {
				keep = append(keep, s)
			}
		}
		all[username] = keep
		savePushSubs(all)
		pushMu.Unlock()
	}
	return sent
}

func sendOnePush(k *ecdsa.PrivateKey, s pushSubscription, payload []byte) (int, error) {
	dec := base64.RawURLEncoding
	ua, err := dec.DecodeString(strings.TrimRight(s.P256dh, "="))
	if err != nil {
		return 0, err
	}
	authSecret, err := dec.DecodeString(strings.TrimRight(s.Auth, "="))
	if err != nil {
		return 0, err
	}
	asPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return 0, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return 0, err
	}
	body, err := encryptPush(ua, authSecret, payload, asPriv, salt)
	if err != nil {
		return 0, err
	}
	authz, err := vapidHeader(k, s.Endpoint, time.Now())
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest("POST", s.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", "high")
	req.Header.Set("Authorization", authz)
	resp, err := pushHTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("届け先が %d と答えました", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// PushSubscribers は、通知を受ける端末を持つ利用者の名前です（拡張が、だれに送るかを選ぶのに使う）。
func PushSubscribers() []string {
	pushMu.Lock()
	defer pushMu.Unlock()
	var out []string
	for u, subs := range loadPushSubs() {
		if len(subs) > 0 {
			out = append(out, u)
		}
	}
	return out
}

// ── 口 ────────────────────────────────────────────────────────────────

// PushKeyAPIHandler は GET /api/push/key です。
func PushKeyAPIHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := GateJSONGet(w, r); !ok {
		return
	}
	k, err := loadVAPID()
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	pub, err := vapidPublic(k)
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, map[string]any{"success": true, "key": pub})
}

// PushSubscribeAPIHandler は POST /api/push/subscribe です（同じ endpoint は1つにまとめる）。
func PushSubscribeAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	u, err := url.Parse(strings.TrimSpace(req.Endpoint))
	if err != nil || u.Scheme != "https" || u.Host == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		JSONFail(w, http.StatusBadRequest, "受け口が読めません（https の endpoint と keys が要ります）")
		return
	}
	pushMu.Lock()
	defer pushMu.Unlock()
	all := loadPushSubs()
	// 同じ端末（endpoint）は、どの利用者の下からも外してから入れ直す（端末を別の人が使い始めたとき）。
	for name, subs := range all {
		var keep []pushSubscription
		for _, s := range subs {
			if s.Endpoint != req.Endpoint {
				keep = append(keep, s)
			}
		}
		all[name] = keep
	}
	all[user.Username] = append(all[user.Username], pushSubscription{Endpoint: req.Endpoint, P256dh: req.Keys.P256dh, Auth: req.Keys.Auth,
		Added: time.Now().Format(time.RFC3339), Agent: r.UserAgent()})
	if err := savePushSubs(all); err != nil {
		JSONFail(w, http.StatusInternalServerError, "覚えられません: "+err.Error())
		return
	}
	auth.Audit(user.Username, "push.subscribe", u.Host)
	WriteJSON(w, map[string]any{"success": true})
}

// PushUnsubscribeAPIHandler は POST /api/push/unsubscribe です。
func PushUnsubscribeAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	pushMu.Lock()
	defer pushMu.Unlock()
	all := loadPushSubs()
	var keep []pushSubscription
	for _, s := range all[user.Username] {
		if s.Endpoint != req.Endpoint {
			keep = append(keep, s)
		}
	}
	all[user.Username] = keep
	if err := savePushSubs(all); err != nil {
		JSONFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, map[string]any{"success": true})
}

// PushTestAPIHandler は POST /api/push/test です——自分の端末へ試しの通知を送ります。
func PushTestAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := GateJSONPost(w, r)
	if !ok {
		return
	}
	n := SendPush(user.Username, PushMessage{Title: "w-cms", Body: "通知の試しです（この端末で受けられます）", URL: "/", Tag: "test"})
	WriteJSON(w, map[string]any{"success": true, "sent": n})
}

// ServiceWorkerHandler は GET /sw.js です（通知を受けて出す画面の側の働き・assets/sw.js）。
// ⚠ 置き場は「/」の直下——ブラウザは、置いた場所より上のページには働かせないため。
func ServiceWorkerHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, filepath.Join("assets", "sw.js"))
}
