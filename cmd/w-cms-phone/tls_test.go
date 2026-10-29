package main

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// TestClientPostsToMachineCall は、call と missed が同じ口（POST /api/machine/call・Bearer の鍵・JSON）へ送り、200 でなければ
// 「番号 中身」の誤りを返すことを固定します（2026-10-29 に2つの写しを post へ寄せた）。
func TestClientPostsToMachineCall(t *testing.T) {
	var got []map[string]string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/machine/call" || r.Header.Get("Authorization") != "Bearer 鍵" ||
			r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("送り方が違います: %s %s %q %q", r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"))
		}
		var m map[string]string
		json.NewDecoder(r.Body).Decode(&m)
		got = append(got, m)
		w.WriteHeader(status)
		w.Write([]byte(`{"page_id":"000123"}`))
	}))
	defer srv.Close()
	cl := &client{base: srv.URL + "/", token: "鍵", http: srv.Client()}
	at := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	if id, err := cl.call("answered", "0312345678", at, ""); err != nil || id != "000123" {
		t.Errorf("call: %q %v", id, err)
	}
	if err := cl.missed("0312345678", at, at.Add(time.Minute)); err != nil {
		t.Errorf("missed: %v", err)
	}
	if len(got) != 2 || got[0]["event"] != "answered" || got[1]["event"] != "missed" || got[1]["ended"] == "" {
		t.Errorf("送った中身が違います: %v", got)
	}
	status = http.StatusServiceUnavailable
	if err := cl.missed("1", at, at); err == nil || err.Error() != `503 {"page_id":"000123"}` {
		t.Errorf("200 でない答えの誤りが違います: %v", err)
	}
}
