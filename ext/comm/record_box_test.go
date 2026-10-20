package comm

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 記録の箱（record_box.go・2026-10-20）——通信記録にだけ箱を描き、ほかの拡張が足す節を順に並べ、下書きには「作るページ」を出さない。
func TestRecordBoxRendersSectionsInOrder(t *testing.T) {
	setupIntakeTest(t)
	saved := recordSections
	t.Cleanup(func() { recordSections = saved })
	recordSections = nil
	RegisterRecordSection(RecordSection{Order: 50, Render: func(u *auth.User, id int, draft bool) string { return `<p class="probe-b">B</p>` }})
	RegisterRecordSection(RecordSection{Order: 5, Render: func(u *auth.User, id int, draft bool) string {
		if draft {
			return ""
		}
		return `<p class="probe-a">A</p>`
	}})
	put := func(id, body string) {
		if err := page.WriteSidecar(id, page.PageMeta{Owner: "alice", Mode: "333", ParentID: "000100"}); err != nil {
			t.Fatal(err)
		}
		if err := cms.SyncIndex(id, body); err != nil {
			t.Fatal(err)
		}
	}
	put("000601", `<h1>受信</h1><dl data-type="tags"><dt>`+ChannelTag+`</dt><dd>`+ChannelMail+`</dd></dl>`)
	put("000602", `<h1>下書き</h1><dl data-type="tags"><dt>`+ChannelTag+`</dt><dd>`+ChannelMail+`</dd><dt>`+DraftTag+`</dt><dd>返信</dd></dl>`)
	put("000603", `<h1>ふつうのページ</h1>`)
	u := &auth.User{Username: "alice"}

	got := recordBoxHTML(u, 601)
	if !strings.Contains(got, `class="vocab-chrome mail-chrome"`) || strings.Index(got, "probe-a") > strings.Index(got, "probe-b") || !strings.Contains(got, "probe-a") {
		t.Errorf("⚠ 通信記録の箱に、節が順に並んでいません:\n%s", got)
	}
	if d := recordBoxHTML(u, 602); strings.Contains(d, "probe-a") || !strings.Contains(d, "probe-b") {
		t.Errorf("⚠ 下書きの箱の節が違います（A は下書きに出さない）:\n%s", d)
	}
	if n := recordBoxHTML(u, 603); n != "" {
		t.Errorf("⚠ 通信記録でないページに箱を描いています:\n%s", n)
	}
}
