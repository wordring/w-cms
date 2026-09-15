package subcon

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSettingsRepoFileHasStages は、**リポジトリの設定から段が読める**ことを固定します。
// 2026-09-15 に `machine_stages` をトップから `extensions.subcon` へ移しました——
// 移し忘れると整理が全件「段（）を選んでください」で止まります。
func TestSettingsRepoFileHasStages(t *testing.T) {
	// TestMain（settings_repo_test.go）がリポジトリの設定を読み込み済み。
	if len(MachineStages()) == 0 {
		t.Fatal("extensions.subcon.machine_stages が空です")
	}
	if !ValidMachineStage(MachineStages()[0]) {
		t.Errorf("先頭の段 %q が一覧に無いと判定されました", MachineStages()[0])
	}
}

// TestSettingsRejectsBrokenSection は、壊れた節で**検査が止める**ことを固定します
// （もとはコアの TestSettingsRejectsBrokenFile にあった「段の重複」を、節と一緒に移した）。
func TestSettingsRejectsBrokenSection(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{"段の重複", `{"machine_stages": ["現行", "現行"]}`, "2回"},
		{"空の段", `{"machine_stages": ["現行", "  "]}`, "空の段"},
		{"区切り文字", `{"machine_stages": ["現行/旧"]}`, "区切り文字"},
		{"打ち間違えたキー", `{"machine_stage": ["現行"]}`, "書式が不正"},
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

// TestSettingsAbsentSectionClearsStages は、**節が無ければ段が空に戻る**ことを固定します
// ——読み直しで節を消した運用者の意図どおり、古い段が残らないように。
func TestSettingsAbsentSectionClearsStages(t *testing.T) {
	saved := MachineStages()
	t.Cleanup(func() {
		stagesMu.Lock()
		machineStages = saved
		stagesMu.Unlock()
	})
	apply, err := parseSettings(nil)
	if err != nil {
		t.Fatalf("節が無いのに止まりました: %v", err)
	}
	apply()
	if len(MachineStages()) != 0 {
		t.Errorf("節が無いのに段が残っています: %v", MachineStages())
	}
}
