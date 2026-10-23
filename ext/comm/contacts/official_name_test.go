package contacts

import (
	"testing"

	"w-cms/internal/cms"
)

// 組織の正式名称（2026-10-23・official_name.go）——組織のページのタグ「正式名称」があればそれ、無ければ題のまま。
// 連絡帳に無い名前もそのまま返す（紙の宛名が空にならない）。
func TestOfficialNameFromOrgPage(t *testing.T) {
	user, companyID := setupPartnerTree(t)
	if got := OfficialName(user, "南北スポーツ機械"); got != "南北スポーツ機械" {
		t.Errorf("正式名称の無い組織は題のままのはず: %q", got)
	}
	body := `<h1>南北スポーツ機械</h1><dl data-type="tags"><dt>` + OfficialNameTag + `</dt><dd>株式会社南北スポーツ機械</dd></dl>`
	if err := cms.SyncIndex(companyID, body); err != nil {
		t.Fatal(err)
	}
	if got := OfficialName(user, " 南北スポーツ機械 "); got != "株式会社南北スポーツ機械" {
		t.Errorf("⚠ 組織のページの正式名称を返していません: %q", got)
	}
	if got := OfficialName(user, "連絡帳に無い会社"); got != "連絡帳に無い会社" {
		t.Errorf("⚠ 連絡帳に無い名前が、そのままになっていません: %q", got)
	}
}
