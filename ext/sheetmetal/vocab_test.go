package sheetmetal

import (
	"regexp"
	"testing"

	"w-cms/internal/cms"
)

// 拡張が持ち込む語彙そのものの検査。
//
// コア側の TestVocabRegistryIsWellFormed は**テスト用の写し**
// （internal/cms/vocab_fixture_test.go）を見ているので、**本物の宣言を検証するのは
// ここだけ**です。写しがずれても構わない代わりに、正本はこちらで押さえます。

var extKebabRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// TestBusinessVocabIsWellFormed は宣言の形を固定します。
func TestBusinessVocabIsWellFormed(t *testing.T) {
	for _, d := range businessVocab {
		if !extKebabRe.MatchString(d.Type) {
			t.Errorf("data-type %q が kebab-case ではありません", d.Type)
		}
		if d.DisplayName == "" {
			t.Errorf("%s: 表示名がありません", d.Type)
		}
		if d.Element != "table" && d.Element != "dl" && d.Element != "section" {
			t.Errorf("%s: element %q は table / dl / section のいずれか", d.Type, d.Element)
		}
		if len(d.Columns) == 0 && d.Element != "section" {
			t.Errorf("%s: 列が1つもありません", d.Type)
		}
		// Items は実在する table 形式を指すこと（明細の解釈がここで決まる）。
		if d.Items != "" {
			ref, ok := cms.VocabDefByType(d.Items)
			if !ok || ref.Element != "table" {
				t.Errorf("%s: Items %q が table 形式として見つかりません", d.Type, d.Items)
			}
		}
		for _, c := range d.Columns {
			if c.Label == "" {
				t.Errorf("%s: ラベルの無い列があります", d.Type)
			}
			if c.Type == cms.ColEnum && len(c.Enum) == 0 {
				t.Errorf("%s.%s: enum 列に選択肢がありません", d.Type, c.Label)
			}
		}
	}
}

// TestBusinessVocabIsRegistered は、拡張の init が実際にコアへ登録したことを固定します。
// **View を宣言した形式は描画も登録されていること**——宣言だけして描画を忘れると、
// 画面に「まだ用意されていません」が出たまま気づかれません。
func TestBusinessVocabIsRegistered(t *testing.T) {
	want := []string{
		"part-materials", "client-order", "client-order-items",
		"our-order", "our-order-items", "our-estimate", "supplier-estimate",
		"required-materials",
	}
	if len(businessVocab) != len(want) {
		t.Errorf("形式の数が変わりました: %d (期待 %d)", len(businessVocab), len(want))
	}
	for _, typ := range want {
		if _, ok := cms.VocabDefByType(typ); !ok {
			t.Errorf("%s がコアへ登録されていません", typ)
		}
	}
}
