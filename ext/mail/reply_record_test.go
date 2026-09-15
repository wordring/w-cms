package mail

import (
	"strings"
	"testing"
	"time"

	"w-cms/internal/cms"
)

// TestSentRecordUsesIntakeTagNames は、**送信の控えが受信の取り込みと同じタグ名で書く**
// ことを固定します。
//
// 2026-09-13 に相手のタグを1人1タグ（`差出人`・`宛先`・`CC`）へ移したとき、受信の
// 取り込み（internal/cms/intake_eml.go）だけが追随し、ここは廃止した名前
// （`差出人アドレス`・`宛先アドレス`・`CCアドレス`）で書き続けていました。読む側
// ——返信の一覧（`/api/replies` の宛先）・スレッド・未登録の連絡先——は送信の控えの
// 相手を**黙って空**で受け取ります。実データに送信記録が0件だったので誰も踏まず、
// 2026-09-15 に拡張の接点を洗い出していて見つかりました。
func TestSentRecordUsesIntakeTagNames(t *testing.T) {
	body := sentRecordBody("admin@example.jp", "010272", "<parent@example.jp>", "<me@example.jp>",
		time.Date(2026, 9, 15, 10, 0, 0, 0, time.Local),
		ReplyRequest{
			Subject: "図面の件",
			To:      []string{"suzuki@example.jp, 田中 <tanaka@example.jp>"},
			Cc:      []string{"cc@example.jp"},
			Body:    "よろしくお願いします。",
		})

	for _, want := range []string{
		"<dt>" + cms.FromTag + "</dt><dd>admin@example.jp</dd>",
		"<dt>" + cms.ToTag + "</dt><dd>suzuki@example.jp</dd>",
		"<dt>" + cms.ToTag + "</dt>",
		"<dt>" + cms.CcTag + "</dt><dd>cc@example.jp</dd>",
		"<dt>" + cms.SentAtTag + "</dt>",
		"<dt>" + cms.InReplyToTag + "</dt>",
		"<dt>" + cms.ReplySourceTag + "</dt><dd>010272</dd>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("控えに %q がありません:\n%s", want, body)
		}
	}
	// **廃止した名前が戻っていないこと**（逆向きの固定）。
	for _, gone := range []string{"差出人アドレス", "宛先アドレス", "CCアドレス"} {
		if strings.Contains(body, gone) {
			t.Errorf("廃止した名前 %q で書いています:\n%s", gone, body)
		}
	}
	// 宛先は1人1タグ（カンマで並べても2つに割れる）。
	if n := strings.Count(body, "<dt>"+cms.ToTag+"</dt>"); n != 2 {
		t.Errorf("宛先が1人1タグになっていません: %d 個", n)
	}
}
