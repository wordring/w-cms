package comm

import (
	"os"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// TestSetHandledReplacesAndUndoes は、メールのページで対応を**付け替え・外せる**ことを固定します（2026-09-30 利用者:
// 「そのメールへの対応が終わったかどうかは、受信フォルダではなく、メールページで選択したいです」）。
//
//   - 済 → 不要 に付け替えると、印は1つのまま値だけ変わる（2つ並ばない）
//   - 未処理（外す）で印が消え、未処理の一覧へ戻る
//   - 字下げされた本文（エディタで保存したもの）でも当たる
func TestSetHandledReplacesAndUndoes(t *testing.T) {
	setupIntakeTest(t)
	const id = "000280"
	if err := page.WriteSidecar(id, page.PageMeta{Owner: "alice", Mode: "330", ParentID: "000100"}); err != nil {
		t.Fatal(err)
	}
	body := "<h1>見積の件</h1>\n<dl data-type=\"tags\">\n    <dt>" + ChannelTag + "</dt>\n    <dd>" + ChannelMail +
		"</dd>\n    <dt>" + HandledTag + "</dt>\n    <dd>" + HandledDone + "</dd>\n</dl>\n<p>本文</p>"
	if err := os.MkdirAll(page.GetPageDir(id), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page.BodyPath(id), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cms.SyncIndex(id, body); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		b, err := cms.ReadPageBody(id)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	if err := SetHandled(id, "alice", HandledNotNeeded); err != nil {
		t.Fatal(err)
	}
	got := read()
	if strings.Count(got, "<dt>"+HandledTag+"</dt>") != 1 || !strings.Contains(got, "<dd>"+HandledNotNeeded+"</dd>") ||
		strings.Contains(got, "<dd>"+HandledDone+"</dd>") {
		t.Errorf("済 → 不要 に付け替わっていません（印は1つのはず）:\n%s", got)
	}

	if err := SetHandled(id, "alice", HandledUndo); err != nil {
		t.Fatal(err)
	}
	if got := read(); strings.Contains(got, HandledTag) {
		t.Errorf("外したのに印が残っています:\n%s", got)
	}
	if !strings.Contains(read(), "<dt>"+ChannelTag+"</dt>") {
		t.Errorf("ほかのタグまで消えています:\n%s", read())
	}
	// 未処理の一覧へ戻る。
	rows, _, err := UnhandledIntakes(&auth.User{Username: "alice", IsAdmin: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		found = found || r.PageID == id
	}
	if !found {
		t.Errorf("外したのに未処理の一覧に戻っていません")
	}

	if err := SetHandled(id, "alice", "完了"); err == nil {
		t.Errorf("決まっていない値を受け付けました")
	}
}
