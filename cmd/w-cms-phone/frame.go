package main

// アロハ ND6 の電文（製造元の機能仕様書 9 章）を読む部分です。試験できるよう、シリアルから切り離してあります。
//
//	着信       STX ＋ 着信日時（月2・日2・曜日1・時2・分2＝9文字）＋ 発信者の番号（20文字・足りない分は空白）＋ ETX
//	           番号の代わりに 非通知「P」・表示圏外「O」・サービス競合「S」・公衆電話「C」・受信の誤り「E」
//	フック     STX ＋ 日時（9文字）＋「OF」（受話器を上げた）か「ON」（置いた）＋ ETX   ……着信のときだけ
//	未応答     STX ＋ 日時（9文字）＋「UR」＋ ETX                                       ……出る前に相手が切った
//	接続の確認 パソコンが STX「CON」ETX を送ると、STX「ALOHA」ETX が返る

import (
	"strconv"
	"strings"
	"time"
)

const (
	stx = 0x02
	etx = 0x03
)

// eventKind は電文の種類です。
type eventKind int

const (
	evUnknown eventKind = iota
	evRing              // 着信（番号）
	evOffHook           // 受話器を上げた（出た）
	evOnHook            // 受話器を置いた
	evMissed            // 未応答
	evAloha             // 接続の確認の返事
)

// event は読めた電文1つです。
type event struct {
	Kind   eventKind
	At     time.Time
	Number string // evRing のときの番号（または P・O・S・C・E）
}

// frameReader は、届いたバイトを STX〜ETX の電文に切り分けます。
type frameReader struct {
	buf  []byte
	open bool
}

// feed は届いたバイトを足し、できあがった電文の中身（STX と ETX を除く）を返します。STX の前のごみは捨てます。
func (f *frameReader) feed(p []byte) []string {
	var out []string
	for _, c := range p {
		c &= 0x7f // 7ビット（パリティはドライバが外すが、外れていなくても読めるように）
		switch {
		case c == stx:
			f.buf = f.buf[:0]
			f.open = true
		case c == etx && f.open:
			out = append(out, string(f.buf))
			f.buf = f.buf[:0]
			f.open = false
		case f.open:
			if len(f.buf) < 128 { // 長すぎる電文は壊れている——切り捨てる
				f.buf = append(f.buf, c)
			}
		}
	}
	return out
}

// parseStamp は ND6 の日時（月日・曜日・時分の9文字）を、now に近い年の時刻にします（年は送ってこない）。
func parseStamp(s string, now time.Time) (time.Time, bool) {
	if len(s) != 9 {
		return time.Time{}, false
	}
	n := func(a, b int) (int, bool) {
		v, err := strconv.Atoi(s[a:b])
		return v, err == nil
	}
	mo, ok1 := n(0, 2)
	d, ok2 := n(2, 4)
	h, ok3 := n(5, 7)
	mi, ok4 := n(7, 9)
	if !ok1 || !ok2 || !ok3 || !ok4 || mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 {
		return time.Time{}, false
	}
	t := time.Date(now.Year(), time.Month(mo), d, h, mi, 0, 0, now.Location())
	// 年の境: 1月に届いた「12月31日」は去年のもの。
	if t.Sub(now) > 24*time.Hour*31 {
		t = t.AddDate(-1, 0, 0)
	}
	return t, true
}

// parseFrame は電文の中身を読みます（now は年を決めるため・日時が読めなければ now）。
func parseFrame(body string, now time.Time) event {
	if body == "ALOHA" {
		return event{Kind: evAloha, At: now}
	}
	if len(body) < 11 {
		return event{Kind: evUnknown, At: now}
	}
	at, ok := parseStamp(body[:9], now)
	if !ok {
		at = now
	}
	rest := body[9:]
	switch {
	case len(body) == 11 && rest == "OF":
		return event{Kind: evOffHook, At: at}
	case len(body) == 11 && rest == "ON":
		return event{Kind: evOnHook, At: at}
	case len(body) == 11 && rest == "UR":
		return event{Kind: evMissed, At: at}
	case len(body) == 29:
		return event{Kind: evRing, At: at, Number: strings.TrimSpace(rest)}
	}
	return event{Kind: evUnknown, At: at}
}
