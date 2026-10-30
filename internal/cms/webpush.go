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
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
	b, err := os.ReadFile(path)
	if err == nil {
		if blk, _ := pem.Decode(b); blk != nil {
			if k, err := x509.ParseECPrivateKey(blk.Bytes); err == nil {
				vapidKey.key = k
				return k, nil
			}
		}
		return nil, errors.New("送り手の鍵 " + path + " が読めません")
	}
	// 作るのは、ファイルが無いときだけ。ほかの読めない理由（ウイルス対策が一瞬つかんでいる など）で作り直すと、
	// 鍵が替わって、どの端末の受け口も黙って使えなくなる（2026-10-22 の見直し）。
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("送り手の鍵 %s が読めません: %w", path, err)
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
	// 書き切ってから置く（2026-10-30。欠けた鍵が残ると「在る」ので作り直さず、人が消すまで通知が全部止まった）。
	if err := page.WriteFileAtomic(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
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

// loadPushSubs は受け口の覚えを読みます。ファイルが無ければ空です。
// ⚠ 読めない・壊れているときは誤りを返す——空として読んで書き戻すと、ほかの人の受け口が全部消える（2026-10-22 の見直し）。
func loadPushSubs() (map[string][]pushSubscription, error) {
	out := map[string][]pushSubscription{}
	b, err := os.ReadFile(filepath.Join(pushDir(), "subscriptions.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("subscriptions.json が読めません: %w", err)
	}
	return out, nil
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
//
// 届け先（endpoint）は、利用者のブラウザが渡してくる URL です。サーバーがそこへ POST するので、社内の機械
// （このサーバー自身・ルーター・電話や FAX の機械）を指されても届かないようにします（2026-10-22 の見直し）:
//   - 内側のアドレス（ループバック・プライベート・リンクローカル など）へはつながない。
//   - 転送（3xx）には従わない（外の https から内側の http へ回されないため）。
var pushHTTP = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, Control: refuseInsideAddress}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// refuseInsideAddress は、つなぐ直前のアドレスが内側なら断ります（名前を引いたあとの IP で見る）。
func refuseInsideAddress(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("届け先のアドレスが読めません: %s", host)
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() || !ip.IsGlobalUnicast() {
		return fmt.Errorf("届け先が内側のアドレス（%s）です", ip)
	}
	if ip.Is4() && ip.As4()[0] == 100 && ip.As4()[1]&0xc0 == 64 { // 100.64.0.0/10（キャリアの内側）
		return fmt.Errorf("届け先が内側のアドレス（%s）です", ip)
	}
	return nil
}

// pushText は通知の題・本文を短くします（暗号のレコードは1つ・4096バイトまでなので、長い件名で溢れないように）。
func pushText(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max-1]) + "…"
}

// SendPush は利用者 username の全部の端末へ通知を送ります（届け先が「もう無い」と答えた受け口は捨てる）。送れた端末の数を返します。
func SendPush(username string, m PushMessage) int {
	pushMu.Lock()
	all, err := loadPushSubs()
	pushMu.Unlock()
	if err != nil {
		log.Printf("通知を送れません（受け口の覚え）: %v", err)
		return 0
	}
	subs := all[username]
	if len(subs) == 0 {
		return 0
	}
	k, err := loadVAPID()
	if err != nil {
		log.Printf("通知を送れません（送り手の鍵）: %v", err)
		return 0
	}
	m.Title, m.Body = pushText(m.Title, 80), pushText(m.Body, 300)
	payload, _ := json.Marshal(m)
	sent := 0
	var gone []string
	if len(subs) > pushMaxPerUser {
		subs = subs[len(subs)-pushMaxPerUser:] // 前に溜まった分も、新しい pushMaxPerUser 個だけに送る
	}
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
		defer pushMu.Unlock()
		all, err := loadPushSubs()
		if err != nil {
			log.Printf("もう無い受け口を捨てられません: %v", err)
			return sent
		}
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
		if err := savePushSubs(all); err != nil {
			log.Printf("もう無い受け口を捨てられません: %v", err)
		}
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
	all, err := loadPushSubs()
	if err != nil {
		log.Printf("通知を受ける人を読めません: %v", err)
		return nil
	}
	var out []string
	for u, subs := range all {
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

// pushMaxPerUser は、1人の利用者が持てる通知の受け口（端末）の数の上限です。
const pushMaxPerUser = 10

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
	req.Endpoint = strings.TrimSpace(req.Endpoint)
	u, err := url.Parse(req.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || len(req.Endpoint) > 2048 || !pushKeyLen(req.Keys.P256dh, 65) || !pushKeyLen(req.Keys.Auth, 16) {
		JSONFail(w, http.StatusBadRequest, "受け口が読めません（https の endpoint と keys が要ります）")
		return
	}
	pushMu.Lock()
	defer pushMu.Unlock()
	all, err := loadPushSubs()
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, "受け口の覚えが読めません: "+err.Error())
		return
	}
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
	// 1人の受け口は新しい pushMaxPerUser 個まで（2026-10-27 の見直し）——応えない住所を何千も登録すると、1件ごとに待つ送り方の
	// せいで、ほかの人への電話・FAX の通知が何時間も遅れた。端末を替えたときは、古いものから落とす。
	if subs := all[user.Username]; len(subs) > pushMaxPerUser {
		all[user.Username] = subs[len(subs)-pushMaxPerUser:]
	}
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
	all, err := loadPushSubs()
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, "受け口の覚えが読めません: "+err.Error())
		return
	}
	// endpoint を持っているのは、その端末のブラウザだけ——だれの下にあっても外す（共用のパソコンで、前の人の受け口が残らないように）。
	for name, subs := range all {
		var keep []pushSubscription
		for _, s := range subs {
			if s.Endpoint != req.Endpoint {
				keep = append(keep, s)
			}
		}
		all[name] = keep
	}
	if err := savePushSubs(all); err != nil {
		JSONFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	auth.Audit(user.Username, "push.unsubscribe", "")
	WriteJSON(w, map[string]any{"success": true})
}

// pushKeyLen は、base64url の鍵が n バイトに解けるかです。
func pushKeyLen(s string, n int) bool {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(s), "="))
	return err == nil && len(b) == n
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
