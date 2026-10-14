package main

import (
	"net"
	"os"
	"strings"
)

// 待ち受けるアドレス（`WCMS_ADDR`・2026-10-14）
//
// 【要求】運用 §3 の表: `WCMS_ADDR` は待受アドレスで、既定は `:8080`。リバースプロキシと同居するなら
// ローカルへ束縛できます（例 `127.0.0.1:8080`）。1台に2つの w-cms を同居させるときは、ポートを分けます（§4）。
//
// ⚠ **渡さなければ、いままでと同じ**——平文は `:8080`、TLS のとき（証明書と鍵がそろったとき）は `:8443`。
// 渡したら、TLS のときもその値で待ち受けます。

// addrEnv は待受アドレスを渡す環境変数の名前です。
const addrEnv = "WCMS_ADDR"

// listenAddr は待ち受けるアドレスです（tls は証明書と鍵がそろっているか）。
func listenAddr(tls bool) string {
	if a := strings.TrimSpace(os.Getenv(addrEnv)); a != "" {
		return a
	}
	if tls {
		return ":8443"
	}
	return ":8080"
}

// listenURL はログに出す「開くURL」です（例 `:8443` → `https://localhost:8443`）。
// ホストを書いていない・全部の口（0.0.0.0・::）なら localhost と出します。読めない形はそのまま添えます
// （待ち受けに失敗すれば、起動の次の行がそう言います）。
func listenURL(addr string, tls bool) string {
	scheme := "http"
	if tls {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return scheme + "://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}
