package cms

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 取り込み係（.eml → 通信記録ページ）のテスト。
//
// 受信箱はトップ直下の「受信箱」ページ（名前が機能を決める・テンプレート置き場と
// 同じ型）。和文メールの復号（ISO-2022-JP）・添付の保存（生成ID・files/）・
// タグの索引まで、既存の仕組みに乗ることを固定する。

// setupInbox は受信箱ページを作ります。
func setupInbox(t *testing.T) string {
	t.Helper()
	if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, parent_id, file_path) VALUES (90, ?, 0, '')`, InboxTitle); err != nil {
		t.Fatalf("受信箱の作成エラー: %v", err)
	}
	if err := page.WriteSidecar("000090", page.PageMeta{Owner: "alice", Group: "team", Mode: "330", ParentID: "000000"}); err != nil {
		t.Fatalf("サイドカー作成エラー: %v", err)
	}
	// 実運用と同じく索引まで通す（権限の読み口は派生＝page_perms。継承はそこから引く）。
	if err := SyncIndex("000090", "<h1>"+InboxTitle+"</h1>"); err != nil {
		t.Fatalf("受信箱の同期エラー: %v", err)
	}
	return "000090"
}

// iso2022jp は文字列を ISO-2022-JP へ符号化します（和文メールの実物形式）。
func iso2022jp(t *testing.T, s string) string {
	t.Helper()
	out, _, err := transform.String(japanese.ISO2022JP.NewEncoder(), s)
	if err != nil {
		t.Fatalf("ISO-2022-JP符号化エラー: %v", err)
	}
	return out
}

// TestInboxPageID は受信箱の解決（トップ直下・名前が正）を検証します。
func TestInboxPageID(t *testing.T) {
	setupSaveTest(t)
	if _, ok := InboxPageID(); ok {
		t.Fatal("受信箱が無いのに見つかりました")
	}
	want := setupInbox(t)
	got, ok := InboxPageID()
	if !ok || got != want {
		t.Errorf("受信箱を解決できません: got %q ok=%v", got, ok)
	}
}

// TestEmlIntakeCreatesRecordPage は、和文メール（ISO-2022-JP・PDF添付つき）が
// 通信記録ページになることを検証します。
func TestEmlIntakeCreatesRecordPage(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)

	pdf := []byte("%PDF-1.4 fake")
	eml := "From: " + "=?ISO-2022-JP?B?" +
		base64.StdEncoding.EncodeToString([]byte(iso2022jp(t, "トーアスポーツ"))) + "?= <toa@example.jp>\r\n" +
		"To: order@example.co.jp\r\n" +
		"Subject: =?ISO-2022-JP?B?" +
		base64.StdEncoding.EncodeToString([]byte(iso2022jp(t, "発注書送付の件"))) + "?=\r\n" +
		"Date: Mon, 01 Sep 2026 10:30:00 +0900\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=BOUND\r\n" +
		"\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/plain; charset=ISO-2022-JP\r\n" +
		"Content-Transfer-Encoding: 7bit\r\n" +
		"\r\n" +
		iso2022jp(t, "お世話になっております。\r\n発注書を送付いたします。") + "\r\n" +
		"--BOUND\r\n" +
		"Content-Type: application/pdf; name=\"chumon.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"Content-Disposition: attachment; filename=\"chumon.pdf\"\r\n" +
		"\r\n" +
		base64.StdEncoding.EncodeToString(pdf) + "\r\n" +
		"--BOUND--\r\n"

	ctx := &IntakeContext{InboxID: inbox, Uploader: "alice"}
	pageID, title, err := emlIntake{}.OnFile(ctx, "mail.eml", []byte(eml))
	if err != nil {
		t.Fatalf("取り込みエラー: %v", err)
	}
	if title != "発注書送付の件" {
		t.Errorf("件名の復号が違います: %q", title)
	}

	body, err := os.ReadFile(filepath.Join(page.GetPageDir(pageID), pageID+".html"))
	if err != nil {
		t.Fatalf("作られたページを読めません: %v", err)
	}
	// 受信日時は ISO 8601 の**ローカル時刻＋オフセット**表記（2026-09-01 ユーザー要望。
	// 期待値はテスト実行機のタイムゾーンで組む——表記が変わっても指す時刻は同じ）。
	wantDate := time.Date(2026, 9, 1, 10, 30, 0, 0, time.FixedZone("JST", 9*3600)).
		In(time.Local).Format(time.RFC3339)

	html := string(body)
	for _, want := range []string{
		"<h1>発注書送付の件</h1>",
		"トーアスポーツ",                  // 差出人の復号
		"<dd>" + wantDate + "</dd>", // 受信日時
		"発注書を送付いたします。",            // 本文の復号
		`download="chumon.pdf"`,      // 元名はリンクが運ぶ
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ページに %q がありません:\n%s", want, html)
		}
	}

	// 添付は生成IDで files/ に居る。
	entries, err := os.ReadDir(page.AttachmentDir(pageID))
	if err != nil || len(entries) != 1 {
		t.Fatalf("添付が保存されていません: %v %v", entries, err)
	}
	saved, _ := os.ReadFile(filepath.Join(page.AttachmentDir(pageID), entries[0].Name()))
	if string(saved) != string(pdf) {
		t.Errorf("添付の中身が違います: %q", saved)
	}
	if !strings.HasSuffix(entries[0].Name(), ".pdf") {
		t.Errorf("拡張子が保たれていません: %s", entries[0].Name())
	}

	// タグは②汎用索引へ載る（差出人・宛先・受信日時で検索できる）。
	idInt := 0
	database.DB.QueryRow(`SELECT id FROM pages WHERE title = ?`, "発注書送付の件").Scan(&idInt)
	rows := queryTags(t, idInt)
	joined := strings.Join(rows, "|")
	for _, want := range []string{"差出人=", "宛先=order@example.co.jp", "受信日時=" + wantDate} {
		if !strings.Contains(joined, want) {
			t.Errorf("タグ索引に %q がありません: %v", want, rows)
		}
	}

	// 親は受信箱（サイドカーが正本）。
	meta, ok := page.ReadSidecar(pageID)
	if !ok || meta.ParentID != inbox {
		t.Errorf("親が受信箱ではありません: %+v", meta)
	}
	if meta.Owner != "alice" || meta.Group != "team" {
		t.Errorf("所有者・グループの継承が違います: %+v", meta)
	}
}

// TestIntakeContextLeastPrivilege は、取り込みの道具が「この取り込みで作った
// ページ」以外へ書けないことを検証します（既存ページの改変口にならない・検査で効く線）。
func TestIntakeContextLeastPrivilege(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)
	ctx := &IntakeContext{InboxID: inbox, Uploader: "alice"}
	if err := ctx.UpdatePage("000001", "<h1>乗っ取り</h1>"); err == nil {
		t.Error("作っていないページを書き直せてしまいました")
	}
	if _, _, err := ctx.SaveAttachment("000001", ".txt", []byte("x")); err == nil {
		t.Error("作っていないページへ添付できてしまいました")
	}
}

// TestEmlIntakeRejectsBroken は、メールとして読めないファイルがエラーになる
// （中途半端に取り込まない）ことを検証します。
func TestEmlIntakeRejectsBroken(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)
	ctx := &IntakeContext{InboxID: inbox, Uploader: "alice"}
	if _, _, err := (emlIntake{}).OnFile(ctx, "x.eml", []byte("これはメールではない")); err == nil {
		t.Error("壊れたファイルが取り込まれました")
	}
}

// ── 重複検知（source_ref・【考察】通信記録処理.md §8・§10-2） ────────────

// buildEml は Message-ID つきの最小のメールを組み立てます。
func buildEml(messageID, subject string) string {
	s := "From: sender@example.jp\r\n" +
		"To: order@example.co.jp\r\n" +
		"Subject: " + subject + "\r\n" +
		"Date: Mon, 01 Sep 2026 10:30:00 +0900\r\n"
	if messageID != "" {
		s += "Message-ID: " + messageID + "\r\n"
	}
	return s + "\r\nhonbun\r\n"
}

// TestEmlIntakeWritesMessageID は、重複検知の鍵が**見える文字として**本文に
// 書かれ、索引から引けることを固定します。専用テーブルは持たない（D-1）ので、
// 鍵の置き場は可変タグ以外にありません。
func TestEmlIntakeWritesMessageID(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)

	ctx := &IntakeContext{InboxID: inbox, Uploader: "alice"}
	pageID, _, err := emlIntake{}.OnFile(ctx, "m.eml", []byte(buildEml("<abc123@example.jp>", "件名")))
	if err != nil {
		t.Fatalf("取り込みエラー: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(page.GetPageDir(pageID), pageID+".html"))
	if err != nil {
		t.Fatalf("作られたページを読めません: %v", err)
	}
	if !strings.Contains(string(body), "<dt>メッセージID</dt><dd>&lt;abc123@example.jp&gt;</dd>") {
		t.Errorf("メッセージIDのタグがありません:\n%s", body)
	}
	// 索引から引ける（逆引きは生テキスト・pagesByTag）
	ids, err := pagesByTag(database.DB, "メッセージID", "<abc123@example.jp>")
	if err != nil {
		t.Fatalf("逆引きエラー: %v", err)
	}
	if len(ids) != 1 {
		t.Errorf("索引から引けません: %v", ids)
	}
}

// TestEmlIntakeDetectsDuplicate は、同じメールを2度落としても記録が二重に
// ならないことを固定します。
//
// 受信箱へ同じ .eml をもう一度ドロップするのは**普通に起きる**（送り直し・
// 取りこぼしの確認）。黙って2枚できると、どちらが正かを人が見分けられない
// ——しかも参照タグ `受信元` の指す先が2つに割れる。
//
// 判定は**通信記録ページの存在だけ**で行う（§2.7）。「このメールから受注ページを
// 作ったか」は使わない——使うと、受注ページを消したあとに再実行できなくなる。
func TestEmlIntakeDetectsDuplicate(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)

	eml := []byte(buildEml("<dup@example.jp>", "同じメール"))
	ctx := &IntakeContext{InboxID: inbox, Uploader: "alice"}
	first, _, err := emlIntake{}.OnFile(ctx, "m.eml", eml)
	if err != nil {
		t.Fatalf("1回目の取り込みエラー: %v", err)
	}

	// 2回目: 取り込み係を呼ぶ前に、コアが鍵で既存ページを見つける
	name, value, ok := emlIntake{}.SourceRef("m.eml", eml)
	if !ok || name != "メッセージID" || value != "<dup@example.jp>" {
		t.Fatalf("鍵の取り出しが違います: %q %q ok=%v", name, value, ok)
	}
	existing, dup := ExistingIntakePage(name, value)
	if !dup {
		t.Fatal("2回目が重複として検出されません")
	}
	if existing != first {
		t.Errorf("重複の指す先が違います: got %q want %q", existing, first)
	}
}

// TestEmlIntakeWithoutMessageIDStillWorks は、Message-ID の無いメールでも
// 取り込みが止まらないことを固定します。**鍵が無いことは異常ではない**
// （手で組んだメール・一部のFAXゲートウェイ）——重複検知が効かないだけで、
// 記録が残らないほうが困ります。
func TestEmlIntakeWithoutMessageIDStillWorks(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)

	eml := []byte(buildEml("", "鍵なし"))
	if _, _, ok := (emlIntake{}).SourceRef("m.eml", eml); ok {
		t.Error("Message-ID が無いのに鍵があると答えました")
	}
	ctx := &IntakeContext{InboxID: inbox, Uploader: "alice"}
	pageID, title, err := emlIntake{}.OnFile(ctx, "m.eml", eml)
	if err != nil {
		t.Fatalf("取り込みエラー: %v", err)
	}
	if pageID == "" || title != "鍵なし" {
		t.Errorf("鍵が無くても取り込めるべきです: %q %q", pageID, title)
	}
}
