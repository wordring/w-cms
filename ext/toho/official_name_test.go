package toho

import (
	"strings"
	"testing"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/cms/page"
)

// 業者・客先へ出すものの宛名は、連絡帳の組織の「正式名称」（2026-10-23）——部材発注書のメールの件名と本文。
// 宛先のアドレスを引く・タグに残すのは短い名前のまま。
// > 2026-10-23 利用者:「さらに、業者への見積依頼や発注書も正式名称が必要です。」
func TestOrderMailUsesOfficialName(t *testing.T) {
	setupExtTest(t, "000990", page.PageMeta{Owner: "root", Mode: "330"})
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 60, 0, contacts.ContactsBoxTitle, "root", "302", true)
	addPage(t, 61, 60, "みなと商店", "root", "302", true)
	syncBody(t, 61, `<h1>みなと商店</h1><dl data-type="tags"><dt>`+contacts.OfficialNameTag+`</dt><dd>株式会社みなと商店</dd></dl>`)
	addPage(t, 62, 0, "発注 みなと商店", "root", "302", true)
	body := `<h1>部材発注</h1><dl data-type="tags"><dt>` + SupplierTag + `</dt><dd>みなと商店</dd></dl>` +
		`<table><caption>部材発注明細表</caption><tbody><tr><th>弊社品番</th><th>品名</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td></td><td>ボルト</td><td>4</td><td>未発注</td></tr></tbody></table>`
	writeBodyFile(t, 62, body)
	syncBody(t, 62, body)
	d, err := orderMailDefaults(adminUser(), "000062")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Subject, "株式会社みなと商店　御中") || !strings.HasPrefix(d.Body, "株式会社みなと商店　御中") {
		t.Errorf("⚠ 部材発注書のメールの宛名が正式名称ではありません: 件名 %q／本文 %q", d.Subject, d.Body)
	}
	if got := contacts.OfficialName(adminUser(), "みなと商店"); got != "株式会社みなと商店" {
		t.Errorf("正式名称を引けません: %q", got)
	}
}
