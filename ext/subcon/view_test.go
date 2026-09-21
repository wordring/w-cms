package subcon

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
		// ⚠ **見出しは 2026-09-21 に変わりました**（材料名の合算 →
		// **加工製品ごと**）。ユーザー:「各受注ページに各加工製品ごとの項目と購入品の
		// 表を集める必要があり、その表の列の一つとして、発注書番号と発注書ページへの
		// リンクが必要」。⚠ **見ていることは同じ**です——材料が出て、必要数が積まれる。
		`手配状況（加工製品ごと）`,
		`極秘部材`,
		`<td class="num">6</td>`, // 1個あたり2 × 受注3個
		// ⚠ **手配していないことを黙りません**（空欄だと「発注済み」に見えます）。
		`未手配`,
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
