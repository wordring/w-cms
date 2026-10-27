package comm

import (
	"net/mail"
	"os"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 送り元の確かめ（sender_check.go・2026-10-27）の試験。

func headerOf(t *testing.T, raw string) mail.Header {
	t.Helper()
	m, err := mail.ReadMessage(strings.NewReader(raw + "\r\nbody\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	return m.Header
}

func TestSenderCheckOf(t *testing.T) {
	cases := []struct {
		name, header, want string
	}{
		{"判定が無い", "From: a@example.jp\r\n", SenderCheckNone},
		{"通過", "Authentication-Results: spf=pass (sender IP is 192.0.2.1) smtp.mailfrom=example.jp; dkim=none; dmarc=pass action=none header.from=example.jp;compauth=pass reason=100\r\n", SenderCheckNoFail},
		{"DMARC の失敗", "Authentication-Results: spf=softfail; dkim=none; dmarc=fail action=none header.from=example.jp;compauth=fail reason=001\r\n", SenderCheckFailed},
		{"compauth だけの失敗・大文字", "Authentication-Results: spf=pass; dmarc=bestguesspass;\r\n COMPAUTH=FAIL reason=601\r\n", SenderCheckFailed},
		// ⚠ 偽の「通過」を上に書き足しても、受け取ったサーバーの「失敗」は消せない——1本でも失敗なら失敗。
		{"偽の通過と本物の失敗", "Authentication-Results: mx.example.jp; dmarc=pass header.from=bank.example\r\nAuthentication-Results: spf=fail; dmarc=fail header.from=bank.example\r\n", SenderCheckFailed},
		{"SPF と DKIM だけの失敗は失敗にしない", "Authentication-Results: spf=fail; dkim=fail; dmarc=pass header.from=example.jp\r\n", SenderCheckNoFail},
		{"ARC の判定は数えない", "ARC-Authentication-Results: i=1; mx.example.jp; dmarc=fail\r\nAuthentication-Results: dmarc=pass\r\n", SenderCheckNoFail},
	}
	for _, c := range cases {
		if got := senderCheckOf(headerOf(t, c.header)); got != c.want {
			t.Errorf("%s: %q（%q のはず）", c.name, got, c.want)
		}
	}
}

func TestReplyToElsewhere(t *testing.T) {
	if r := replyToElsewhere([]string{"山田 <yamada@example.jp>"}, []string{"窓口 <sales@EXAMPLE.jp>"}); r != "" {
		t.Errorf("同じ会社なのに違うと言った: %q", r)
	}
	if r := replyToElsewhere([]string{"yamada@example.jp"}, []string{"pay@evil.example"}); r != "pay@evil.example" {
		t.Errorf("違う会社を見逃した: %q", r)
	}
	if r := replyToElsewhere([]string{"yamada@example.jp"}, nil); r != "" {
		t.Errorf("返信先が無いのに違うと言った: %q", r)
	}
}

// TestIntakeWritesSenderCheckAndWarns は、取り込みが判定をタグに書くこと・記録の箱と未処理の一覧に ⚠ が出ること・
// 消したタグを原本から書き足せることを確かめます。
func TestIntakeWritesSenderCheckAndWarns(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)
	ctx := &IntakeContext{InboxID: inbox, Uploader: "alice"}
	spoof := "Authentication-Results: spf=fail; dkim=none; dmarc=fail action=quarantine header.from=bank.example;compauth=fail reason=000\r\n" +
		"From: 銀行 <info@bank.example>\r\n" +
		"Reply-To: pay@evil.example\r\n" +
		"To: order@example.co.jp\r\n" +
		"Subject: 振込先変更のお知らせ\r\n" +
		"Date: Mon, 01 Sep 2026 10:30:00 +0900\r\n" +
		"Message-ID: <spoof@bank.example>\r\n\r\nhonbun\r\n"
	id, _, err := emlIntake{}.OnFile(ctx, "s.eml", []byte(spoof))
	if err != nil {
		t.Fatal(err)
	}
	okID, _, err := emlIntake{}.OnFile(ctx, "o.eml", []byte("Authentication-Results: dmarc=pass\r\n"+buildEml("<ok@example.jp>", "普通のメール")))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(cms.PageTagValue(database.DB, atoiPage(id), SenderCheckTag)); got != SenderCheckFailed {
		t.Fatalf("なりすましの記録の判定: %q", got)
	}
	if got := strings.TrimSpace(cms.PageTagValue(database.DB, atoiPage(okID), SenderCheckTag)); got != SenderCheckNoFail {
		t.Fatalf("普通の記録の判定: %q", got)
	}

	alice := &auth.User{Username: "alice", IsAdmin: true}
	box := senderWarningsHTML(alice, atoiPage(id), false)
	if !strings.Contains(box, "送り元の確かめに失敗") || !strings.Contains(box, "pay@evil.example") {
		t.Errorf("記録の箱に ⚠ が出ていない:\n%s", box)
	}
	if box := senderWarningsHTML(alice, atoiPage(okID), false); box != "" {
		t.Errorf("⚠ の無いはずの記録に何か出た（✓ も出さない）:\n%s", box)
	}
	list := unhandledViewHTML(alice, atoiPage(inbox))
	if strings.Count(list, `class="unhandled-warn"`) != 1 {
		t.Errorf("未処理の一覧の ⚠ が1つでない:\n%s", list)
	}

	// 今ある記録への書き足し: タグを消してから、原本から読み直す。
	if err := cms.RewriteBody(id, "alice", func(cur string) string {
		d := cms.EditBody(cur)
		d.ReplaceTag(SenderCheckTag)
		return d.HTML()
	}); err != nil {
		t.Fatal(err)
	}
	if v := cms.PageTagValue(database.DB, atoiPage(id), SenderCheckTag); v != "" {
		t.Fatalf("タグが消えていない: %q", v)
	}
	n, err := FillSenderChecks("alice")
	if err != nil || n != 1 {
		t.Fatalf("書き足した数: %d %v（1のはず）", n, err)
	}
	if got := strings.TrimSpace(cms.PageTagValue(database.DB, atoiPage(id), SenderCheckTag)); got != SenderCheckFailed {
		t.Errorf("原本から読み直した判定: %q", got)
	}
	if n, _ := FillSenderChecks("alice"); n != 0 {
		t.Errorf("2回目も書き足した: %d", n)
	}

	// 原本が無い記録は「判定なし」にして、読み直しを繰り返さない。
	if err := cms.RewriteBody(okID, "alice", func(cur string) string {
		d := cms.EditBody(cur)
		d.ReplaceTag(SenderCheckTag)
		return d.HTML()
	}); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(page.AttachmentDir(okID))
	if n, _ := FillSenderChecks("alice"); n != 1 || strings.TrimSpace(cms.PageTagValue(database.DB, atoiPage(okID), SenderCheckTag)) != SenderCheckNone {
		t.Errorf("原本の無い記録が「判定なし」になっていない")
	}
}
