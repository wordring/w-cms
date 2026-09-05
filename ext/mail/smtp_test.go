package mail

import (
	"encoding/base64"
	"mime"
	"strings"
	"testing"

	"w-cms/internal/cms"
)

// SMTP へ渡すMIMEの組み立てのテスト（2026-09-03）。
//
// **In-Reply-To が立つことが SMTP を選んだ理由そのもの**なので、そこを固定します。
// これが無いと、返信が相手のメールソフトで元のスレッドに並びません。

func TestBuildMIMESetsInReplyTo(t *testing.T) {
	out, err := buildMIME("me@example.com", "<mine@example.com>", cms.OutgoingMail{
		To:        []string{"you@example.com"},
		Subject:   "RE: お見積り依頼",
		BodyText:  "本文です。",
		InReplyTo: "<original@toa.example>",
	})
	if err != nil {
		t.Fatalf("組み立てエラー: %v", err)
	}
	s := string(out)
	for _, want := range []string{
		"In-Reply-To: <original@toa.example>",
		// References も入れる（メールソフトはどちらも見ます）。
		"References: <original@toa.example>",
		"Message-ID: <mine@example.com>",
		"From: me@example.com",
		"To: you@example.com",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("%q がありません:\n%s", want, s)
		}
	}
}

// TestBuildMIMEEncodesJapaneseSubject は、和文の件名が符号化語になることを
// 固定します。生のまま置くと経路によっては壊れます。
func TestBuildMIMEEncodesJapaneseSubject(t *testing.T) {
	out, _ := buildMIME("me@example.com", "<m@x>", cms.OutgoingMail{
		To: []string{"you@example.com"}, Subject: "お見積り依頼", BodyText: "x",
	})
	s := string(out)
	if strings.Contains(s, "Subject: お見積り依頼") {
		t.Errorf("件名が符号化されていません:\n%s", s)
	}
	// 復号して元へ戻ること。
	var dec mime.WordDecoder
	for _, line := range strings.Split(s, "\r\n") {
		if !strings.HasPrefix(line, "Subject: ") {
			continue
		}
		got, err := dec.DecodeHeader(strings.TrimPrefix(line, "Subject: "))
		if err != nil || got != "お見積り依頼" {
			t.Errorf("件名を復号できません: %q (%v)", got, err)
		}
		return
	}
	t.Errorf("Subject がありません:\n%s", s)
}

// TestBuildMIMEBodyIsBase64 は、本文が base64 で運ばれることを固定します
// （和文を 8bit のまま流さない）。
func TestBuildMIMEBodyIsBase64(t *testing.T) {
	body := "こんにちは。\n改行もあります。"
	out, _ := buildMIME("me@example.com", "<m@x>", cms.OutgoingMail{
		To: []string{"you@example.com"}, Subject: "x", BodyText: body,
	})
	s := string(out)
	if !strings.Contains(s, "Content-Transfer-Encoding: base64") {
		t.Fatalf("base64 になっていません:\n%s", s)
	}
	i := strings.Index(s, "\r\n\r\n")
	if i < 0 {
		t.Fatalf("ヘッダと本文の境目がありません")
	}
	enc := strings.ReplaceAll(strings.TrimSpace(s[i+4:]), "\r\n", "")
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("本文を復号できません: %v", err)
	}
	if string(raw) != body {
		t.Errorf("本文が変わっています: %q", string(raw))
	}
}

// TestBuildMIMEAttachesFiles は、添付が multipart で載ることを固定します。
// **SMTP の利点の1つが添付の上限**（M365 で約35MB）なので、ここは固定しておきます。
func TestBuildMIMEAttachesFiles(t *testing.T) {
	out, err := buildMIME("me@example.com", "<m@x>", cms.OutgoingMail{
		To: []string{"you@example.com"}, Subject: "x", BodyText: "本文",
		Attachments: []cms.MailAttachment{{
			Name: "図面.pdf", MIMEType: "application/pdf", Content: []byte("%PDF-1.4 test"),
		}},
	})
	if err != nil {
		t.Fatalf("組み立てエラー: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "multipart/mixed") {
		t.Errorf("multipart になっていません:\n%s", s)
	}
	if !strings.Contains(s, "Content-Type: application/pdf") {
		t.Errorf("添付の種別がありません:\n%s", s)
	}
	// 和文のファイル名も符号化語で運ぶ（実際に和文の図面名が来ます）。
	if strings.Contains(s, `filename="図面.pdf"`) {
		t.Errorf("和文のファイル名が符号化されていません:\n%s", s)
	}
	if !strings.Contains(s, base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 test"))) {
		t.Errorf("添付の中身がありません:\n%s", s)
	}
}

// TestXOAUTH2InitialResponse は認証の初手の形を固定します
// （区切りは 0x01。ここを間違えると「認証は通らないが理由も出ない」になります）。
func TestXOAUTH2InitialResponse(t *testing.T) {
	a := &xoauth2Auth{user: "me@example.com", token: "TOK"}
	name, init, err := a.Start(nil)
	if err != nil {
		t.Fatalf("Start エラー: %v", err)
	}
	if name != "XOAUTH2" {
		t.Errorf("方式が違います: %q", name)
	}
	if want := "user=me@example.com\x01auth=Bearer TOK\x01\x01"; string(init) != want {
		t.Errorf("初手が違います: %q", string(init))
	}
}
