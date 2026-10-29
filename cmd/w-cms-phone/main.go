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
//   - 出る前に切れた（未応答 UR）着信は、記録のページにしない（利用者の答え「出た着信だけ記録にします」）。2026-10-28 からは、
//     {event:"missed"} で w-cms の「出ていない着信」の履歴に残す（ページは人が通信箱で「記録にする」を押したら作る）。携帯へ転送された
//     電話も、ND6 には鳴りやんだように見えるため（利用者:「電話が鳴るだけでページを作るということではなく、なった記録だけして、
//     編集者がページにするか選択すればよいと思うのです」）。
//
// USB の電源が切れたときの対策:
//   - ポートが無くなったり読めなくなったりしたら、閉じて、数秒おきに開き直し続けます。
//   - Linux では、1分ごとに「CON」を送り、「ALOHA」が2回続けて返らなければ開き直します（Windows は読み書きが同時にできないので
//     しない——抜けたときは読み込みの誤りで分かる）。
//   - Linux の udev の決まり・systemd の設定は deploy/phone/ にあります。
//
// 使い方:
//
//	WCMS_MACHINE_TOKEN=<鍵> w-cms-phone -dev /dev/aloha-nd6 -base https://localhost:8443 [-open] [-ca <証明書>] [-insecure]
//	（Windows なら -dev COM3）
//
// 鍵は w-cms の WCMS_MACHINE_TOKEN と同じもの。別の機械から呼ぶなら、w-cms の側の WCMS_MACHINE_ALLOW にこの機械のアドレスを足す。
// 外部のライブラリは使いません（シリアルの設定は Linux の stty・Windows の mode に任せる）。
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
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
//
// 送るのは裏で（電文を読む流れを止めない）。w-cms が止まっている（入れ替え中など）あいだは、retry の間隔で
// 送り直す——それまでは、そのあいだの着信が消えていた（2026-10-22 の見直し）。
// 時刻は、機器の時計ではなくこのパソコンの時計で記録する（機器の時計は進み遅れ、電源が切れると戻る）。
type caller struct {
	now   func() time.Time
	send  func(event, number string, at time.Time, pageID string) (string, error)
	open  func(pageID string) // 出た着信の記録を開く（nil なら開かない）
	// missed は出ていない着信を知らせます（鳴り始めと鳴りやみ・nil なら知らせない）。
	missed func(number string, ringAt, endAt time.Time) error
	retry []time.Duration     // 送れなかったときに待つ間隔（尽きたら諦める・空なら1回だけ）

	mu         sync.Mutex
	lastRing   string
	lastRingAt time.Time
	current    *callRec // いまの通話（出たときに作る）
	wg         sync.WaitGroup
}

// callRec は通話1つです。id は w-cms の記録（作れたら入る）、ended は記録を作る前に電話を置いたか。
type callRec struct {
	id    string
	ended bool
	endAt time.Time
}

// deliver は f を、成功するまで retry の間隔で繰り返します（諦めたら最後の誤りを返す）。
func (c *caller) deliver(what string, f func() error) error {
	err := f()
	for _, d := range c.retry {
		if err == nil {
			return nil
		}
		log.Printf("⚠ %s を送れません（%v 後にもう一度）: %v", what, d, err)
		time.Sleep(d)
		err = f()
	}
	return err
}

// answer は出た着信の記録を作り、そのあいだに電話を置いていたら通話終了も書きます（裏で動く）。
func (c *caller) answer(rec *callRec, number string, at time.Time) {
	defer c.wg.Done()
	var id string
	err := c.deliver("出た着信", func() (err error) {
		id, err = c.send("answered", number, at, "")
		return err
	})
	if err != nil {
		log.Printf("⚠ 記録を作れません（%s）: %v", number, err)
		return
	}
	log.Printf("出た着信を記録しました: /%s（%s）", id, number)
	c.mu.Lock()
	rec.id = id
	ended, endAt := rec.ended, rec.endAt
	c.mu.Unlock()
	if c.open != nil && id != "" && !ended {
		go c.open(id)
	}
	if ended && id != "" {
		c.hangup(id, endAt)
	}
}

// hangup は通話終了を書きます。
func (c *caller) hangup(id string, at time.Time) {
	if err := c.deliver("通話終了", func() error {
		_, err := c.send("hangup", "", at, id)
		return err
	}); err != nil {
		log.Printf("⚠ 通話終了を書けません（/%s）: %v", id, err)
	}
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
		number, ringAt, endAt := c.lastRing, c.lastRingAt, c.now()
		if number == "" || endAt.Sub(ringAt) > ringFresh {
			number, ringAt = "", endAt // 着信の番号が来ていない・古い（鳴り始めは分からない）
		}
		c.lastRing = ""
		log.Printf("未応答（出ていない着信として残します）: %s", number)
		if c.missed != nil {
			c.wg.Add(1)
			go func() {
				defer c.wg.Done()
				if err := c.deliver("出ていない着信", func() error { return c.missed(number, ringAt, endAt) }); err != nil {
					log.Printf("⚠ 出ていない着信を残せません（%s）: %v", number, err)
				}
			}()
		}
	case evOffHook:
		number := ""
		if c.lastRing != "" && c.now().Sub(c.lastRingAt) <= ringFresh {
			number = c.lastRing
		}
		c.lastRing = ""
		rec := &callRec{}
		c.current = rec // 前の通話の「置いた」が来なかったときも、前の記録へは書かない
		c.wg.Add(1)
		go c.answer(rec, number, c.now())
	case evOnHook:
		rec := c.current
		c.current = nil
		if rec == nil {
			return
		}
		if rec.id == "" {
			rec.ended, rec.endAt = true, c.now() // 記録を作り終えたら書く（answer）
			return
		}
		c.wg.Add(1)
		go func(id string, at time.Time) {
			defer c.wg.Done()
			c.hangup(id, at)
		}(rec.id, c.now())
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

// missed は出ていない着信を POST /api/machine/call {event:"missed"} で知らせます（ページは作られない）。
func (cl *client) missed(number string, ringAt, endAt time.Time) error {
	body, _ := json.Marshal(map[string]string{"event": "missed", "number": number, "at": ringAt.Format(time.RFC3339), "ended": endAt.Format(time.RFC3339)})
	req, err := http.NewRequest("POST", strings.TrimRight(cl.base, "/")+"/api/machine/call", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cl.token)
	resp, err := cl.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
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
	ca := flag.String("ca", "", "w-cms の証明書を確かめるのに使う CA の証明書（PEM・自分で作った証明書のとき。業務サーバーでは /etc/w-cms/local-ca.pem）")
	insecure := flag.Bool("insecure", false, "w-cms の証明書を確かめない（古い置き方のため残す・-ca を使うこと）")
	flag.Parse()
	token := strings.TrimSpace(os.Getenv("WCMS_MACHINE_TOKEN"))
	if token == "" {
		log.Fatal("環境変数 WCMS_MACHINE_TOKEN（w-cms と同じ鍵）がありません")
	}
	tlsConf, err := tlsConfig(*ca, *insecure)
	if err != nil {
		log.Fatal(err)
	}
	tr := &http.Transport{TLSClientConfig: tlsConf}
	cl := &client{base: *base, token: token, http: &http.Client{Timeout: 20 * time.Second, Transport: tr}}
	c := &caller{now: time.Now, send: cl.call, missed: cl.missed,
		// 合わせて1時間ほど送り直す（サーバーの入れ替え・再起動を待てる長さ）。
		retry: []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute,
			5 * time.Minute, 10 * time.Minute, 10 * time.Minute, 15 * time.Minute, 15 * time.Minute}}
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

// tlsConfig は w-cms へ繋ぐときの TLS の設定です（2026-10-29）。ca があれば、この機械の信頼ストアにその CA を足して確かめる
// ——それまでは業務サーバーでも -insecure で、同じ機械の w-cms へ証明書を確かめずに機械の鍵を送っていた（【考察】コードの矛盾 §7.4）。
// 読めない・証明書が無いファイルは誤り（黙って確かめない形に落ちない）。
func tlsConfig(ca string, insecure bool) (*tls.Config, error) {
	if ca == "" {
		return &tls.Config{InsecureSkipVerify: insecure}, nil
	}
	b, err := os.ReadFile(ca)
	if err != nil {
		return nil, fmt.Errorf("CA の証明書を読めません: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(b) {
		return nil, fmt.Errorf("CA の証明書がありません: %s", ca)
	}
	return &tls.Config{RootCAs: pool}, nil
}
