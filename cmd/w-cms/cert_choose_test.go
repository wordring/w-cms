package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 証明書を名前で使い分ける（2026-10-26・cert_reload.go の certChooser）。

// namedCert は、名前 name だけを持つ自己署名の証明書と鍵を dir に書き、その場所を返します。
func namedCert(t *testing.T, dir, name string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cp, kp := filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")
	if err := os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0600); err != nil {
		t.Fatal(err)
	}
	return cp, kp
}

// TestCertChooserPicksByServerName は、
//   - 求められた名前が公開の名前の証明書に合えばそれを、住所で開いた・名前を送らない・ほかの名前ならいままでの証明書を渡す
//   - 公開の名前の証明書が起動のときに無くても止まらず、写されてきたら（間隔を過ぎた次の接続から）使う
//
// ことを固定します。
func TestCertChooserPicksByServerName(t *testing.T) {
	src := t.TempDir()
	defC, defK := namedCert(t, src, "wcms-server")
	pubSrcC, pubSrcK := namedCert(t, src, "sm.example.jp")
	def, err := newCertReloader(defC, defK)
	if err != nil {
		t.Fatal(err)
	}
	live := t.TempDir()
	pubC, pubK := filepath.Join(live, "public.crt"), filepath.Join(live, "public.key")
	pub := newOptionalCertReloader(pubC, pubK) // まだ写されていない
	clock := time.Now()
	pub.now = func() time.Time { return clock }
	c := &certChooser{def: def, pub: pub}
	leafOf := func(name string) []byte {
		t.Helper()
		got, err := c.GetCertificate(&tls.ClientHelloInfo{ServerName: name})
		if err != nil || got == nil {
			t.Fatalf("%q で証明書が返りません: %v", name, err)
		}
		return got.Certificate[0]
	}
	defLeaf := leafOf("")
	if !bytes.Equal(leafOf("sm.example.jp"), defLeaf) {
		t.Errorf("公開の名前の証明書がまだ無いのに、いままでの証明書以外を渡しました")
	}

	// 写されてきた。
	copyFile(t, pubSrcC, pubC, clock)
	copyFile(t, pubSrcK, pubK, clock)
	clock = clock.Add(checkEvery + time.Second)
	pubLeaf := leafOf("sm.example.jp")
	if bytes.Equal(pubLeaf, defLeaf) {
		t.Fatalf("⚠ 公開の名前で来たのに、公開の名前の証明書を渡しません")
	}
	for _, name := range []string{"", "192.168.1.100", "wcms-server", "other.example.jp"} {
		if !bytes.Equal(leafOf(name), defLeaf) {
			t.Errorf("⚠ %q で来たのに、いままでの証明書を渡しません", name)
		}
	}
}
