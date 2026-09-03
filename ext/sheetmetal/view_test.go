package sheetmetal

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// TestRequiredMaterialsViewRenders は手配状況リストの事前描画を固定します。
//
// コア側（vocab_stage4_test.go）は子ページ一覧までしか見ません——このビューは
// 拡張が `RegisterView` で持ち込むもので、`-tags minimal` では存在しないためです。
// **拡張が入っているときに中身が出る**ことは、拡張のテストが受け持ちます。
func TestRequiredMaterialsViewRenders(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedSecretMaterial(t, 3, false, "alice")

	body := `<h1>受注</h1><section data-type="required-materials" data-id="v2"></section>`
	req := httptest.NewRequest("GET", "/000003", nil)
	req = auth.WithUser(req, &auth.User{Username: "root", IsAdmin: true})
	out := cms.RenderComputedViews(req, 3, body)

	for _, want := range []string{
		`data-type="required-materials"`, // マーカーは保存内容のまま
		`class="vocab-chrome"`,           // 中身はクローム（保存されない）
		`部材手配・発注進捗状況`,
		`極秘部材`,
		`<td class="num">6</td>`, // 1個あたり2 × 受注3個
		// **仕入先はここには出ません**——構成部品の表から仕入先を外したため
		// （2026-09-03 ユーザー:「仕入れ先は複数あります」）。埋まるのは発注実績が
		// 付いたときで、仕入先の正本は発注書のヘッダと材料屋の見積もりです。
		`<td>-</td>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("描画結果に %q がありません:\n%s", want, out)
		}
	}
	// 登録漏れの告知（missingViewHTML）が出ていないこと＝RegisterView が効いている。
	if strings.Contains(out, "まだ用意されていません") {
		t.Errorf("ビューの描画が登録されていません:\n%s", out)
	}
}
