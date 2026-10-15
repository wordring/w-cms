package toho

import (
	"strings"
	"testing"
)

// TestKindMarkSeparatesKindFromCaption は、必要部材表の種類の札が**種類の名前**で決まることを固定します
// （2026-10-15）。表のキャプションを「材料表」にしたので、種類「材料」と表の名前は別の言葉になりました——
// 種類を表の表示名と比べると、材料の行にまで札が付きます。
func TestKindMarkSeparatesKindFromCaption(t *testing.T) {
	// TestMain（settings_repo_test.go）がリポジトリの設定を読み込み済み。
	if got := orderKindNameOfTable(partMaterialsType); got != "材料" {
		t.Fatalf("材料表から引いた種類が %q です（「材料」のはず）", got)
	}
	if got := orderKindNameOfTable(partOutsourcingType); got != "外注加工" {
		t.Errorf("外注加工表から引いた種類が %q です（「外注加工」のはず）", got)
	}
	if m := unorderedKindMark(UnorderedItem{Kind: "材料"}); m != "" {
		t.Errorf("⚠ 材料の行に種類の札が付いています: %s", m)
	}
	if m := unorderedKindMark(UnorderedItem{Kind: "外注加工"}); !strings.Contains(m, "外注加工") {
		t.Errorf("外注加工の行に種類の札がありません: %q", m)
	}
}
