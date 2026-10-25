package contacts

import (
	"testing"

	"w-cms/internal/cms"
)

// 相手ごとの送り方の勧め（2026-10-26・send_way.go）——探す順は、人の書類ごと → 人の総称 → 組織の書類ごと → 組織の総称。
// 3つのどれでもない値は、書いていないものとして次を見る。連絡帳に無い組織は勧め無し。
func TestPreferredSendWayOrder(t *testing.T) {
	user, companyID := setupPartnerTree(t)
	personID, err := EnsureContactPerson(user, companyID, "山田 太郎")
	if err != nil {
		t.Fatal(err)
	}
	tags := func(title string, kv ...string) string {
		b := `<h1>` + title + `</h1><dl data-type="tags">`
		for i := 0; i+1 < len(kv); i += 2 {
			b += `<dt>` + kv[i] + `</dt><dd>` + kv[i+1] + `</dd>`
		}
		return b + `</dl>`
	}
	set := func(id, body string) {
		if err := cms.SyncIndex(id, body); err != nil {
			t.Fatal(err)
		}
	}
	check := func(label, person, doc, want, wantFrom, wantTag string) {
		t.Helper()
		got := PreferredSendWay(user, "南北スポーツ機械", person, doc)
		if got.Way != want || (want != "" && (got.From != wantFrom || got.Tag != wantTag)) {
			t.Errorf("%s: %+v（%s・%s・%s のはず）", label, got, want, wantFrom, wantTag)
		}
	}

	check("どこにも書いていない", "山田 太郎", "見積書", "", "", "")
	set(companyID, tags("南北スポーツ機械", SendWayTag, SendWayFax, "見積書の送り方", SendWayMail))
	check("組織の総称", "", "部材発注書", SendWayFax, "南北スポーツ機械", SendWayTag)
	check("組織の書類ごとが総称より先", "", "見積書", SendWayMail, "南北スポーツ機械", "見積書の送り方")
	check("人に書いていなければ組織", "山田 太郎", "部材発注書", SendWayFax, "南北スポーツ機械", SendWayTag)
	check("連絡帳に居ない人は組織", "知らない人", "見積書", SendWayMail, "南北スポーツ機械", "見積書の送り方")

	set(personID, tags("山田 太郎", SendWayTag, SendWayHand))
	check("人の総称が組織の書類ごとより先", "山田 太郎", "見積書", SendWayHand, "山田 太郎", SendWayTag)
	set(personID, tags("山田 太郎", SendWayTag, SendWayHand, "部材発注書の送り方", SendWayMail))
	check("人の書類ごとが人の総称より先", "山田 太郎", "部材発注書", SendWayMail, "山田 太郎", "部材発注書の送り方")
	set(personID, tags("山田 太郎", "部材発注書の送り方", "電報"))
	check("読めない値は飛ばして次", "山田 太郎", "部材発注書", SendWayFax, "南北スポーツ機械", SendWayTag)

	if got := PreferredSendWay(user, "連絡帳に無い会社", "", "見積書"); got.Way != "" {
		t.Errorf("連絡帳に無い会社に勧めがあります: %+v", got)
	}
}
