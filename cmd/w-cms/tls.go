package main

// ─────────────────────────────────────────────────────────────────────────
// HTTPS（2026-09-07）
//
// **WebDAV のために要る**、というのが直接のきっかけです。Windows の WebDAV
// クライアントは `BasicAuthLevel=1`（既定）のせいで**平文HTTPでは合言葉を送らず**、
// `OPTIONS` を1回投げて諦めます（エラー67・認証の失敗としては報告されない。実測は
// [docs/【考察】添付をローカルアプリで編集する.md] §6a）。逃げ道を3つ当たって、
// 残ったのが HTTPS でした——Digest は argon2id しか持たないので作れず、
// レジストリ変更は各PCの設定で勧められない。
//
// もっとも、**HTTPS はもともと本番に要るもの**です。いまは Cookie が平文で流れており
// （`WCMS_SECURE_COOKIES=0` はそれを承知でローカル検証に使う抜け道）、社内LANでも
// 合言葉とセッションが素で流れているのは変わりません。
//
// ── 使い方 ──
//
//	go run ./cmd/w-cms -gencert          # 自己署名の証明書を作る（data/tls/）
//	WCMS_TLS_CERT=data/tls/cert.pem \
//	WCMS_TLS_KEY=data/tls/key.pem  go run ./cmd/w-cms
//
// **証明書と鍵は `data/` に置きます**——環境ごとの持ち物で、Git には入りません
// （鍵をリポジトリに入れない、が第一。公開リポジトリなら尚更）。
//
// ── 自己署名で足りるのか ──
//
// **社内LANでは足ります。ただし各PCで「信頼」させる必要があります。** ブラウザも
// Windows の WebDAV クライアントも、信頼していない証明書は拒みます。
// 逃げ道は3つで、どれも一長一短:
//
//  1. **自己署名＋各PCの信頼された証明機関へ導入**（`-gencert` はこれ向け）。
//     1台ずつ1回だけ。`BasicAuthLevel` を変えるのと違い、**w-cms 以外の通信を
//     緩めません**——その1枚を信じるだけです。
//  2. **社内の証明機関**——台数が増えるならこちら。運用が要ります。
//  3. **公的な証明書**（Let's Encrypt 等）——公開ドメインが要るので、社内LANだけの
//     いまは当てはまりません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// tlsCertPath / tlsKeyPath は証明書と鍵の位置です（環境変数で差し替え）。
func tlsCertPath() string { return os.Getenv("WCMS_TLS_CERT") }
func tlsKeyPath() string  { return os.Getenv("WCMS_TLS_KEY") }

// defaultCertDir は `-gencert` の出力先です。
const defaultCertDir = "data/tls"

// generateSelfSignedCert は自己署名の証明書と鍵を作ります。
//
// **この機械の名前とIPを全部入れます**——`localhost` だけだと、LANの他のPCから
// `https://192.168.x.x:8443/` で来たときに名前が合わず拒まれます。実際の運用では
// 「サーバー機のIPで繋ぐ」のが普通なので、ここを取りこぼすと使えません。
//
// 鍵は P-256 の ECDSA。RSA より小さく速く、いまのブラウザとWindowsはどちらも扱えます。
func generateSelfSignedCert(dir string) (certFile, keyFile string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}

	hosts := []string{"localhost"}
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	if name, err := os.Hostname(); err == nil && name != "" {
		hosts = append(hosts, name)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				ips = append(ips, ipnet.IP)
			}
		}
	}

	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"w-cms"}, CommonName: hosts[len(hosts)-1]},
		NotBefore:    time.Now().Add(-time.Hour),
		// **2年**。短すぎると入れ直しが手間、長すぎると鍵が漏れたときに長く効きます。
		NotAfter:              time.Now().AddDate(2, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		// **自己署名を「信頼された証明機関」へ入れて使う**ので、CA として振る舞える
		// 必要があります（Windows はここが false の証明書をルートに入れても信じません）。
		IsCA:        true,
		DNSNames:    hosts,
		IPAddresses: ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}

	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := writePEM(certFile, "CERTIFICATE", der, 0o644); err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}
	// **鍵は 0600**。Windows では効きませんが（トークンの保管と同じ事情）、
	// 指定はしておきます——本番機のOSのアクセス制御が実質の守りです。
	if err := writePEM(keyFile, "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return "", "", err
	}
	return certFile, keyFile, nil
}

// writePEM は PEM で1件書き出します。
func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

// printCertInstructions は作ったあとに何をすればよいかを出します。
//
// **ここを出さないと詰みます**——自己署名は「作っただけ」では誰も信じないので、
// 各PCで信頼させる一手が要ることを、作った直後のいちばん見る場所で伝えます。
func printCertInstructions(certFile, keyFile string) {
	// **絶対パスで出します。** 管理者で開いた PowerShell は System32 から始まるので、
	// 相対パスを貼っても「ファイルが見つかりません」になります——1回きりの操作で
	// つまずかせないための一手間です。
	absCert := certFile
	if a, err := filepath.Abs(certFile); err == nil {
		absCert = a
	}
	fmt.Printf(`自己署名の証明書を作りました:
  証明書: %s
  鍵:     %s

起動:
  WCMS_TLS_CERT=%s WCMS_TLS_KEY=%s go run ./cmd/w-cms
  → https://localhost:8443/

⚠ このままではブラウザもWindowsも信じません。使う各PCで1度だけ信頼させます
  （PowerShell を「管理者として実行」して、**この1行を貼る**）:

  Import-Certificate -FilePath "%s" -CertStoreLocation Cert:\LocalMachine\Root

  信頼させると、WebDAV の割り当ても通るようになります（平文HTTPで Basic を送らない
  という Windows の既定は、HTTPS なら当てはまらないため）。
`, certFile, keyFile, certFile, keyFile, absCert)
}
