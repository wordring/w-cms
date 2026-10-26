package main

// 業務サーバーの中だけで開く入口の待ち受け（2026-10-26・internal/auth/local_socket.go）。
//
// WCMS_LOCAL_SOCKET（ファイルの場所）と WCMS_LOCAL_SOCKET_USER（この入口から来た要求を扱う w-cms の利用者）がそろったときだけ
// 開きます。片方だけなら、設定の書きかけなので起動を止めます（黙って開かない・黙って別の人で開く、をさせない）。
// ファイルは持ち主だけが読み書きできる形（0600）にします。置き場のフォルダも持ち主だけにする（systemd の RuntimeDirectoryMode=0700）。

import (
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strings"

	"w-cms/internal/auth"
)

// startLocalSocket は、設定があれば業務サーバーの中の入口を開き、裏で待ち受けます（無ければ何もしない）。
func startLocalSocket(h http.Handler) error {
	path := strings.TrimSpace(os.Getenv("WCMS_LOCAL_SOCKET"))
	user := strings.TrimSpace(os.Getenv("WCMS_LOCAL_SOCKET_USER"))
	if path == "" && user == "" {
		return nil
	}
	if path == "" || user == "" {
		return errors.New("業務サーバーの中の入口の設定が片方だけです（WCMS_LOCAL_SOCKET と WCMS_LOCAL_SOCKET_USER は両方要ります）")
	}
	// 前に起こしたときのファイルが残っていれば外す（ソケットのときだけ——ほかのファイルは消さない）。
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return errors.New("業務サーバーの中の入口の場所に、ソケットでないファイルがあります: " + path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0600); err != nil {
		ln.Close()
		return err
	}
	srv := newServer("", auth.LocalSocket(h, user))
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("業務サーバーの中の入口が止まりました: %v", err)
		}
	}()
	log.Println("業務サーバーの中の入口: " + path + "（利用者 " + user + "）")
	return nil
}
