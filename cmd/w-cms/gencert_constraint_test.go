package main

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 自分で作る証明書は、この機械の名前とアドレスしか名乗れない（鍵が漏れても、ほかの名前の証明書を作れない・2026-10-22 の見直し）。
func TestGencertIsNameConstrained(t *testing.T) {
	certFile, _, err := generateSelfSignedCert(filepath.Join(t.TempDir(), "tls"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(certFile)
	blk, _ := pem.Decode(b)
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !c.PermittedDNSDomainsCritical || len(c.PermittedDNSDomains) == 0 || !c.MaxPathLenZero {
		t.Fatalf("⚠ 名前の制約がありません: %+v", c.PermittedDNSDomains)
	}
	roots := x509.NewCertPool()
	roots.AddCert(c)
	if _, err := c.Verify(x509.VerifyOptions{Roots: roots, DNSName: "localhost", CurrentTime: time.Now()}); err != nil {
		t.Errorf("自分の名前（localhost）で確かめられません: %v", err)
	}
}
