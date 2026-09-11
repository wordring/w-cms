package sheetmetal

// 整理パネルの「装置名称の候補」（2026-09-11）。
//
// ユーザー:「装置名称の候補表示はあると良いと思います」——顧客名で解いたのと同じ問題が
// 一段下に残っていました。実データでは1通のメールの5枚が `φ410 2輪` / `2輪シュート改良` /
// `φ410-2輪` / `2軸シュート改良`（輪→軸の誤読）に割れ、そのまま流せば1台の装置が
// 4フォルダに散ります。

import (
	"testing"

	"w-cms/internal/auth"
)

// TestMachineNamesGroupsByCustomer は、装置名称の候補が**顧客ごと**に返ることを
// 固定します（2026-09-11 ユーザー:「装置名称の候補表示はあると良いと思います」）。
//
// 全部混ぜて返すと、他社の装置名が候補に出ます。段はまたいで集めます——人が知りたいのは
// 「この装置はもう在るか」で、どの段に在るかは suggestStage が別に答えるためです。
func TestMachineNamesGroupsByCustomer(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}

	for _, f := range []struct{ customer, stage, machine, drawing string }{
		{"トーアスポーツマシーン", "現行", "φ410 2輪", "シュート先本体"},
		{"トーアスポーツマシーン", "試作", "φ320 三輪共通", "補強ストッパー"},
		{"高瀬製作所", "現行", "スリッター", "受け板"},
	} {
		partID := makeDrawingPage(t, inbox, "Y050-"+f.drawing, f.drawing, f.machine, f.customer)
		results := postFiling(t, user, []filingRequest{{
			PageID: partID, Customer: f.customer, Stage: f.stage,
			MachineName: f.machine, DrawingName: f.drawing,
		}})
		if len(results) != 1 || results[0].Outcome != "moved" {
			t.Fatalf("下ごしらえが失敗しました: %+v", results)
		}
	}

	got := machineNames(user)
	if len(got["トーアスポーツマシーン"]) != 2 {
		t.Errorf("トーアスポーツマシーンの装置が2つ返りません: %v", got["トーアスポーツマシーン"])
	}
	// **段をまたいで集める**——試作の装置も候補に出る。
	if !contains(got["トーアスポーツマシーン"], "φ320 三輪共通") {
		t.Errorf("別の段の装置が落ちています: %v", got["トーアスポーツマシーン"])
	}
	// **他社の装置は混ざらない**。
	if contains(got["トーアスポーツマシーン"], "スリッター") {
		t.Errorf("他社の装置が混ざっています: %v", got["トーアスポーツマシーン"])
	}
	if !contains(got["高瀬製作所"], "スリッター") {
		t.Errorf("高瀬製作所の装置が返りません: %v", got["高瀬製作所"])
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
