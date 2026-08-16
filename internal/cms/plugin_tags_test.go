package cms

import (
	"strings"
	"testing"

	"w-cms/internal/cms/htmldoc"
)

// findSpec は要素名で TagSpec を探します。
func findSpec(specs []TagSpec, element string) (TagSpec, bool) {
	for _, s := range specs {
		if s.Element == element {
			return s, true
		}
	}
	return TagSpec{}, false
}

// TestPluginTagsMergesAcrossPlugins は、同じ要素を複数のプラグインが宣言したときに
// 属性が**和集合**になることを検証します。<m-item> は顧客の発注書（item-id/price）と
// 弊社の発注書（cost）の双方が使うため、どちらの属性も落ちてはいけません。
func TestPluginTagsMergesAcrossPlugins(t *testing.T) {
	specs := PluginTags()

	item, ok := findSpec(specs, "m-item")
	if !ok {
		t.Fatal("m-item がどのプラグインからも宣言されていません")
	}
	for _, want := range []string{"item-id", "item-name", "price", "cost", "quantity", "status"} {
		if !contains(item.Attributes, want) {
			t.Errorf("m-item の属性 %q が和集合に含まれていません: %v", want, item.Attributes)
		}
	}
}

// TestPluginTagsCoversPluginReads は、プラグインが Sync で実際に読む属性が
// すべて宣言されていることを検証します。宣言漏れは「保存のたびに値が静かに消える」
// 不具合になるため（過去に m-file の item-name で発生）、代表的な属性を明示的に確認します。
func TestPluginTagsCoversPluginReads(t *testing.T) {
	specs := PluginTags()

	cases := []struct {
		element string
		attrs   []string
		why     string
	}{
		{"m-file", []string{"src", "name", "ext"}, "ファイル容器 (plugin_file)"},
		{"m-client-order", []string{"order-no", "client-name", "ordered-at"}, "顧客の発注書 (plugin_client_order)"},
		{"m-supplier-order", []string{"order-no", "supplier-name", "ordered-at"}, "弊社の発注書 (plugin_our_order)"},
		{"m-our-estimate", []string{"item-id", "client-name", "price", "estimated-at"}, "弊社の見積もり (plugin_estimates)"},
		{"m-supplier-estimate", []string{"item-name", "supplier-name", "cost", "estimated-at"}, "材料屋の見積もり (plugin_estimates)"},
		{"m-material", []string{"item-name", "cost", "supplier-name", "quantity"}, "部材 (plugin_materials)"},
		{"m-required-materials", []string{"page-id"}, "手配状況リスト (plugin_materials)"},
	}

	for _, c := range cases {
		spec, ok := findSpec(specs, c.element)
		if !ok {
			t.Errorf("%s: 要素 %q が宣言されていません", c.why, c.element)
			continue
		}
		for _, a := range c.attrs {
			if !contains(spec.Attributes, a) {
				t.Errorf("%s: %s の属性 %q が宣言されていません: %v", c.why, c.element, a, spec.Attributes)
			}
		}
	}
}

// TestPluginTagsIsSorted は戻り値が安定した順序であることを検証します
// （/api/tag-schema の応答が呼び出しごとに変わらないようにするため）。
func TestPluginTagsIsSorted(t *testing.T) {
	specs := PluginTags()
	for i := 1; i < len(specs); i++ {
		if specs[i-1].Element > specs[i].Element {
			t.Errorf("要素名がソートされていません: %q の後に %q", specs[i-1].Element, specs[i].Element)
		}
	}
	for _, s := range specs {
		for i := 1; i < len(s.Attributes); i++ {
			if s.Attributes[i-1] > s.Attributes[i] {
				t.Errorf("%s の属性がソートされていません: %v", s.Element, s.Attributes)
				break
			}
		}
	}
}

// TestSanitizerAllowlistComesFromPlugins は、サニタイズの許可リストが
// プラグインの宣言から組み上がっていることを検証します。
// サニタイザ自身はカスタム要素の語彙を持ちません。
func TestSanitizerAllowlistComesFromPlugins(t *testing.T) {
	allowed := AllowedVocabulary()

	for _, spec := range PluginTags() {
		attrs, ok := allowed[spec.Element]
		if !ok {
			t.Errorf("プラグインが宣言した要素 %q が許可リストにありません", spec.Element)
			continue
		}
		for _, a := range spec.Attributes {
			if !contains(attrs, a) {
				t.Errorf("プラグインが宣言した属性 %s[%s] が許可リストにありません", spec.Element, a)
			}
		}
	}

	// 構造HTMLはコア側が持ち続ける
	for _, el := range []string{"h1", "p", "table", "a"} {
		if _, ok := allowed[el]; !ok {
			t.Errorf("構造HTMLの %q が許可リストから消えています", el)
		}
	}
}

// TestSanitizerHasNoCustomVocabulary は、サニタイザのコア定義（htmldoc の
// structuralElements）にカスタム要素が1つも直書きされていないことを検証します。
// 「カスタムタグはすべてプラグインが所有する」という線引きを回帰から守ります。
// htmldoc.New(nil) は注入なし＝構造HTMLだけの語彙を返すので、それを検査します。
func TestSanitizerHasNoCustomVocabulary(t *testing.T) {
	for el := range htmldoc.New(nil).AllowedVocabulary() {
		if strings.HasPrefix(el, "m-") {
			t.Errorf("カスタム要素 %q がサニタイザに直書きされています（プラグインの Tags() で宣言してください）", el)
		}
	}

	// 移設した要素がプラグイン側から供給されていることも確認する
	for _, el := range []string{"m-tag", "m-child-list"} {
		if _, ok := findSpec(PluginTags(), el); !ok {
			t.Errorf("%q を宣言するプラグインがありません", el)
		}
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
