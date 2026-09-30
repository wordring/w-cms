package toho

import (
	"testing"

	"w-cms/internal/auth"
)

// TestIndexSourceMachines は、顧客の発注書（読んだまま）から装置名を引く見分け方を固定します（2026-09-30）。
//
//   - 「部品番号」の列に名前が書かれていれば、それが装置名（客先がそこに装置名を書いてくる）
//   - 記号の並ぶ「部品番号」は装置名ではない
//   - 「機種」の見出しがあればそれを使う
//   - 行は品番の記号か品名で当てる（記号の無い行は品名で）
func TestIndexSourceMachines(t *testing.T) {
	idx := &sourceMachineIndex{byCode: map[string]string{}, byName: map[string]string{}}
	indexSourceMachines(orderSourceTable{
		Headers: []string{"No.", "部品番号", "品名", "サイズ", "図面番号", "数量"},
		Rows: [][]string{
			{"1", "標準2輪", "本体ベース", "未塗装", "K120-01-03", "20"},
			{"2", "", "ベルトカバー", "", "K120-02-01", "20"},
			{"3", "ロングトス", "セット品", "", "", "5"},
		},
	}, idx)
	if idx.byCode["K120-01-03"] != "標準2輪" || idx.byCode["K120-02-01"] != "" || idx.byName["セット品"] != "ロングトス" {
		t.Errorf("読んだままの表から装置名を引けていません: %+v", idx)
	}

	coded := &sourceMachineIndex{byCode: map[string]string{}, byName: map[string]string{}}
	indexSourceMachines(orderSourceTable{
		Headers: []string{"部品番号", "品名"},
		Rows:    [][]string{{"A100-B01-02", "ブラケット"}},
	}, coded)
	if len(coded.byCode)+len(coded.byName) != 0 {
		t.Errorf("記号の部品番号を装置名にしています: %+v", coded)
	}

	named := &sourceMachineIndex{byCode: map[string]string{}, byName: map[string]string{}}
	indexSourceMachines(orderSourceTable{
		Headers: []string{"機種", "品名", "図番"},
		Rows:    [][]string{{"標準2輪", "ブラケット", "K120-3"}},
	}, named)
	if named.byCode["K120-3"] != "標準2輪" {
		t.Errorf("「機種」の列を使っていません: %+v", named)
	}
}

// TestMachineOfProductFolder は、弊社品番の加工製品ページから置き場の装置名を引くことを固定します。
func TestMachineOfProductFolder(t *testing.T) {
	const inbox = "000058"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}
	p := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(p, "取付ベース", "")})

	m := newMachineLookup()
	if got, fromProduct := m.machineOf(0, p, "", ""); got != "標準2輪" || !fromProduct {
		t.Errorf("置き場の装置名を引けていません: %q %v", got, fromProduct)
	}
	// 置き場の外（通信箱の下のまま）のページは装置名を持たない。
	loose := makeDrawingPage(t, inbox, "K120-2", "カバー", "標準2輪", "南北スポーツ")
	if got, _ := m.machineOf(0, loose, "", ""); got != "" {
		t.Errorf("置き場の外のページに装置名を付けています: %q", got)
	}
}
