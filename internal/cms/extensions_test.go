package cms

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// withExtension は試験のあいだだけ拡張を名簿に載せます。
func withExtension(t *testing.T, id, name string) {
	t.Helper()
	RegisterExtension(id, name)
	t.Cleanup(func() { delete(extensionRegistry, id) })
}

// TestExtensionsSortedAndNamed は、名簿が ID の順で返ることを固定します
// （起動ログと画面の出し分けが、同じ順・同じ名前を見るため）。
func TestExtensionsSortedAndNamed(t *testing.T) {
	withExtension(t, "zzz", "最後")
	withExtension(t, "aaa", "最初")
	got := ExtensionIDs()
	if len(got) != 2 || got[0] != "aaa" || got[1] != "zzz" {
		t.Errorf("順が違います: %v", got)
	}
	if Extensions()[0].Name != "最初" {
		t.Errorf("名前が違います: %+v", Extensions()[0])
	}
}

// TestExtensionDuplicatePanics は、同じ ID の2度の登録を止めることを固定します。
func TestExtensionDuplicatePanics(t *testing.T) {
	withExtension(t, "dup", "一回目")
	defer func() {
		if recover() == nil {
			t.Error("同じ ID の2度目の登録が通りました")
		}
	}()
	RegisterExtension("dup", "二回目")
}

// TestTagSchemaListsExtensions は、`/api/tag-schema` が**載っている拡張を知らせる**ことを
// 固定します（2026-09-15・組み替え §4.1 案A）。
//
// ⚠ **拡張が1つも無くても `[]` を返すこと**（`null` ではなく）。画面は `null`／欠落を
// 「知らされていない」とみなして出し分けをしない（従来どおり全部出す）ので、
// `null` を返すと素の w-cms で壊れたボタンが残ります。
func TestTagSchemaListsExtensions(t *testing.T) {
	read := func() []string {
		rec := httptest.NewRecorder()
		TagSchemaAPIHandler(rec, httptest.NewRequest("GET", "/api/tag-schema", nil))
		var d struct {
			Extensions *[]string `json:"extensions"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatalf("応答が読めません: %v", err)
		}
		if d.Extensions == nil {
			t.Fatalf("extensions が null か欠落しています: %s", rec.Body.String())
		}
		return *d.Extensions
	}

	// コアの試験には拡張が載っていない——空の配列であること。
	if got := read(); len(got) != 0 {
		t.Fatalf("前提が崩れています（拡張が載っている）: %v", got)
	}
	withExtension(t, "subcon", "下請け業務")
	if got := read(); len(got) != 1 || got[0] != "subcon" {
		t.Errorf("載っている拡張が知らされていません: %v", got)
	}
}
