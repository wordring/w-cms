package subcon

// ─────────────────────────────────────────────────────────────────────────
// 下請け業務の設定の節（`config/settings.json` の `extensions.subcon`・2026-09-15）
//
// ユーザー決定（拡張の組み替え §4.2）:「拡張が自分の節を登録」。それまで段
// （`machine_stages`）は**使うのが下請けだけなのに、型も検査もコアの settings.go** に
// ありました。設定ファイルは1本のまま、読み方と検査をここへ移しています。
//
//	"extensions": {
//	  "subcon": { "machine_stages": ["現行", "旧型", "試作"] }
//	}
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"w-cms/internal/cms"
)

// settingsSection は `extensions.subcon` の中身です。
type settingsSection struct {
	// MachineStages は装置名称の**上の段**の名前です（`取引先／社名／段／装置名称`）。
	// ユーザー:「装置名の上の段として、旧型、現行、試作などがあったほうが探しやすい」
	// （2026-09-05）。「など」と付いたので**運用中に増える前提**。
	// **未指定なら段を1つも出しません**（既定の一覧はありません）。
	//
	// **並び順に意味があります**——先頭が整理の画面の初期値（＝いちばん多い行き先）。
	MachineStages []string `json:"machine_stages,omitempty"`
}

var (
	stagesMu      sync.RWMutex
	machineStages []string
)

func init() {
	cms.RegisterSettingsSection("subcon", parseSettings)
}

// parseSettings は節を読んで検査し、効かせる関数を返します（コアが全体の検査のあとに呼ぶ）。
func parseSettings(raw json.RawMessage) (func(), error) {
	var s settingsSection
	if len(raw) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		// 打ち間違えたキー（machine_stage など）を黙って無視しない——コアと同じ流儀。
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("書式が不正です（手で直してください）: %w", err)
		}
	}
	seen := map[string]bool{}
	for _, st := range s.MachineStages {
		v := strings.TrimSpace(st)
		if v == "" {
			return nil, fmt.Errorf("machine_stages に空の段があります")
		}
		// **段はページの題になります。** 題に使えない文字が混じると、整理の実行が
		// 全件そこで止まります——書いた時点で気づけるよう、ここで断ります。
		if strings.ContainsAny(v, "/\\") {
			return nil, fmt.Errorf("machine_stages の %q に区切り文字は使えません（ページの題になります）", st)
		}
		if seen[v] {
			return nil, fmt.Errorf("machine_stages に %q が2回あります", v)
		}
		seen[v] = true
	}
	stages := s.MachineStages
	return func() {
		stagesMu.Lock()
		machineStages = stages
		stagesMu.Unlock()
	}, nil
}

// MachineStages は設定の段の一覧を返します（並び順つき。**先頭が整理の初期値**）。
// 返した配列は書き換えないこと（参照側が共有しています）。
func MachineStages() []string {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	return machineStages
}

// ValidMachineStage は段が一覧にあるかを**表引きで**確かめます。
// 「現行」と「現行品」が混ざると、探すときに静かに取りこぼすためです。
func ValidMachineStage(v string) bool {
	for _, st := range MachineStages() {
		if st == v {
			return true
		}
	}
	return false
}
