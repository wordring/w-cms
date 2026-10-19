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
