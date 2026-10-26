package main

// ─────────────────────────────────────────────────────────────────────────
// 証明書を読み直す（2026-10-19・【考察】職場のサーバー機と社外からの接続 §3.3・§5）
//
// 外へ出すと、証明書は Let's Encrypt から取って 60日ごとに更新します（VPS で取ってサーバー機へ写す形・§3.3 の A）。
// それまでの w-cms は、証明書を起動のときに1回だけ読んでいたので、更新のたびに起こし直す必要がありました
// （知らせの口 SSE が一度切れる）。いまは、証明書と鍵のファイルが替わったら、次の接続から新しいものを使います。
//
//   - 見るのは接続（TLS のあいさつ）のとき。ファイルの更新時刻を見るのは、多くても checkEvery に1回。
//   - 証明書と鍵の組が合わない（写している途中——証明書だけ新しい など）ときは、古いものを使い続け、
//     ファイルがまた替わったら読み直す（読めないことを1回だけログに書く）。
//   - 起動のときに読めなければ、いままでどおり止める（黙って平文に落ちない——main.go）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"crypto/tls"
	"log"
	"os"
	"sync"
	"time"
)

// checkEvery はファイルの更新時刻を見る間隔です。
const checkEvery = 10 * time.Second

// certReloader は、証明書と鍵のファイルが替わったら読み直して渡す tls.Config.GetCertificate です。
type certReloader struct {
	certPath, keyPath string

	mu      sync.Mutex
	cert    *tls.Certificate
	loaded  [2]time.Time // 使っている証明書を読んだときの、証明書と鍵の更新時刻
	tried   [2]time.Time // 最後に読もうとしたときの更新時刻（読めなかった組を何度も読まない）
	checked time.Time    // 最後に更新時刻を見た時刻
	now     func() time.Time
}

// newCertReloader は証明書と鍵を読み、読み直す仕掛けを返します（読めなければ誤り）。
func newCertReloader(certPath, keyPath string) (*certReloader, error) {
	r := &certReloader{certPath: certPath, keyPath: keyPath, now: time.Now}
	mods, err := r.modTimes()
	if err != nil {
		return nil, err
	}
	c, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	r.cert, r.loaded, r.tried, r.checked = &c, mods, mods, r.now()
	return r, nil
}

func (r *certReloader) modTimes() ([2]time.Time, error) {
	var out [2]time.Time
	for i, p := range []string{r.certPath, r.keyPath} {
		st, err := os.Stat(p)
		if err != nil {
			return out, err
		}
		out[i] = st.ModTime()
	}
	return out, nil
}

// GetCertificate は tls.Config.GetCertificate です。
func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.now().Sub(r.checked) >= checkEvery {
		r.checked = r.now()
		r.reloadIfChanged()
	}
	return r.cert, nil
}

// reloadIfChanged はファイルが替わっていれば読み直します（錠は呼び手が持つ）。
func (r *certReloader) reloadIfChanged() {
	mods, err := r.modTimes()
	if err != nil || mods == r.loaded || mods == r.tried {
		return
	}
	r.tried = mods
	c, err := tls.LoadX509KeyPair(r.certPath, r.keyPath)
	if err != nil {
		log.Printf("証明書を読み直せません（いままでの証明書を使い続けます。ファイルがまた替わったら読み直します）: %v", err)
		return
	}
	r.cert, r.loaded = &c, mods
	log.Println("証明書を読み直しました: " + r.certPath)
}

// newOptionalCertReloader は、まだファイルが無い・読めないかもしれない証明書の読み直しです（2026-10-26）。
// 読めなければ証明書なし（nil）で始め、ファイルが替わったら（写されてきたら）読みます。
func newOptionalCertReloader(certPath, keyPath string) *certReloader {
	r := &certReloader{certPath: certPath, keyPath: keyPath, now: time.Now}
	r.checked = r.now()
	r.reloadIfChanged()
	if r.cert == nil {
		log.Printf("公開の名前の証明書はまだ読めません（写されてきたら読みます）: %s", certPath)
	}
	return r
}

// ─────────────────────────────────────────────────────────────────────────
// 証明書を名前で使い分ける（2026-10-26・【考察】職場のサーバー機と社外からの接続 §3.3・§3.4）
//
// 社内からは、いままでの住所（https://192.168.1.100:8443）でも、公開の名前（社内の名前引きで業務サーバーを指す）でも
// 開けるようにします。公開の名前の証明書（Let's Encrypt・VPS で取って写す——WCMS_TLS_PUBLIC_CERT / WCMS_TLS_PUBLIC_KEY）には
// 社内の住所が入らないので、1枚では両方を警告なしにできません。そこで、接続のあいさつで求められた名前（SNI）が公開の名前の
// 証明書に合えばそれを、合わなければ（住所で開いた・名前を送らない・VPS の中継の wcms-server）いままでの証明書を渡します。
// ─────────────────────────────────────────────────────────────────────────

// certChooser は、求められた名前で証明書を選ぶ tls.Config.GetCertificate です。pub は無くてよい（nil）。
type certChooser struct {
	def, pub *certReloader
}

// GetCertificate は tls.Config.GetCertificate です。
func (c *certChooser) GetCertificate(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if c.pub != nil && h != nil && h.ServerName != "" {
		if pc, _ := c.pub.GetCertificate(h); pc != nil && pc.Leaf != nil && pc.Leaf.VerifyHostname(h.ServerName) == nil {
			return pc, nil
		}
	}
	return c.def.GetCertificate(h)
}
