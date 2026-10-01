package toho

import (
	"encoding/json"
	"strings"
	"testing"

	"w-cms/internal/cms"
)

// TestSettingsRepoFileHasProductKinds は、**リポジトリの設定から加工製品の区分が読める**ことを
// 固定します（2026-09-29 に段〔`machine_stages`〕をやめて、語彙 `区分` にした）。
// 読めないと整理の画面に区分の印が1つも出ず、一覧でも絞れません。
func TestSettingsRepoFileHasProductKinds(t *testing.T) {
	// TestMain（settings_repo_test.go）がリポジトリの設定を読み込み済み。
	kinds := ProductKinds()
	for _, want := range []string{"試作", "見積", "旧型"} {
		if !contains(kinds, want) {
			t.Errorf("語彙の区分に %q がありません: %v", want, kinds)
		}
	}
	// ⚠ **構成部品の表の `区分` は現行／廃版のまま**（表ごとの例外）——既定を加工製品の区分に
	//    したので、例外が効いていないと材料表の「廃版」が薄赤になり、手配の除外も読めなくなる。
	for _, table := range []string{"材料", "外注加工", "購入部品", "支給部品"} {
		if got := strings.Join(cms.ColumnWord(table, ProductKindTag).Values, "・"); got != "現行・廃版" {
			t.Errorf("%s の区分の選択肢が %q です（現行・廃版のはず）", table, got)
		}
	}
}

// TestSettingsRejectsBrokenSection は、壊れた節で**検査が止める**ことを固定します
// （もとはコアの TestSettingsRejectsBrokenFile にあった「段の重複」を、節と一緒に移した）。
func TestSettingsRejectsBrokenSection(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		// ⚠ **段は 2026-09-29 に無くなった**——書いたままの設定は黙って無視せず止める。
		{"無くなった段", `{"machine_stages": ["現行"]}`, "書式が不正"},
		{"打ち間違えたキー", `{"product_code_tag": ["品番"]}`, "書式が不正"},
		{"照合タグの重複", `{"product_code_tags": ["品番", "品番"]}`, "2回"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apply, err := parseSettings(json.RawMessage(tc.raw))
			if err == nil {
				t.Fatal("壊れた節が受け入れられました")
			}
			if apply != nil {
				t.Error("壊れた節なのに効かせる関数を返しています")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("知らせに %q がありません: %v", tc.want, err)
			}
		})
	}
}

// TestSettingsAbsentSectionClearsValues は、**節が無ければ値が空に戻る**ことを固定します
// ——読み直しで節を消した運用者の意図どおり、古い値が残らないように。
func TestSettingsAbsentSectionClearsValues(t *testing.T) {
	// ⚠ **節の全部を戻します**（2026-09-27）。`apply()` は照合タグ・PDFのフォント・
	//    会社の情報も空にします。一部しか戻していなかったので、**後に走る試験へ空の照合タグが
	//    漏れていました**——材料表の宣言が `部品番号` を控えとして持っていたあいだは表に出ず、
	//    宣言を外した日に `TestRequiredMaterialsViewRenders` が全体で流したときだけ落ちました。
	stagesMu.RLock()
	savedTags, savedFont, savedFace, savedCompany :=
		productCodeTags, pdfFont, pdfFontFace, companyInf
	savedKinds, savedPrint, savedHeads := orderKinds, orderPrintColumns, orderPrintHeads
	stagesMu.RUnlock()
	t.Cleanup(func() {
		stagesMu.Lock()
		productCodeTags, pdfFont, pdfFontFace, companyInf =
			savedTags, savedFont, savedFace, savedCompany
		orderKinds, orderPrintColumns, orderPrintHeads = savedKinds, savedPrint, savedHeads
		stagesMu.Unlock()
	})
	apply, err := parseSettings(nil)
	if err != nil {
		t.Fatalf("節が無いのに止まりました: %v", err)
	}
	apply()
	if len(ProductCodeTags()) != 0 {
		t.Errorf("節が無いのに照合タグが残っています: %v", ProductCodeTags())
	}
}

