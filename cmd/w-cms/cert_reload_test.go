package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 証明書を読み直す（2026-10-19・cert_reload.go）。

func copyFile(t *testing.T, from, to string, mod time.Time) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(to, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// TestCertReloaderPicksUpNewFiles は、
//   - ファイルが替わったら、間隔を過ぎた次の接続から新しい証明書を渡す（間隔の内側では見ない）
//   - 証明書と鍵の組が合わない（写している途中）ときは古い証明書を使い続け、鍵も替わったら読み直す
//
// ことを固定します。
func TestCertReloaderPicksUpNewFiles(t *testing.T) {
	gen := func() (string, string) {
		c, k, err := generateSelfSignedCert(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return c, k
	}
	c1, k1 := gen()
	c2, k2 := gen()
	c3, k3 := gen()
	live := t.TempDir()
	cert, key := filepath.Join(live, "cert.pem"), filepath.Join(live, "key.pem")
	base := time.Now().Add(-time.Hour)
	copyFile(t, c1, cert, base)
	copyFile(t, k1, key, base)

	r, err := newCertReloader(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	r.now = func() time.Time { return clock }
	leaf := func() []byte {
		c, err := r.GetCertificate(nil)
		if err != nil || c == nil {
			t.Fatalf("証明書が返りません: %v", err)
		}
		return c.Certificate[0]
	}
	first := leaf()

	// 2枚目へ替える——間隔の内側ではまだ古いもの、過ぎたら新しいもの。
	copyFile(t, c2, cert, base.Add(time.Minute))
	copyFile(t, k2, key, base.Add(time.Minute))
	if !bytes.Equal(leaf(), first) {
		t.Errorf("⚠ 間隔の内側で読み直しました")
	}
	clock = clock.Add(checkEvery + time.Second)
	second := leaf()
	if bytes.Equal(second, first) {
		t.Fatalf("⚠ ファイルを替えたのに、新しい証明書を渡しません")
	}

	// 写している途中——証明書だけ3枚目、鍵は2枚目のまま。古い（2枚目の）証明書を使い続ける。
	copyFile(t, c3, cert, base.Add(2*time.Minute))
	clock = clock.Add(checkEvery + time.Second)
	if !bytes.Equal(leaf(), second) {
		t.Errorf("⚠ 組の合わない証明書で、いままでの証明書を手放しました")
	}
	// 鍵も3枚目になったら読み直す。
	copyFile(t, k3, key, base.Add(3*time.Minute))
	clock = clock.Add(checkEvery + time.Second)
	if third := leaf(); bytes.Equal(third, second) {
		t.Errorf("⚠ 鍵もそろったのに、3枚目を読み直しません")
	}
}
