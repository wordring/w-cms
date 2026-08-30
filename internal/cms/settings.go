package cms

// ─────────────────────────────────────────────────────────────────────────
// 設定ファイル（data/settings.json）
//
// **正本はファイル。DBには置きません。** `cms.db` は全再構築で全テーブルが消える
// 派生索引なので、設定をDBにだけ置くと設定も消えます（docs/アーキテクチャとDBスキーマ.md
// §8.2 と §9 の決定ログ D-5）。「文書（ファイル）が主」の原則の、設定への適用です。
//
// 最初の住人は**語→型の推論辞書**です。ユーザーの決定（2026-08-30）:
// 「**運用中に増やしてDB再構築します**」——Goコード内の map のままだと語を増やすたびに
// 再ビルドが要るため、ここへ移しました。増やしたら **DB再構築**（`POST /api/rebuild-db`）
// で読み直され、既存ページの索引にも反映されます（[RebuildDatabase] が先頭で読み直す）。
//
// 壊れたときの流儀は、正本まわりの既存の決定に揃えてあります:
//
//   - **ファイルが無ければ、コード内の既定値で作る**——失うものが無く、運用者が
//     中身を見て編集できるようになるため。
//   - **有るのに読めない・値が不正なら、起動を止める**——既定値で黙って上書きすると
//     運用者が足した語が消え、**集計の内容が黙って変わります**。壊れたときは壊れたままに
//     しておくほうが気づける（§8.4「派生から正本へ書き戻さない」と同じ流儀）。
//
// ファイルの中身は既定値を**置き換えます**（足すのではありません）。既定値を書き出した
// 状態から始まるので、要らない語は消せます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"w-cms/internal/cms/page"
)

// SettingsPath は設定ファイルの位置です（正本）。
const SettingsPath = "data/settings.json"

// Settings は data/settings.json の中身です。
//
// 項目を増やすときは、**既存のファイルを読めなくしないこと**——読み込みは
// 未知のキーを弾く（打ち間違いを黙って無視しないため）ので、新しいキーは
// 「無ければゼロ値」で動くように書きます。
type Settings struct {
	// TypeInference は見出し語→列型の推論辞書です（vocab.go の決定順序の3番目）。
	TypeInference map[string]ColumnType `json:"type_inference"`
}

// settings は読み込み済みの設定です。nil のあいだはコード内の既定値が使われます
// （テストや、LoadSettings を呼ばない経路のため）。
//
// 中身のマップは読み込み後**書き換えません**。差し替えは常にポインタごと行うので、
// 参照側はロックの外でマップを読んで構いません。
var (
	settingsMu sync.RWMutex
	settings   *Settings
)

// LoadSettings は設定ファイルを読み込み、以後の参照先にします。
// ファイルが無ければ既定値で作成します。
func LoadSettings() error {
	s, err := readOrCreateSettings(SettingsPath)
	if err != nil {
		return err
	}
	settingsMu.Lock()
	settings = s
	settingsMu.Unlock()
	return nil
}

// readOrCreateSettings は設定ファイルを読みます。無ければ既定値で作ります。
func readOrCreateSettings(path string) (*Settings, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s := defaultSettings()
		if err := writeSettings(path, s); err != nil {
			return nil, fmt.Errorf("設定ファイル %s を作成できません: %w", path, err)
		}
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("設定ファイル %s を読めません: %w", path, err)
	}

	var s Settings
	dec := json.NewDecoder(bytes.NewReader(raw))
	// 打ち間違えたキー（type_inferrence など）を黙って無視しない。
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("設定ファイル %s の書式が不正です（手で直してください）: %w", path, err)
	}
	if err := s.validate(path); err != nil {
		return nil, err
	}
	return &s, nil
}

// validate は設定の中身を検査します。**不正なら止めます**——読み飛ばすと、
// 書いたつもりの語が効かないまま集計だけが変わります。
func (s Settings) validate(path string) error {
	for word, typ := range s.TypeInference {
		if strings.TrimSpace(word) == "" {
			return fmt.Errorf("%s: type_inference に空の見出し語があります", path)
		}
		if !validColumnTypes[typ] {
			return fmt.Errorf("%s: type_inference の %q に未知の列型 %q があります（使えるのは text / number / date / enum / image）", path, word, typ)
		}
	}
	return nil
}

// writeSettings は設定ファイルを書きます。原子的書き込み（page.WriteFileAtomic）で、
// 書きかけの切り詰めが正本に残らないようにします。
func writeSettings(path string, s *Settings) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return page.WriteFileAtomic(path, append(body, "\n"...), 0o644)
}

// defaultSettings はコード内の既定値です（ファイルが無いときの初期内容）。
func defaultSettings() *Settings {
	dict := make(map[string]ColumnType, len(defaultTypeInference))
	for k, v := range defaultTypeInference {
		dict[k] = v
	}
	return &Settings{TypeInference: dict}
}

// activeTypeInference はいま効いている推論辞書を返します。
// 返したマップは書き換えないこと（参照側が共有しています）。
func activeTypeInference() map[string]ColumnType {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings != nil && settings.TypeInference != nil {
		return settings.TypeInference
	}
	return defaultTypeInference
}
