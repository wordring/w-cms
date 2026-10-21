package main

import (
	"testing"
	"time"
)

// TestFrames は ND6 の電文の切り分けと読み方を固定します（機能仕様書 9 章の形）。
func TestFrames(t *testing.T) {
	now := time.Date(2026, 10, 21, 10, 16, 0, 0, time.Local)
	var fr frameReader
	// ごみ・着信・フック（上げた）・フック（置いた）・未応答・接続の確認——2回に分けて届いても切り分ける。
	in := "xx\x02" + "102121015" + "0312345678          " + "\x03\x02102121015O"
	got := fr.feed([]byte(in))
	got = append(got, fr.feed([]byte("F\x03\x02102121020ON\x03\x02102121030UR\x03\x02ALOHA\x03"))...)
	if len(got) != 5 {
		t.Fatalf("電文の数が %d です（5 を期待）: %q", len(got), got)
	}
	want := []struct {
		kind   eventKind
		number string
		hm     string
	}{{evRing, "0312345678", "10:15"}, {evOffHook, "", "10:15"}, {evOnHook, "", "10:20"}, {evMissed, "", "10:30"}, {evAloha, "", "10:16"}}
	for i, w := range want {
		e := parseFrame(got[i], now)
		if e.Kind != w.kind || e.Number != w.number || e.At.Format("15:04") != w.hm {
			t.Errorf("%d 番目: %+v（種類 %v・番号 %q・%s を期待）", i, e, w.kind, w.number, w.hm)
		}
	}
	// 非通知は「P」のまま渡す（w-cms が言葉にする）。
	if e := parseFrame("102121015P                   ", now); e.Kind != evRing || e.Number != "P" {
		t.Errorf("非通知が読めません: %+v", e)
	}
	// 年の境: 1月2日に届いた 12月31日の着信は去年。
	jan := time.Date(2027, 1, 2, 9, 0, 0, 0, time.Local)
	if at, ok := parseStamp("123142359", jan); !ok || at.Year() != 2026 {
		t.Errorf("年の境の着信が %v です（2026 を期待）", at)
	}
}

// TestCallFlow は、電文の流れから w-cms への知らせを決める部分を固定します（出た着信だけ記録・未応答は記録しない）。
func TestCallFlow(t *testing.T) {
	now := time.Date(2026, 10, 21, 10, 16, 0, 0, time.Local)
	var calls []string
	c := &caller{now: func() time.Time { return now }, send: func(ev string, number string, at time.Time, pageID string) (string, error) {
		calls = append(calls, ev+":"+number+":"+pageID)
		if ev == "answered" {
			return "002100", nil
		}
		return "", nil
	}}
	// 出なかった着信（着信 → 未応答）は何も送らない。
	c.handle(event{Kind: evRing, At: now, Number: "0311112222"})
	c.handle(event{Kind: evMissed, At: now})
	// 出た着信（着信 → 上げた → 置いた）は answered と hangup。
	c.handle(event{Kind: evRing, At: now, Number: "0312345678"})
	c.handle(event{Kind: evOffHook, At: now})
	c.handle(event{Kind: evOnHook, At: now})
	want := []string{"answered:0312345678:", "hangup::002100"}
	if len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Errorf("w-cms への知らせが %q です（%q を期待）", calls, want)
	}
	// 古い着信（5分より前）に続く「上げた」は、番号なしで記録する（前の着信の番号を使わない）。
	calls = nil
	c.handle(event{Kind: evRing, At: now.Add(-10 * time.Minute), Number: "0399999999"})
	c.lastRingAt = now.Add(-10 * time.Minute)
	c.handle(event{Kind: evOffHook, At: now})
	if len(calls) != 1 || calls[0] != "answered::" {
		t.Errorf("古い着信の番号を使っています: %q", calls)
	}
}
