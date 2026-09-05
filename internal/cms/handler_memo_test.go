package cms

import (
	"strings"
	"testing"
	"time"
)

// 手で作る記録のテスト（2026-09-05）。
//
// 固定するのは**書くのは分かることだけ**という線です——メモには受信日時を書かず、
// 発信には向きを書く。ここが崩れると、かけた電話が作業待ちに並びます。

// TestMemoBodyWritesOnlyWhatIsKnown は、チャネルごとに書くタグが変わることを固定します。
func TestMemoBodyWritesOnlyWhatIsKnown(t *testing.T) {
	cases := []struct {
		name      string
		channel   string
		direction string
		want      []string
		notWant   []string
	}{
		{
			name: "電話（受けた）", channel: "電話", direction: DirectionIn,
			want:    []string{"<dt>向き</dt><dd>受信</dd>", "<dt>チャネル</dt><dd>電話</dd>", "<dt>受信日時</dt>"},
			notWant: []string{"<dt>発信日時</dt>"},
		},
		{
			name: "電話（かけた）", channel: "電話", direction: DirectionOut,
			want: []string{"<dt>向き</dt><dd>送信</dd>", "<dt>発信日時</dt>",
				"<dt>電話番号</dt><dd>06-1234-5678</dd>", "<dt>相手</dt><dd>010199</dd>"},
			notWant: []string{"<dt>受信日時</dt>"},
		},
		{
			name: "FAX（送った）", channel: "FAX", direction: DirectionOut,
			want:    []string{"<dt>向き</dt><dd>送信</dd>", "<dt>チャネル</dt><dd>FAX</dd>", "<dt>発信日時</dt>"},
			notWant: []string{"<dt>受信日時</dt>"},
		},
		{
			name: "メモ", channel: "メモ", direction: "",
			want: []string{"<dt>チャネル</dt><dd>メモ</dd>"},
			// **メモは届きも出もしません**。向きも日時も書かない（並びは更新日時が代わる）。
			notWant: []string{"<dt>向き</dt>", "<dt>受信日時</dt>", "<dt>発信日時</dt>"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			phone, cp := "", ""
			if c.channel == "電話" && c.direction == DirectionOut {
				phone, cp = "06-1234-5678", "010199"
			}
			got := memoBodyHTML(c.channel, "用件", c.direction, phone, cp, time.Now())
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("%q がありません: %s", w, got)
				}
			}
			for _, n := range c.notWant {
				if strings.Contains(got, n) {
					t.Errorf("%q が入っています: %s", n, got)
				}
			}
		})
	}
}
