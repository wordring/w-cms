// Command w-cms-phone は、電話の着信を w-cms の通信箱へ記録する小さな常駐の道具です（2026-10-21 夜・
// docs/考察/【考察】通信箱の改善.md §7.3）。
//
// > 2026-10-21 利用者:「電話装置はアロハND6でパソコンとつなぎます。パソコンとの接続はUSBケーブルとなります。USBの電源断など
// > 対策も必要と思います。このプログラムもお願いします」
// > 2026-10-21 利用者:「出た着信だけ記録にします。…電話に出たら素早くメモを作って、しかもユーザーがアクセスする手段を提供して
// > 欲しいです。」
//
// アロハ ND6（USB の仮想 COM ポート・9600bps・7ビット・偶数パリティ・ストップ1・RTS/CTS）の電文を読み、
//
//   - 着信（番号）を覚えておき、受話器を上げた（OF）ら w-cms の POST /api/machine/call {event:"answered"} を呼ぶ
//     → w-cms がチャネル電話の記録を作る（相手は電話番号で引く）。-open なら、その記録をこのパソコンのブラウザで開く。
//   - 受話器を置いた（ON）ら {event:"hangup"} で「通話終了」を書く。
//   - 出る前に切れた（未応答 UR）着信は、記録にしない（利用者の答え「出た着信だけ記録にします」）。
//
// USB の電源が切れたときの対策:
//   - ポートが無くなったり読めなくなったりしたら、閉じて、数秒おきに開き直し続けます。
//   - Linux では、1分ごとに「CON」を送り、「ALOHA」が2回続けて返らなければ開き直します（Windows は読み書きが同時にできないので
//     しない——抜けたときは読み込みの誤りで分かる）。
//   - Linux の udev の決まり・systemd の設定は deploy/phone/ にあります。
//
// 使い方:
//
//	WCMS_MACHINE_TOKEN=<鍵> w-cms-phone -dev /dev/aloha-nd6 -base https://localhost:8443 [-open] [-insecure]
//	（Windows なら -dev COM3）
//
// 鍵は w-cms の WCMS_MACHINE_TOKEN と同じもの。別の機械から呼ぶなら、w-cms の側の WCMS_MACHINE_ALLOW にこの機械のアドレスを足す。
// 外部のライブラリは使いません（シリアルの設定は Linux の stty・Windows の mode に任せる）。
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ringFresh は、着信の番号を「いまの電話」とみなす長さです（それより前の着信の番号は使わない）。
const ringFresh = 5 * time.Minute

// caller は電文の流れから w-cms へ知らせる部分です（試験では send を差し替える）。
type caller struct {
	now  func() time.Time
	send func(event, number string, at time.Time, pageID string) (string, error)
	open func(pageID string) // 出た着信の記録を開く（nil なら開かない）

	mu         sync.Mutex
	lastRing   string
	lastRingAt time.Time
	current    string // いまの通話の記録（answered で作ったページ）
}

// handle は電文1つを処理します。
func (c *caller) handle(e event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch e.Kind {
	case evRing:
		c.lastRing, c.lastRingAt = e.Number, c.now()
		log.Printf("着信: %s", e.Number)
	case evMissed:
		log.Printf("未応答（記録しません）: %s", c.lastRing)
		c.lastRing = ""
	case evOffHook:
		number := ""
		if c.lastRing != "" && c.now().Sub(c.lastRingAt) <= ringFresh {
			number = c.lastRing
		}
		c.lastRing = ""
		id, err := c.send("answered", number, e.At, "")
		if err != nil {
			log.Printf("⚠ 記録を作れません（%s）: %v", number, err)
			return
		}
		c.current = id
		log.Printf("出た着信を記録しました: /%s（%s）", id, number)
		if c.open != nil && id != "" {
			go c.open(id)
		}
	case evOnHook:
		if c.current == "" {
			return
		}
		if _, err := c.send("hangup", "", e.At, c.current); err != nil {
			log.Printf("⚠ 通話終了を書けません（/%s）: %v", c.current, err)
		}
		c.current = ""
	}
}

// client は w-cms の機械専用の入口を呼びます。
type client struct {
	base  string
	token string
	http  *http.Client
}

// call は POST /api/machine/call を呼び、作った記録のページ番号を返します。
func (cl *client) call(ev, number string, at time.Time, pageID string) (string, error) {
	body, _ := json.Marshal(map[string]string{"event": ev, "number": number, "at": at.Format(time.RFC3339), "page_id": pageID})
	req, err := http.NewRequest("POST", strings.TrimRight(cl.base, "/")+"/api/machine/call", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cl.token)
	resp, err := cl.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		PageID string `json:"page_id"`
	}
	json.Unmarshal(b, &out)
	return out.PageID, nil
}

// openBrowser は記録をこのパソコンの既定のブラウザで開きます。
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("⚠ ブラウザを開けません: %v", err)
	}
}

// openPort は ND6 のポートを開きます（設定は Linux なら stty、Windows なら mode）。
func openPort(dev string) (*os.File, error) {
	switch runtime.GOOS {
	case "windows":
		name := strings.TrimPrefix(strings.ToUpper(dev), `\\.\`)
		if out, err := exec.Command("mode", name+":", "BAUD=9600", "PARITY=E", "DATA=7", "STOP=1", "RTS=HS", "OCTS=ON", "TO=OFF").CombinedOutput(); err != nil {
			return nil, fmt.Errorf("mode %s: %v %s", name, err, strings.TrimSpace(string(out)))
		}
		return os.OpenFile(`\\.\`+name, os.O_RDWR, 0)
	default:
		if out, err := exec.Command("stty", "-F", dev, "9600", "cs7", "parenb", "-parodd", "-cstopb", "crtscts", "raw", "-echo", "clocal").CombinedOutput(); err != nil {
			return nil, fmt.Errorf("stty %s: %v %s", dev, err, strings.TrimSpace(string(out)))
		}
		return os.OpenFile(dev, os.O_RDWR, 0)
	}
}

// runPort はポートを1回開いて、読めなくなるまで電文を処理します（読めなくなったら戻る——呼び手が開き直す）。
func runPort(dev string, c *caller) error {
	f, err := openPort(dev)
	if err != nil {
		return err
	}
	defer f.Close()
	log.Printf("%s を開きました", dev)
	var alive sync.Mutex
	lastAloha := time.Now()
	done := make(chan struct{})
	defer close(done)
	// 接続の確認（Linux だけ）: 1分ごとに CON を送り、2分以上 ALOHA が返らなければポートを閉じて開き直させる。
	if runtime.GOOS != "windows" {
		go func() {
			t := time.NewTicker(time.Minute)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					alive.Lock()
					stale := time.Since(lastAloha) > 2*time.Minute+10*time.Second
					alive.Unlock()
					if stale {
						log.Printf("⚠ %s から返事がありません——開き直します", dev)
						f.Close()
						return
					}
					if _, err := f.Write([]byte{stx, 'C', 'O', 'N', etx}); err != nil {
						f.Close()
						return
					}
				}
			}
		}()
	}
	var fr frameReader
	buf := make([]byte, 256)
	for {
		n, err := f.Read(buf)
		for _, body := range fr.feed(buf[:n]) {
			e := parseFrame(body, time.Now())
			if e.Kind == evAloha {
				alive.Lock()
				lastAloha = time.Now()
				alive.Unlock()
				continue
			}
			c.handle(e)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("ポートが閉じました")
			}
			return err
		}
	}
}

func main() {
	dev := flag.String("dev", "/dev/aloha-nd6", "ND6 のポート（Linux は /dev/ttyUSB0 か udev で付けた名前・Windows は COM3 など）")
	base := flag.String("base", "https://localhost:8443", "w-cms の場所")
	open := flag.Bool("open", false, "出た着信の記録を、このパソコンのブラウザで開く（電話機のそばのパソコンで動かすとき）")
	insecure := flag.Bool("insecure", false, "w-cms の証明書を確かめない（自分で作った証明書のとき・同じ機械か社内の網だけで使う）")
	flag.Parse()
	token := strings.TrimSpace(os.Getenv("WCMS_MACHINE_TOKEN"))
	if token == "" {
		log.Fatal("環境変数 WCMS_MACHINE_TOKEN（w-cms と同じ鍵）がありません")
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: *insecure}}
	cl := &client{base: *base, token: token, http: &http.Client{Timeout: 20 * time.Second, Transport: tr}}
	c := &caller{now: time.Now, send: cl.call}
	if *open {
		c.open = func(id string) { openBrowser(strings.TrimRight(*base, "/") + "/" + id) }
	}
	// 開き直しの間隔: 3秒から倍々に30秒まで。しばらく（1分以上）読めていたあとに落ちたら、また3秒から。
	wait := 3 * time.Second
	for {
		started := time.Now()
		err := runPort(*dev, c)
		if time.Since(started) > time.Minute {
			wait = 3 * time.Second
		}
		log.Printf("⚠ %s: %v——%v 後に開き直します", *dev, err, wait)
		time.Sleep(wait)
		if wait < 30*time.Second {
			wait *= 2
		}
	}
}
