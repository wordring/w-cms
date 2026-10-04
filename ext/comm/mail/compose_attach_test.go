package mail

// パソコンのファイルを添える（2026-10-04・compose_attach.go）。
//
// ⚠ **本物のメールは出しません**——送る口（`sendMail`）を偽物に差し替えて、何が渡ったかを見ます。

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// uploadToDraft は送る欄と同じ形（multipart: draft_id・file）で /api/mail/attach を呼びます。
func uploadToDraft(t *testing.T, draftID, name string, content []byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("draft_id", draftID)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write(content)
	mw.Close()
	req := httptest.NewRequest("POST", "/api/mail/attach", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = auth.WithUser(req, alice)
	rec := httptest.NewRecorder()
	MailAttachAPIHandler(rec, req)
	out := map[string]any{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// newDraft は新しいメールの下書きを1枚作ります。
func newDraft(t *testing.T) string {
	t.Helper()
	code, out := callJSON(t, DraftSaveAPIHandler, "POST", "/api/mail/draft", map[string]any{
		"purpose": comm.PurposeNew, "to": []string{"t@example.jp"}, "subject": "図面を送ります", "body": "本文",
	}, alice)
	id, _ := out["draft_id"].(string)
	if code != 200 || id == "" {
		t.Fatalf("下書きを作れません: %d %v", code, out)
	}
	return id
}

// TestLocalFileGoesToDraftAndSentRecord は、パソコンのファイルを下書きへ置いて送ると——メールに元の名前で添わり、
// 控えのページへ写って控えのリンクが控えのファイルを指し（ごみ箱の下書きを指さない）、下書きは片付く——を固定します。
func TestLocalFileGoesToDraftAndSentRecord(t *testing.T) {
	setupMailTest(t)
	seedSourceMail(t)
	var sent comm.OutgoingMail
	orig := sendMail
	sendMail = func(_ *auth.User, m comm.OutgoingMail) (string, error) { sent = m; return "<new@example.jp>", nil }
	t.Cleanup(func() { sendMail = orig })

	draftID := newDraft(t)
	code, up := uploadToDraft(t, draftID, "図面A.pdf", []byte("%PDF-1.4 手元の図面"))
	if code != 200 || up["success"] != true || up["page_id"] != draftID || up["name"] != "図面A.pdf" {
		t.Fatalf("下書きへ置けません: %d %v", code, up)
	}
	file, _ := up["file"].(string)
	if _, ok := page.AttachmentPath(draftID, file); !ok || file == "" {
		t.Fatalf("下書きにファイルがありません: %v", up)
	}

	code, out := callJSON(t, MailSendAPIHandler, "POST", "/api/mail/send", map[string]any{
		"purpose": comm.PurposeNew, "draft_id": draftID, "to": []string{"t@example.jp"}, "subject": "図面を送ります", "body": "本文",
		"attachments": []map[string]string{
			{"page_id": draftID, "file": file, "name": "図面A.pdf"},
			{"page_id": "000200", "file": "ab12.pdf", "name": "見積.pdf"}, // w-cms の中のファイルはそのまま元を指す
		},
	}, alice)
	if code != 200 || out["success"] != true {
		t.Fatalf("送れません: %d %v", code, out)
	}
	if _, ok := out["attach_error"]; ok {
		t.Errorf("写せたはずなのに attach_error: %v", out)
	}
	if len(sent.Attachments) != 2 || sent.Attachments[0].Name != "図面A.pdf" || string(sent.Attachments[0].Content) != "%PDF-1.4 手元の図面" {
		t.Errorf("メールの添付が違います: %+v", sent.Attachments)
	}
	recordID, _ := out["page_id"].(string)
	body, _ := cms.ReadPageBody(recordID)
	if strings.Contains(body, "/"+draftID+"/") {
		t.Errorf("⚠ 控えのリンクが下書き（ごみ箱へ行く）を指しています:\n%s", body)
	}
	if !strings.Contains(body, `href="/000200/ab12.pdf"`) {
		t.Errorf("w-cms の中のファイルは元のページを指したままのはず:\n%s", body)
	}
	metas := cms.ReadAttachmentMetas(recordID)
	var copied string
	for stored, m := range metas {
		if m.Name == "図面A.pdf" {
			copied = stored
		}
	}
	if copied == "" {
		t.Fatalf("控えにパソコンのファイルが写っていません: %v", metas)
	}
	if !strings.Contains(body, `href="`+page.AttachmentURLFor(recordID, copied)+`"`) {
		t.Errorf("控えのリンクが写したファイルを指していません:\n%s", body)
	}
	if p, ok := page.AttachmentPath(recordID, copied); !ok {
		t.Errorf("写したファイルの場所がありません")
	} else if b, _ := os.ReadFile(p); string(b) != "%PDF-1.4 手元の図面" {
		t.Errorf("写したファイルの中身が違います: %q", b)
	}
	if _, err := os.Stat(page.GetPageDir(draftID)); !os.IsNotExist(err) {
		t.Errorf("下書き /%s が片付いていません", draftID)
	}
}

// TestLocalFileRefused は、送れない種類・中身の合わない PDF・下書きでないページへは置かないことを固定します。
func TestLocalFileRefused(t *testing.T) {
	setupMailTest(t)
	seedSourceMail(t)
	draftID := newDraft(t)
	if code, out := uploadToDraft(t, draftID, "動かす.exe", []byte("MZ")); code != 400 {
		t.Errorf("送れない種類（.exe）を %d %v で受けました（400 のはず）", code, out)
	}
	if code, out := uploadToDraft(t, draftID, "偽物.pdf", []byte("PDFではない")); code != 400 {
		t.Errorf("中身が PDF でないものを %d %v で受けました（400 のはず）", code, out)
	}
	// ⚠ 下書きでないページ（受信の記録）へは置かない——この口で任意のページへ書かせない。
	if code, _ := uploadToDraft(t, "000200", "図面.pdf", []byte("%PDF-1.4 x")); code != 409 {
		t.Errorf("下書きでないページへ置こうとして %d です（409 のはず）", code)
	}
	if len(cms.ReadAttachmentMetas("000200")) != 0 {
		t.Errorf("⚠ 受信の記録にファイルが増えました: %v", cms.ReadAttachmentMetas("000200"))
	}
}
