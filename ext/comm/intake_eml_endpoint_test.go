package comm

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// postEML は /api/intake/eml へ1ファイルを上げます。
func postEML(t *testing.T, u *auth.User, name string, content []byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	w.Close()
	req := httptest.NewRequest("POST", "/api/intake/eml", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if u != nil {
		req = auth.WithUser(req, u)
	}
	rr := httptest.NewRecorder()
	IntakeEMLAPIHandler(rr, req)
	var out map[string]any
	json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

// TestIntakeEMLEndpoint は `.eml` を通信記録にする口を固定します（2026-09-30——通信箱へ落とす道をやめた代わり・
// 使い手は tools/mail/push）。
//
//   - `.eml` は通信箱の子の記録になる・同じ Message-ID の2通目は作らない（重複）
//   - `.eml` でないものは断る（添付にもしない）
//   - 通信箱へ書けない人は断る
func TestIntakeEMLEndpoint(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)
	alice := &auth.User{Username: "alice"}

	eml := []byte(buildEml("<endpoint@example.jp>", "口の試験"))
	code, out := postEML(t, alice, "mail.eml", eml)
	if code != 200 || out["intake"] != true || out["page_id"] == nil || out["title"] != "口の試験" {
		t.Fatalf(".eml を記録にしていません: %d %+v", code, out)
	}
	code, out = postEML(t, alice, "mail.eml", eml)
	if code != 200 || out["duplicate"] != true {
		t.Errorf("同じメールの2通目を重複と言っていません: %d %+v", code, out)
	}

	if code, _ := postEML(t, alice, "図面.pdf", []byte("%PDF-1.4 x")); code != 400 {
		t.Errorf(".eml でないものを断っていません: %d", code)
	}

	// 通信箱へ書けない人（持ち主でもグループでもない）。
	if code, _ := postEML(t, &auth.User{Username: "mallory"}, "mail2.eml", []byte(buildEml("<other@example.jp>", "別"))); code == 200 {
		t.Errorf("通信箱へ書けない人の取り込みを通しました: %d（通信箱 %s）", code, inbox)
	}
}

// TestDropOnMailboxIsJustAttachment は、**通信箱へファイルを落としても記録は生まれない**ことを固定します
// （2026-09-30 利用者:「通信箱ページのファイルをドロップすると子ページが作られる機能はもはや必要ないでしょう」）。
// 落とすのと同じ口（/api/upload-file）へ `.eml` を上げると、ほかのページと同じく添付になるだけ。
func TestDropOnMailboxIsJustAttachment(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("page_id", inbox)
	fw, _ := w.CreateFormFile("file", "mail.eml")
	fw.Write([]byte(buildEml("<drop@example.jp>", "落としたメール")))
	w.Close()
	req := httptest.NewRequest("POST", "/api/upload-file", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req = auth.WithUser(req, &auth.User{Username: "alice"})
	rr := httptest.NewRecorder()
	cms.UploadFileHandler(rr, req)
	var out map[string]any
	json.Unmarshal(rr.Body.Bytes(), &out)
	if rr.Code != 200 || out["intake"] != nil || out["id"] == nil {
		t.Fatalf("添付になっていません（記録にしていませんか）: %d %s", rr.Code, rr.Body.String())
	}
	idInt, _ := strconv.Atoi(inbox)
	if kids, err := cms.ChildPages(database.DB, idInt); err != nil || len(kids) != 0 {
		t.Errorf("通信箱に子ページが生まれています: %+v %v", kids, err)
	}
}

// TestIntakeEMLOwnAddressOnlyAdminBecomesSent は、手で上げたメールの差出人が自社のアドレスでも、「送信」の記録になるのは管理者が
// 上げたときだけであることを固定します（2026-10-28 のセキュリティの点検）。ほかの人が上げると「受信」——自社を名乗るメールを
// 上げて、送ったことにした控えを作れないように。
func TestIntakeEMLOwnAddressOnlyAdminBecomesSent(t *testing.T) {
	setupSaveTest(t)
	setupInbox(t)
	RegisterOwnAddresses(func() []string { return []string{"me@example.co.jp"} })
	t.Cleanup(func() { RegisterOwnAddresses(nil) })
	own := func(id string) string {
		return "From: me@example.co.jp\r\nTo: a@example.jp\r\nSubject: 自社から\r\nDate: Mon, 01 Sep 2026 10:30:00 +0900\r\nMessage-ID: " + id + "\r\n\r\nhonbun\r\n"
	}
	dirOf := func(out map[string]any) string {
		id, _ := out["page_id"].(string)
		return strings.TrimSpace(cms.PageTagValue(database.DB, atoiPage(id), DirectionTag))
	}
	// alice は通信箱の持ち主だが、管理者ではない。
	code, out := postEML(t, &auth.User{Username: "alice"}, "a.eml", []byte(own("<own-a@example.co.jp>")))
	if code != 200 {
		t.Fatalf("取り込めない: %d %v", code, out)
	}
	if d := dirOf(out); d != DirectionIn {
		t.Errorf("⚠ 管理者でない人が上げた自社のメールが %q になった（受信のはず）", d)
	}
	code, out = postEML(t, &auth.User{Username: "root", IsAdmin: true}, "b.eml", []byte(own("<own-b@example.co.jp>")))
	if code != 200 {
		t.Fatalf("取り込めない: %d %v", code, out)
	}
	if d := dirOf(out); d != DirectionOut {
		t.Errorf("管理者が上げた自社のメールが %q（送信のはず）", d)
	}
}
