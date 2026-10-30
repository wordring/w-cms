// w-cms-label は、業務サーバーにつないだブラザーのラベルプリンター QL-800 で札を刷る道具です（2026-10-30）。
//
// > 2026-10-23 利用者:「ubuntuにブラザーql800を繋げて現品票を印刷しようと思います。納品用と塗装、メッキ等の表面処理用途なります。」
//
// w-cms は守りのため機械の部品に触れません（deploy/server/w-cms.service の PrivateDevices）。そこで、電話をかける道具
// （cmd/w-cms-dial）と同じく、プリンターの口だけを持つこの道具が刷り、w-cms とは Unix ソケットだけでやりとりします:
//
//	GET  /state … {"connected":bool,"width_mm":62,"dots":696,"error":"…"} いま刷れるか（電源・ロール・ふた）
//	POST /print {"pages":[{"width":696,"height":1181,"bits":"<base64>"}]} … 札を1枚ずつ刷って切る
//
// 札の中身（何を刷るか）は知りません。w-cms が白黒の絵にして渡します（ext/label）。ここは絵をプリンターの命令にするだけです。
// 外部のライブラリは使いません。命令の決まりは raster.go です。
//
// 使い方: w-cms-label -dev /dev/usb/lp0 -sock /run/w-cms-label/label.sock
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	errOff      = errors.New("QL-800 の電源が切れているか、USB が抜けています")
	errNoAnswer = errors.New("プリンターが答えません")
	errBusy     = errors.New("ほかの札を刷っています")
)

// device はプリンターの口です（device_linux.go）。試験で差し替える。
type device interface {
	Write(b []byte, timeout time.Duration) error
	Read(timeout time.Duration) ([]byte, error)
	Close() error
}

// printer は、1度に1つの仕事だけを口に流します。
type printer struct {
	open func() (device, error)
	mu   sync.Mutex
}

// stateResult は GET /state の答えです。
type stateResult struct {
	Connected bool   `json:"connected"`
	WidthMM   int    `json:"width_mm,omitempty"`
	Dots      int    `json:"dots,omitempty"`
	Error     string `json:"error,omitempty"`
}

// begin は口を開き、残っている答えを読み捨て、初期化して状態を読みます。
func (p *printer) begin() (device, status, error) {
	d, err := p.open()
	if err != nil {
		return nil, status{}, err
	}
	for { // 前の仕事の答えが残っていれば読み捨てる
		if _, err := d.Read(100 * time.Millisecond); err != nil {
			break
		}
	}
	if err := d.Write(startCommands(), 5*time.Second); err != nil {
		d.Close()
		return nil, status{}, err
	}
	b, err := d.Read(3 * time.Second)
	if err != nil {
		d.Close()
		return nil, status{}, err
	}
	st, err := parseStatus(b)
	if err != nil {
		d.Close()
		return nil, status{}, err
	}
	return d, st, nil
}

func (p *printer) State() stateResult {
	if !p.mu.TryLock() {
		return stateResult{Connected: true, Error: errBusy.Error()}
	}
	defer p.mu.Unlock()
	d, st, err := p.begin()
	if err != nil {
		return stateResult{Connected: !errors.Is(err, errOff), Error: err.Error()}
	}
	d.Close()
	t, err := st.tapeOf()
	if err != nil {
		return stateResult{Connected: true, WidthMM: st.WidthMM, Error: err.Error()}
	}
	return stateResult{Connected: true, WidthMM: st.WidthMM, Dots: t.dots}
}

// Print は札を1枚ずつ刷ります。刷り終えた枚数を返します（途中で止まれば、そこまでの数と誤り）。
func (p *printer) Print(pages []page) (int, error) {
	if !p.mu.TryLock() {
		return 0, errBusy
	}
	defer p.mu.Unlock()
	d, st, err := p.begin()
	if err != nil {
		return 0, err
	}
	defer d.Close()
	t, err := st.tapeOf()
	if err != nil {
		return 0, err
	}
	for _, pg := range pages {
		if err := pg.check(t); err != nil {
			return 0, err
		}
	}
	for i, pg := range pages {
		if err := d.Write(pageCommands(pg, t, st.WidthMM, i == 0, i == len(pages)-1), 20*time.Second); err != nil {
			return i, err
		}
		if err := waitPrinted(d); err != nil {
			return i, err
		}
	}
	return len(pages), nil
}

// waitPrinted は「刷り終わった」の答えを待ちます（段の変わり目・知らせは読み飛ばす）。
func waitPrinted(d device) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		b, err := d.Read(15 * time.Second)
		if err != nil {
			return err
		}
		st, err := parseStatus(b)
		if err != nil {
			return err
		}
		switch st.Type {
		case statusPrinted:
			return nil
		case statusError:
			if t := st.errorText(); t != "" {
				return errors.New(t)
			}
			return errors.New("プリンターが誤りを返しました")
		case statusOff:
			return errOff
		}
	}
	return errors.New("刷り終わったという答えが来ません")
}

func main() {
	dev := flag.String("dev", "/dev/usb/lp0", "プリンターの口")
	sock := flag.String("sock", "/run/w-cms-label/label.sock", "w-cms から頼まれる Unix ソケット")
	flag.Parse()

	p := &printer{open: func() (device, error) { return openDevice(*dev) }}
	_ = os.Remove(*sock)
	ln, err := net.Listen("unix", *sock)
	if err != nil {
		log.Fatalf("ソケットを開けません: %v", err)
	}
	// w-cms（組 wcms-label に入れた本番だけ）がつなげる。
	if err := os.Chmod(*sock, 0o660); err != nil {
		log.Fatalf("ソケットの権限を変えられません: %v", err)
	}
	log.Printf("%s で待っています（プリンター %s）", *sock, *dev)
	log.Fatal(http.Serve(ln, handler(p)))
}

// printRequest は POST /print の中身です。
type printRequest struct {
	Pages []struct {
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Bits   string `json:"bits"`
	} `json:"pages"`
}

const maxPages = 50

// handler は Unix ソケットの口です。
func handler(p *printer) http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			reply(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "GET だけです"})
			return
		}
		reply(w, http.StatusOK, p.State())
	})
	mux.HandleFunc("/print", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			reply(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "POST だけです"})
			return
		}
		var req printRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&req); err != nil {
			reply(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "頼みの中身が読めません"})
			return
		}
		if len(req.Pages) == 0 || len(req.Pages) > maxPages {
			reply(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "札は1〜50枚で頼んでください"})
			return
		}
		pages := make([]page, 0, len(req.Pages))
		for _, rp := range req.Pages {
			bits, err := base64.StdEncoding.DecodeString(rp.Bits)
			if err != nil {
				reply(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "札の絵が読めません"})
				return
			}
			pages = append(pages, page{Width: rp.Width, Height: rp.Height, Bits: bits})
		}
		n, err := p.Print(pages)
		if err != nil {
			code := http.StatusServiceUnavailable
			switch {
			case errors.Is(err, errBusy):
				code = http.StatusConflict
			case errors.Is(err, errOff):
				code = http.StatusServiceUnavailable
			}
			log.Printf("刷れません（%d/%d 枚で止まった）: %v", n, len(pages), err)
			reply(w, code, map[string]any{"ok": false, "printed": n, "error": err.Error()})
			return
		}
		log.Printf("%d 枚を刷りました", n)
		reply(w, http.StatusOK, map[string]any{"ok": true, "printed": n})
	})
	return mux
}
