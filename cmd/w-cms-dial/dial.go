package main

// モデムで電話をかける芯（2026-10-28）。シリアルから切り離してあるので、試験では偽のモデムを渡せます。
//
//	ATZ            … 初めの形に戻す
//	ATX3           … 発信音を待たずに回す（ひかり電話のアナログの口でも、発信音を聞き取れない型があるため）
//	ATDT<番号>;    … トーンで番号を回し、回し終えたら命令の形に戻る（; ——線はつかんだまま）
//	ATH0           … 線から離れる（受話器を上げた電話機が、そのまま話を続ける）
//
// 受話器を上げたかはモデムには分からない（並べてつないだ電話機の動きは見えない。ND6 も発信のときは知らせない）。
// そのため、人が「上げた」を押すか、決まった時間（hold）がたったら離れる。

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// port はモデムのポートです（*os.File か、試験の偽のモデム）。
type port interface {
	io.ReadWriteCloser
}

// dialer はモデムを1本持ち、1度に1本だけかけます。
type dialer struct {
	open    func() (port, error) // ポートを開く（かけるたびに開いて、離れたら閉じる）
	hold    time.Duration        // 回し終えてから線をつかんでいる長さ（このあいだに受話器を上げる）
	timeout time.Duration        // 1つの命令の答えを待つ長さ

	mu      sync.Mutex
	busy    bool
	number  string
	release chan struct{} // 「上げた」で閉じる
}

// errBusy は、ほかの電話をかけているあいだに頼まれたときの誤りです。
var errBusy = errors.New("いま、ほかの電話をかけています（少し待ってからかけ直してください）")

// Dial は number（数字だけ）を回し、回し終えたら戻ります。線はつかんだまま、hold のあいだか Release まで持ち、そのあと離れます。
func (d *dialer) Dial(number string) error {
	d.mu.Lock()
	if d.busy {
		d.mu.Unlock()
		return errBusy
	}
	d.busy, d.number, d.release = true, number, make(chan struct{})
	release := d.release
	d.mu.Unlock()
	done := func() {
		d.mu.Lock()
		d.busy, d.number, d.release = false, "", nil
		d.mu.Unlock()
	}

	p, err := d.open()
	if err != nil {
		done()
		return fmt.Errorf("モデムを開けません: %v", err)
	}
	r := bufio.NewReader(p)
	hangup := func() {
		_ = command(p, r, "ATH0", d.timeout)
		p.Close()
		done()
	}
	for _, c := range []string{"ATZ", "ATX3"} {
		if err := command(p, r, c, d.timeout); err != nil {
			p.Close()
			done()
			return fmt.Errorf("モデムが答えません（%s）: %v", c, err)
		}
	}
	// 回すのには番号の長さぶん時間がかかる（1桁 0.1 秒ほど）——答えはゆっくり待つ。
	if err := command(p, r, "ATDT"+number+";", d.timeout+time.Duration(len(number))*300*time.Millisecond); err != nil {
		hangup()
		return fmt.Errorf("番号を回せませんでした: %v", err)
	}
	go func() {
		select {
		case <-release:
		case <-time.After(d.hold):
		}
		hangup()
	}()
	return nil
}

// Release は、受話器を上げたので線から離れるよう頼みます（かけていなければ何もしない）。
func (d *dialer) Release() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.release != nil {
		select {
		case <-d.release:
		default:
			close(d.release)
		}
	}
}

// State はいまの様子です（かけている番号・かけていなければ空）。
func (d *dialer) State() (busy bool, number string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.busy, d.number
}

// command は命令を1つ送り、OK を待ちます。ERROR・NO DIALTONE・BUSY・NO CARRIER・NO ANSWER は誤り。
func command(p port, r *bufio.Reader, cmd string, timeout time.Duration) error {
	if _, err := io.WriteString(p, cmd+"\r"); err != nil {
		return err
	}
	type line struct {
		s   string
		err error
	}
	ch := make(chan line, 1)
	go func() {
		for {
			s, err := r.ReadString('\n')
			s = strings.TrimSpace(s)
			if err != nil {
				ch <- line{s, err}
				return
			}
			switch s {
			case "", cmd: // 空の行・こだま（ATE が切れていないとき）
				continue
			case "OK":
				ch <- line{s, nil}
				return
			case "ERROR", "NO DIALTONE", "BUSY", "NO CARRIER", "NO ANSWER":
				ch <- line{s, errors.New(s)}
				return
			}
		}
	}()
	select {
	case l := <-ch:
		return l.err
	case <-time.After(timeout):
		return errors.New("答えがありません")
	}
}
