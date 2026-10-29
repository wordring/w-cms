package main

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestTLSConfigTrustsGivenCA は、-ca に渡した証明書で w-cms の自分で作った証明書を確かめて繋げること・渡さなければ
// 繋がらない（黙って確かめない形に落ちない）こと・読めないファイルは誤りにすることを固定します（2026-10-29）。
func TestTLSConfigTrustsGivenCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "local-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0644); err != nil {
		t.Fatal(err)
	}
	get := func(caFile string) error {
		conf, err := tlsConfig(caFile, false)
		if err != nil {
			return err
		}
		res, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: conf}}).Get(srv.URL)
		if err == nil {
			res.Body.Close()
		}
		return err
	}
	if err := get(ca); err != nil {
		t.Errorf("渡した CA で繋がりません: %v", err)
	}
	if err := get(""); err == nil {
		t.Error("CA を渡さないのに、自分で作った証明書の相手に繋がりました（確かめていない）")
	}
	if _, err := tlsConfig(filepath.Join(t.TempDir(), "無い.pem"), false); err == nil {
		t.Error("読めない CA のファイルが誤りになりません")
	}
	empty := filepath.Join(t.TempDir(), "空.pem")
	os.WriteFile(empty, []byte("証明書ではない"), 0644)
	if _, err := tlsConfig(empty, false); err == nil {
		t.Error("証明書の無いファイルが誤りになりません")
	}
}
