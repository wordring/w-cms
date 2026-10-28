// w-cms-dial は、業務サーバーにつないだ USB モデムで電話をかける道具です（2026-10-28）。
//
// 会社の電話の線（ND6 のつながる線）をモデムと電話機に分けてつなぎ、w-cms の「☎ かける」でモデムが番号を回します。
// 会社の電話機の受話器を上げたら、モデムは線から離れ、そのまま電話機で話します。
//
// > 2026-10-28 利用者:「会社のパソコンから書ける場合は3、スマホかける場合は1は駄目ですか？」
// > （3 ＝ 業務サーバーのモデムで番号を回し、会社の電話機で話す・1 ＝ スマホをホームゲートウェイの内線にする）
//
// w-cms とは Unix ソケットだけでやりとりします（社内の網には出ない）:
//
//	POST /dial    {"number":"0312345678"} … 番号を回す（回し終えたら答える・線は -hold のあいだつかむ）
//	POST /release                         … 受話器を上げたので、線から離れる
//	GET  /state                           … {"busy":bool,"number":"…"}
//
// 番号の確かめ（だれがかけてよいか・料金の高い番号）は w-cms がします。ここは数字6〜15桁だけを受けます。
// 外部のライブラリは使いません（シリアルの設定は stty に任せる——w-cms-phone と同じ）。
//
// 使い方: w-cms-dial -dev /dev/wcms-dialer -sock /run/w-cms-dial/dial.sock -hold 30s
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	dev := flag.String("dev", "/dev/wcms-dialer", "モデムのポート")
	sock := flag.String("sock", "/run/w-cms-dial/dial.sock", "w-cms から頼まれる Unix ソケット")
	hold := flag.Duration("hold", 30*time.Second, "回し終えてから線をつかんでいる長さ（このあいだに受話器を上げる）")
	flag.Parse()

	d := &dialer{open: func() (port, error) { return openModem(*dev) }, hold: *hold, timeout: 5 * time.Second}
	_ = os.Remove(*sock)
	ln, err := net.Listen("unix", *sock)
	if err != nil {
		log.Fatalf("ソケットを開けません: %v", err)
	}
	// w-cms（同じ利用者・同じ組）だけがつなげる。
	if err := os.Chmod(*sock, 0o660); err != nil {
		log.Fatalf("ソケットの権限を変えられません: %v", err)
	}
	log.Printf("%s で待っています（モデム %s・線をつかむのは %s まで）", *sock, *dev, *hold)
	log.Fatal(http.Serve(ln, handler(d)))
}

// handler は Unix ソケットの口です。
func handler(d *dialer) http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/dial", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			reply(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST だけです"})
			return
		}
		var req struct {
			Number string `json:"number"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil || !digitsOnly(req.Number) {
			reply(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "番号は数字6〜15桁で渡してください"})
			return
		}
		if err := d.Dial(req.Number); err != nil {
			code := http.StatusServiceUnavailable
			if err == errBusy {
				code = http.StatusConflict
			}
			log.Printf("かけられません（%s）: %v", req.Number, err)
			reply(w, code, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		log.Printf("回しました: %s", req.Number)
		reply(w, http.StatusOK, map[string]any{"ok": true, "hold_sec": int(d.hold.Seconds())})
	})
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			reply(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST だけです"})
			return
		}
		d.Release()
		reply(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		busy, number := d.State()
		reply(w, http.StatusOK, map[string]any{"ok": true, "busy": busy, "number": number})
	})
	return mux
}

// digitsOnly は、数字だけの6〜15桁かです（モデムの命令に人の書いた文字を混ぜないため）。
func digitsOnly(s string) bool {
	if len(s) < 6 || len(s) > 15 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// openModem はモデムのポートを開きます（設定は stty・USB のモデムは速さを見ないが、命令の形に合わせる）。
func openModem(dev string) (port, error) {
	if out, err := exec.Command("stty", "-F", dev, "9600", "cs8", "-parenb", "-cstopb", "raw", "-echo", "clocal").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("stty %s: %v %s", dev, err, strings.TrimSpace(string(out)))
	}
	return os.OpenFile(dev, os.O_RDWR, 0)
}
