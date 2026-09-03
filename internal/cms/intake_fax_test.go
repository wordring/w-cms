package cms

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// FAX（スキャンPDF）の取り込み係のテスト。
//
// 固定するのは「**同じ口が2つの運用に効く**」ことの土台——人がドロップしても
// FAXサーバの橋渡しが POST しても、通るのは同じ `serveIntake` → この係。
// ここではその係自身の振る舞い（記録ページの形・重複検知・解析へ繋がる形）を見る。

// TestFaxIntakeCreatesRecordPage は、PDF が通信記録ページになることを検証します。
func TestFaxIntakeCreatesRecordPage(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)

	pdf := []byte("%PDF-1.4 fax scan")
	ctx := &IntakeContext{InboxID: inbox, Uploader: "alice"}
	pageID, title, err := (faxIntake{}).OnFile(ctx, "20260903_1430_0312345678.pdf", pdf)
	if err != nil {
		t.Fatalf("取り込みエラー: %v", err)
	}
	// 題はファイル名から（FAXサーバは受信時刻や発信番号を名前へ入れる）。
	if title != "20260903_1430_0312345678" {
		t.Errorf("題がファイル名から採られていません: %q", title)
	}

	body, err := os.ReadFile(filepath.Join(page.GetPageDir(pageID), pageID+".html"))
	if err != nil {
		t.Fatalf("作られたページを読めません: %v", err)
	}
	html := string(body)
	for _, want := range []string{
		"<h1>20260903_1430_0312345678</h1>",
		"<dt>チャネル</dt><dd>FAX</dd>",
		"<dt>取り込み日時</dt>", // **受信日時ではない**——PDFは受け取った時刻を持たない
		"<dt>内容ハッシュ</dt>",
		`download="20260903_1430_0312345678.pdf"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ページに %q がありません:\n%s", want, html)
		}
	}
	// **分からないことは書かない**——受信日時のタグは作らない。
	if strings.Contains(html, "<dt>受信日時</dt>") {
		t.Errorf("知らない受信日時を書いています:\n%s", html)
	}
	// 📎 リンクの宛先が**きれいなURL**であること——閲覧モードのクロームは
	// この形（/<6桁ページID>/<生成ID>.pdf）に一致するときだけ「▶ 表示」と
	// **「🤖 解析」**を付ける。FAXで来た発注書がそのまま受注ページになる道。
	// （属性の並びはサニタイザが決めるので、順序には依存しない検査にする。）
	if !strings.Contains(html, "📎 ") ||
		!regexp.MustCompile(`href="/`+pageID+`/[0-9a-z]+\.pdf"`).MatchString(html) {
		t.Errorf("解析ボタンの付く📎リンクの形になっていません:\n%s", html)
	}

	// PDFは解釈していないので、添付そのものが受信原本（1件だけ・原文のまま）。
	entries, err := os.ReadDir(page.AttachmentDir(pageID))
	if err != nil || len(entries) != 1 {
		t.Fatalf("添付が保存されていません: %v %v", entries, err)
	}
	saved, _ := os.ReadFile(filepath.Join(page.AttachmentDir(pageID), entries[0].Name()))
	if string(saved) != string(pdf) {
		t.Errorf("原本が原文のまま保存されていません: %q", saved)
	}
}

// TestFaxIntakeDetectsDuplicateByContent は、同じPDFを2度置いても記録が
// 二重にならないことを検証します（自然な鍵が無いので中身のハッシュを鍵にする）。
func TestFaxIntakeDetectsDuplicateByContent(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)

	pdf := []byte("%PDF-1.4 same fax")
	name, value, ok := (faxIntake{}).SourceRef("a.pdf", pdf)
	if !ok || name != ContentHashTag || len(value) != 64 {
		t.Fatalf("重複検知の鍵が返りません: %q %q %v", name, value, ok)
	}
	// 名前が違っても中身が同じなら同じ鍵（＝同じFAXの置き直し）。
	_, v2, _ := (faxIntake{}).SourceRef("b.pdf", pdf)
	if v2 != value {
		t.Errorf("同じ中身で鍵が変わります: %q vs %q", value, v2)
	}
	// 中身が違えば別の鍵（再送されて改めてスキャンされたFAXは別の受信）。
	_, v3, _ := (faxIntake{}).SourceRef("a.pdf", []byte("%PDF-1.4 other"))
	if v3 == value {
		t.Errorf("中身が違うのに鍵が同じです: %q", v3)
	}

	// 取り込んだあと、その鍵で記録ページが逆引きできる（コア側の照合が効く条件）。
	ctx := &IntakeContext{InboxID: inbox, Uploader: "alice"}
	if _, _, err := (faxIntake{}).OnFile(ctx, "a.pdf", pdf); err != nil {
		t.Fatalf("取り込みエラー: %v", err)
	}
	ids, err := PagesByTag(database.DB, ContentHashTag, value)
	if err != nil {
		t.Fatalf("逆引きエラー: %v", err)
	}
	if len(ids) != 1 {
		t.Errorf("内容ハッシュから記録ページを引けません: %v", ids)
	}
	if existing, dup := ExistingIntakePage(ContentHashTag, value); !dup || existing == "" {
		t.Errorf("コアの重複検知が効きません: %q %v", existing, dup)
	}
}

// TestChannelTagOnBothIntakes は、メールとFAXが**同じ名前の軸**で横断して
// 引けることを検証します（取引先Aからの受信をチャネル横断で一覧する土台・§6）。
func TestChannelTagOnBothIntakes(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)
	ctx := &IntakeContext{InboxID: inbox, Uploader: "alice"}

	if _, _, err := (emlIntake{}).OnFile(ctx, "m.eml",
		[]byte(buildEml("<ch@example.jp>", "メールの記録"))); err != nil {
		t.Fatalf("メールの取り込みエラー: %v", err)
	}
	if _, _, err := (faxIntake{}).OnFile(ctx, "f.pdf", []byte("%PDF-1.4 fax")); err != nil {
		t.Fatalf("FAXの取り込みエラー: %v", err)
	}

	for _, want := range []struct{ channel string }{{"メール"}, {"FAX"}} {
		ids, err := PagesByTag(database.DB, ChannelTag, want.channel)
		if err != nil {
			t.Fatalf("逆引きエラー: %v", err)
		}
		if len(ids) != 1 {
			t.Errorf("チャネル %q で引けません: %v", want.channel, ids)
		}
	}
}
