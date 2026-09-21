package subcon

import (
	"testing"

	"w-cms/internal/cms"
)

// **自社の発注書の明細は、実物7枚から採った1つの形**（2026-09-21）。
//
// 実物（材料2／加工2／塗装2／鍍金1）の見出し:
//
//	材料        … 材質｜形状｜寸法｜単位｜数量｜単価｜金額（税抜）
//	加工        … 商品コード｜商品名｜単位｜数量｜単価｜金額（税抜）
//	塗装・鍍金  … 商品コード｜商品名｜**色**｜単位｜数量｜単価｜金額（税抜）
//
// ⚠ **塗装と鍍金は完全に同じ形**で、**加工はそこから `色` が抜けただけ**。
// 違うのは**材料か、そうでないか**の1点だけなので、**形式は1つ**にしました
// （ユーザー:「まとめる方法があればまとめたい」）。

// TestOurOrderItemsCoverRealDocuments は、**実物の列がすべて入っている**ことを
// 固定します。
//
// ⚠ **1つでも欠けると、その種類の発注書が組めません**——そして欠けたことは
// 「なぜか列が出ない」という形でしか現れません。
func TestOurOrderItemsCoverRealDocuments(t *testing.T) {
	def, ok := cms.VocabDefByType("our-order-items")
	if !ok {
		t.Fatal("発注明細の宣言がありません")
	}
	have := map[string]cms.ColumnType{}
	for _, c := range def.Columns {
		have[c.Label] = c.Type
	}
	// 実物にあった列（4種類ぶんの合併）。
	for _, want := range []string{
		"品番", "品名", "数量", "単位", "単価", // 全種類に共通
		"材質", "形状", "寸法", // 材料だけ
		"色",           // 塗装・鍍金だけ
		"備考", "状態", "弊社品番", // 弊社の管理
	} {
		if _, ok := have[want]; !ok {
			t.Errorf("⚠ 実物にある列 %q がありません（その種類の発注書が組めません）", want)
		}
	}
}

// TestOurOrderItemsMatchClientOrderNames は、⚠ **受注明細と名前・型が揃っている**
// ことを固定します。
//
// ⚠ **同じものを2つの名前で呼ぶと、横断検索が片方を取りこぼします。** 索引は
// **見出しの表示文字**で引くので、`品番` と `商品コード` が混在すると、
// 「この品番の履歴」を出したいときに**どちらか半分しか出ません**——そして
// **エラーにはならず、ただ少なく出ます**。
//
// ⚠ 型も揃える必要があります。`品番` が片方 `code`・片方 `text` だと、
// **畳み方が違って突き合わせが静かに外れます**（`code` は長音と大小を畳む）。
func TestOurOrderItemsMatchClientOrderNames(t *testing.T) {
	ours, ok1 := cms.VocabDefByType("our-order-items")
	theirs, ok2 := cms.VocabDefByType(clientOrderItemsType)
	if !ok1 || !ok2 {
		t.Fatal("宣言が足りません")
	}
	typeOf := func(def cms.VocabDef, label string) (cms.ColumnType, bool) {
		for _, c := range def.Columns {
			if c.Label == label {
				return c.Type, true
			}
		}
		return "", false
	}
	// 両方に出る列は、型まで同じであること。
	for _, label := range []string{"弊社品番", "品番", "品名", "数量", "単位", "単価", "備考"} {
		a, okA := typeOf(ours, label)
		b, okB := typeOf(theirs, label)
		if !okA || !okB {
			t.Errorf("%q が片方にしかありません（発注 %v／受注 %v）", label, okA, okB)
			continue
		}
		if a != b {
			t.Errorf("⚠ %q の型が食い違っています（発注 %q／受注 %q）——畳み方が違うと"+
				"突き合わせが静かに外れます", label, a, b)
		}
	}
}

// TestOurOrderStatusIsTwoValues は、**発注の状態は2つだけ**であることを固定します。
//
// ⚠ 受注明細の `状態`（未着手／加工中／検査中／納品済／完了）と**別物**です——
// こちらは**相手がまだ納めていないか、納めたか**の2値。混ぜると、`手配状況リスト` の
// 発注済の数え方が変わります。
func TestOurOrderStatusIsTwoValues(t *testing.T) {
	def, _ := cms.VocabDefByType("our-order-items")
	for _, c := range def.Columns {
		if c.Label != "状態" {
			continue
		}
		if len(c.Enum) != 2 || c.Enum[0] != "未納品" || c.Enum[1] != "納品済" {
			t.Errorf("発注の状態が %v です（未納品／納品済 の2つを期待）", c.Enum)
		}
		return
	}
	t.Fatal("状態の列がありません")
}
