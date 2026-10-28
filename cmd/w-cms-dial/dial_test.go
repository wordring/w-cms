package main

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeModem は命令を憶え、決めた答えを返す偽のモデムです（answer が空なら OK）。
type fakeModem struct {
	mu       sync.Mutex
	commands []string
	answer   map[string]string // 命令の頭 → 答え（OK 以外を返すとき）
	closed   bool
	toModem  *io.PipeWriter
	fromHost *io.PipeReader
	toHost   *io.PipeWriter
	fromMdm  *io.PipeReader
}

func newFakeModem(answer map[string]string) *fakeModem {
	m := &fakeModem{answer: answer}
	m.fromHost, m.toModem = io.Pipe()
	m.fromMdm, m.toHost = io.Pipe()
	go func() {
		r := bufio.NewReader(m.fromHost)
		for {
			s, err := r.ReadString('\r')
			if err != nil {
				return
			}
			cmd := strings.TrimSpace(s)
			m.mu.Lock()
			m.commands = append(m.commands, cmd)
			ans := "OK"
			for head, a := range m.answer {
				if strings.HasPrefix(cmd, head) {
					ans = a
				}
			}
			m.mu.Unlock()
			io.WriteString(m.toHost, "\r\n"+ans+"\r\n")
		}
	}()
	return m
}

func (m *fakeModem) Read(p []byte) (int, error)  { return m.fromMdm.Read(p) }
func (m *fakeModem) Write(p []byte) (int, error) { return m.toModem.Write(p) }
func (m *fakeModem) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.toModem.Close()
	m.toHost.Close()
	return nil
}
func (m *fakeModem) sent() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.commands...)
}
func (m *fakeModem) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func waitIdle(t *testing.T, d *dialer) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if busy, _ := d.State(); !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("線から離れません")
}

// TestDialAndRelease は、番号を回して線をつかみ、「上げた」で離れること・つかんでいるあいだのもう1本は断ることを固定します。
func TestDialAndRelease(t *testing.T) {
	m := newFakeModem(nil)
	d := &dialer{open: func() (port, error) { return m, nil }, hold: time.Minute, timeout: time.Second}
	if err := d.Dial("0312345678"); err != nil {
		t.Fatalf("かけられません: %v", err)
	}
	if busy, n := d.State(); !busy || n != "0312345678" {
		t.Errorf("かけている様子 = %v %q", busy, n)
	}
	if err := d.Dial("0698765432"); err != errBusy {
		t.Errorf("⚠ つかんでいるあいだに、もう1本かけました: %v", err)
	}
	d.Release()
	waitIdle(t, d)
	want := []string{"ATZ", "ATX3", "ATDT0312345678;", "ATH0"}
	if got := m.sent(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("送った命令 = %v（%v のはず）", got, want)
	}
	if !m.isClosed() {
		t.Error("ポートを閉じていません")
	}
}

// TestDialHoldTimesOut は、受話器を上げなくても、hold がたてば線から離れることを固定します。
func TestDialHoldTimesOut(t *testing.T) {
	m := newFakeModem(nil)
	d := &dialer{open: func() (port, error) { return m, nil }, hold: 20 * time.Millisecond, timeout: time.Second}
	if err := d.Dial("0312345678"); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, d)
	if got := m.sent(); got[len(got)-1] != "ATH0" {
		t.Errorf("離れていません: %v", got)
	}
}

// TestDialBusyLine は、話し中（BUSY）なら誤りを返し、線から離れることを固定します。
func TestDialBusyLine(t *testing.T) {
	m := newFakeModem(map[string]string{"ATDT": "BUSY"})
	d := &dialer{open: func() (port, error) { return m, nil }, hold: time.Minute, timeout: time.Second}
	if err := d.Dial("0312345678"); err == nil || !strings.Contains(err.Error(), "BUSY") {
		t.Fatalf("話し中を誤りにしていません: %v", err)
	}
	if busy, _ := d.State(); busy {
		t.Error("⚠ 話し中なのに、かけている様子のままです")
	}
	if got := m.sent(); got[len(got)-1] != "ATH0" {
		t.Errorf("話し中のあと離れていません: %v", got)
	}
}

// TestHandlerRejectsNonDigits は、口が数字6〜15桁でない番号を断り、モデムを開かないことを固定します。
func TestHandlerRejectsNonDigits(t *testing.T) {
	opened := false
	d := &dialer{open: func() (port, error) { opened = true; return newFakeModem(nil), nil }, hold: time.Minute, timeout: time.Second}
	h := handler(d)
	for _, body := range []string{`{"number":"03-1234-5678"}`, `{"number":"12345"}`, `{"number":"0312345678;ATH"}`, `{}`} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("POST", "/dial", strings.NewReader(body)))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s を断っていません: %d", body, rr.Code)
		}
	}
	if opened {
		t.Error("⚠ 読めない番号でモデムを開きました")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/dial", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET でかけようとしました: %d", rr.Code)
	}
}
