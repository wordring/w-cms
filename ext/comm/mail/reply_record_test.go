package mail

import (
	"strings"
	"testing"
	"time"

	"w-cms/ext/comm"
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
	body, err := sentRecordBody(testSentTemplate, "admin@example.jp", "010272", "<parent@example.jp>", "<me@example.jp>",
		time.Date(2026, 9, 15, 10, 0, 0, 0, time.Local),
		ReplyRequest{
			Subject: "図面の件",
			To:      []string{"suzuki@example.jp, 田中 <tanaka@example.jp>"},
			Cc:      []string{"cc@example.jp"},
			Body:    "よろしくお願いします。",
		})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"<dt>" + comm.FromTag + "</dt><dd>admin@example.jp</dd>",
		"<dt>" + comm.ToTag + "</dt><dd>suzuki@example.jp</dd>",
		"<dt>" + comm.ToTag + "</dt>",
		"<dt>" + comm.CcTag + "</dt><dd>cc@example.jp</dd>",
		"<dt>" + comm.SentAtTag + "</dt>",
		"<dt>" + comm.InReplyToTag + "</dt>",
		"<dt>" + comm.ParentPageTag + "</dt><dd>010272</dd>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("控えに %q がありません:\n%s", want, body)
		}
	}
	// **廃止した名前が戻っていないこと**（逆向きの固定）。
	for _, gone := range []string{"差出人アドレス", "宛先アドレス", "CCアドレス",
		"<dt>メッセージID</dt>", "<dt>返信元メッセージID</dt>", "<dt>返信元</dt>"} { // 2026-10-09 に名前を替えた
		if strings.Contains(body, gone) {
			t.Errorf("廃止した名前 %q で書いています:\n%s", gone, body)
		}
	}
	// 宛先は1人1タグ（カンマで並べても2つに割れる）。
	if n := strings.Count(body, "<dt>"+comm.ToTag+"</dt>"); n != 2 {
		t.Errorf("宛先が1人1タグになっていません: %d 個", n)
	}
}

// testSentTemplate は職場の「通信記録（送信メール）」テンプレートの形です（2026-09-27）。
const testSentTemplate = "<h1>" + SentTemplate + "</h1>" +
	"<section><h2>" + comm.MailBodyHeading + "</h2></section>" +
	"<section><h2>" + comm.MailFilesHeading + "</h2></section>"

// TestSentRecordFillsTheTemplate は、控えの本文と添付がテンプレートの見出しの節に入り、
// 器が無ければ組まない（＝送る前に断れる）ことを固定します（2026-09-27）。
func TestSentRecordFillsTheTemplate(t *testing.T) {
	req := ReplyRequest{Subject: "見積の件", To: []string{"a@example.jp"}, Body: "よろしく",
		Attachments: []AttachRef{{PageID: "000123", File: "ab12.pdf", Name: "見積.pdf"}}}
	body, err := sentRecordBody(testSentTemplate, "me@example.jp", "", "", "<m@x>", time.Now(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<h1>見積の件</h1><dl data-type=\"tags\">",
		"<section><h2>" + comm.MailBodyHeading + "</h2><pre>よろしく</pre></section>",
		"<section><h2>" + comm.MailFilesHeading + "</h2><p>📎 <a ",
		`href="/000123/ab12.pdf">見積.pdf</a></p></section>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%q がありません:\n%s", want, body)
		}
	}
	// 添付の節の無いテンプレートでは、ファイルを添えた控えを組まない。
	noFiles := "<h1>" + SentTemplate + "</h1><section><h2>" + comm.MailBodyHeading + "</h2></section>"
	if _, err := sentRecordBody(noFiles, "me@example.jp", "", "", "", time.Now(), req); err == nil {
		t.Error("添付の置き場が無いのに控えを組みました（送ったあとで控えが作れなくなる）")
	}
	req.Attachments = nil
	if _, err := sentRecordBody(noFiles, "me@example.jp", "", "", "", time.Now(), req); err != nil {
		t.Errorf("添付の無い控えは添付の節が無くても組めるはず: %v", err)
	}
}
