package cms

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// 書ける形でも写真の住所に版の印を添える（2026-10-27・【考察】キャッシュ制御 案A）。
// 利用者:「【考察】キャッシュ制御の勧める順で作ってください。公開の添付は10分にとどめる（勧め）でお願いします。」

// TestStripAttachmentVersions は、サニタイズが写真の住所の版の印だけを外し、それを「変化した」に数えないことを固定します。
// ほかの問い合わせの付いた住所・リンクには触りません。
func TestStripAttachmentVersions(t *testing.T) {
	in := `<p data-id="aaaa"><img src="/000901/ph21.jpg?v=muno1aa0" alt="現場"/><img src="/000901/b.png?x=1"/><a href="/000901/c.pdf?v=abc">c</a></p>`
	got := Sanitize(in)
	for _, want := range []string{`src="/000901/ph21.jpg"`, `src="/000901/b.png?x=1"`, `href="/000901/c.pdf?v=abc"`} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ %q がありません: %s", want, got)
		}
	}
	if strings.Contains(got, "ph21.jpg?v=") {
		t.Errorf("⚠ 写真の版の印が本文に残ります: %s", got)
	}
	clean := `<p data-id="aaaa"><img src="/000901/ph21.jpg" alt="現場"/></p>`
	if _, changed := SanitizeReport(strings.Replace(clean, `.jpg"`, `.jpg?v=muno1aa0"`, 1)); changed {
		t.Errorf("⚠ 版の印を外しただけで「サニタイズで変わった」と数えています（書き手に知らせが出る）")
	}
}

// TestEditingBodyCarriesAttachmentVersions は、書くための本文の口（素の /api/load・/api/sync の書き出し）が写真の住所に
// 版の印を添え、画面がその書き出しのまま送り返しても、本文には印が残らず、ほかの人とぶつからないことを固定します。
func TestEditingBodyCarriesAttachmentVersions(t *testing.T) {
	setupSaveTest(t)
	const id = "000901"
	setupSyncPage(t, id, `<h1 data-id="h1h1">題</h1><p data-id="aaaa"><img src="/000901/ph21.jpg"/>写真</p><p data-id="bbbb">B</p>`)
	p := putAttachment(t, id, "ph21.jpg", noisyJPEG(t, 300, 200))
	fi, _ := os.Stat(p)
	src := `/000901/ph21.jpg?v=` + page.AttachmentVersion(fi)

	// 素の /api/load
	r := auth.WithUser(httptest.NewRequest("GET", "/api/load?id="+id, nil), &auth.User{Username: "alice", IsAdmin: true})
	rr := httptest.NewRecorder()
	LoadAPIHandler(rr, r)
	if !strings.Contains(rr.Body.String(), `src="`+src+`"`) {
		t.Errorf("⚠ 素の /api/load の写真に版の印がありません: %s", rr.Body.String())
	}

	// /api/sync の書き出し（開いたとき）
	alice := openScreen(t, "alice", id)
	bob := openScreen(t, "bob", id)
	if !strings.Contains(alice.html["aaaa"], `src="`+src+`"`) {
		t.Fatalf("⚠ /api/sync の書き出しの写真に版の印がありません: %s", alice.html["aaaa"])
	}

	// 画面は書き出し（印つき）のまま、写真のあるブロックに書き足して送る。
	edited := strings.Replace(alice.html["aaaa"], "写真", "写真の説明", 1)
	if code, r := alice.edit(t, "aaaa", edited); code != 200 || !r.Written || len(r.Conflicts) != 0 {
		t.Fatalf("alice が書けない: %d %+v", code, r)
	}
	body := bodyOfFile(t, id)
	if !strings.Contains(body, "写真の説明") || strings.Contains(body, "?v=") || !strings.Contains(body, `src="/000901/ph21.jpg"`) {
		t.Fatalf("⚠ 本文に版の印が残るか、書き足しが入っていません: %s", body)
	}

	// bob は違うブロックを書く——写真のあるブロックの印のせいでぶつからない。
	if code, r := bob.edit(t, "bbbb", `<p data-id="bbbb">B-bob</p>`); code != 200 || len(r.Conflicts) != 0 {
		t.Fatalf("⚠ 違うブロックなのにぶつかった: %d %+v", code, r.Conflicts)
	}
	// alice がもう一度、印つきの書き出しで写真のブロックを何も変えずに送っても、書かない（変わっていない）。
	alice2 := openScreen(t, "alice", id)
	if code, r := alice2.edit(t, "aaaa", alice2.html["aaaa"]); code != 200 || len(r.Conflicts) != 0 {
		t.Fatalf("⚠ 何も変えていないのにぶつかった: %d %+v", code, r.Conflicts)
	}
	if got := bodyOfFile(t, id); strings.Contains(got, "?v=") {
		t.Fatalf("⚠ 本文に版の印が残りました: %s", got)
	}
}
