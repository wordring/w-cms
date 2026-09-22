package subcon

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
	// ⚠ **`drawing` は 2026-09-18 に廃しました**——図面番号・図面名称・装置名称・客先は
	// 可変タグ（`page_tags`）へ移りました。横断検索の口が読む表はそちらだからです。
	// ⚠ **ヘッダだけの形式は 2026-09-18 に全廃しました**（`drawing`・`client-order`・
	// `our-order`・`our-estimate`・`supplier-estimate`）。値は**可変タグ**（`page_tags`）へ
	// 移り、残るのは**行が並ぶ表**と**ビューの器**だけです——「タグと表だけがDBに入る」。
	// ⚠ **`part-estimate` は 2026-09-21 に足しました**（ユーザー:「表のうち、
	// 意味の分かるものにキャプションを付けましょう。**キャプションを頼りに集計できる
	// ようになるはず**です」）。⚠ **キャプションだけでは索引に入りません**
	// ——「登録された語彙だけDBに入る」ので、ここに載せて初めて集計できます。
	want := []string{
		"part-materials", "client-order-items", "our-order-items",
		"required-materials", "drawing-revisions", "drawing-revision-items",
		"part-outsourcing", "part-purchased", "part-supplied", "part-estimate",
		// **発注部材表**（2026-09-22）——未発注の表から作り、人が足し引きしてから
		// 発注書にする。⚠ **列は発注明細と同じ**（`orderItemColumns`）。
		OrderDraftType,
		// **発注書へのリンク**（2026-09-22）——発注部材表が化けた1行。
		// ⚠ **本文に書くのはリンクと行数だけ**で、進み具合は鏡が読み直します。
		OrderLinkType,
		// ⚠ **ビューの器**（2026-09-21）——材質・形状・寸法で材料を探す欄。
		MaterialSearchViewType, UnorderedViewType,
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

// TestOrderDraftSharesColumnsWithOrderItems は、⚠ **発注部材表と発注明細の列が
// 同じ**であることを固定します（2026-09-22）。
//
// ⚠ **片方だけ列を足すと、発注部材表から発注書へ移すときに落ちます**——しかも
// エラーは出ません（索引は見出しの表示文字で引くので、黙ってずれるだけ）。
func TestOrderDraftSharesColumnsWithOrderItems(t *testing.T) {
	draft := columnsOf(OrderDraftType)
	items := columnsOf(ourOrderItemsType)
	if len(draft) != len(items) {
		t.Fatalf("列の数が違います: 発注部材表 %d / 発注明細 %d", len(draft), len(items))
	}
	for i := range draft {
		if draft[i].Label != items[i].Label || draft[i].Field != items[i].Field ||
			draft[i].Type != items[i].Type {
			t.Errorf("%d列目が違います: %#v / %#v", i, draft[i], items[i])
		}
	}
	// ⚠ **スライスを共有していないこと**——片方を書き換えたらもう片方も変わる、
	//    という壊れ方は**気づくのが何か月も後**になります。
	if len(draft) > 0 && &draft[0] == &items[0] {
		t.Error("⚠ 同じスライスを返しています（片方の書き換えがもう片方に及びます）")
	}
}
