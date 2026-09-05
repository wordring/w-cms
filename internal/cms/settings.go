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
// 「無ければ既定値」で動くように書きます。
type Settings struct {
	// TypeInference は見出し語→列型の推論辞書です（vocab.go の決定順序の3番目）。
	TypeInference map[string]ColumnType `json:"type_inference"`

	// MaxUploadMiB は添付1件あたりの上限（MiB）です。0（未指定）なら既定の32
	// （「サイズ上限32MiBは設定で変えられるように」——2026-08-31 ユーザー決定）。
	MaxUploadMiB int `json:"max_upload_mib,omitempty"`

	// AttachmentExtensions は汎用の添付として受ける拡張子です（ドットつき小文字）。
	// 未指定なら既定＝ワンノート実データの15種（【考察】ワンノート移行.md §3-4）。
	// 画像と .pdf は専用の口（中身検査つき）があるため、ここに書いても汎用の口は
	// 受けません。**.json は書けません**——添付の置き場は files/ に分離済みで
	// 構造上は無害だが、正本と同じ拡張子を添付に混ぜる運用そのものを断つ。
	AttachmentExtensions []string `json:"attachment_extensions,omitempty"`

	// MachineStages は装置名称の**上の段**の名前です（`取引先／社名／段／装置名称`）。
	// ユーザー:「装置名の上の段として、旧型、現行、試作などがあったほうが探しやすい」
	// （2026-09-05）。「など」と付いたので**運用中に増える前提**——語彙・推論辞書と
	// 同じくここに置きます。未指定なら既定＝現行・旧型・試作。
	//
	// **並び順に意味があります**——先頭が整理の画面の初期値（＝いちばん多い行き先）。
	MachineStages []string `json:"machine_stages,omitempty"`
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
			return fmt.Errorf("%s: type_inference の %q に未知の列型 %q があります（使えるのは text / code / number / date / enum / image）", path, word, typ)
		}
	}
	if s.MaxUploadMiB < 0 {
		return fmt.Errorf("%s: max_upload_mib が負です", path)
	}
	seenStage := map[string]bool{}
	for _, st := range s.MachineStages {
		v := strings.TrimSpace(st)
		if v == "" {
			return fmt.Errorf("%s: machine_stages に空の段があります", path)
		}
		// **段はページの題になります。** 題に使えない文字が混じると、整理の実行が
		// 全件そこで止まります——書いた時点で気づけるよう、ここで断ります。
		if strings.ContainsAny(v, "/\\") {
			return fmt.Errorf("%s: machine_stages の %q に区切り文字は使えません（ページの題になります）", path, st)
		}
		if seenStage[v] {
			return fmt.Errorf("%s: machine_stages に %q が2回あります", path, v)
		}
		seenStage[v] = true
	}
	for _, ext := range s.AttachmentExtensions {
		e := strings.ToLower(strings.TrimSpace(ext))
		if !strings.HasPrefix(e, ".") || len(e) < 2 {
			return fmt.Errorf("%s: attachment_extensions の %q はドットつき拡張子（例 .dxf）で書いてください", path, ext)
		}
		if e == ".json" {
			return fmt.Errorf("%s: attachment_extensions に .json は書けません（正本と同じ拡張子を添付に混ぜない）", path)
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
	return &Settings{
		TypeInference:        dict,
		MaxUploadMiB:         32,
		AttachmentExtensions: append([]string{}, defaultAttachmentExtensions...),
		MachineStages:        append([]string{}, defaultMachineStages...),
	}
}

// defaultMachineStages は装置の段の既定値です。**先頭が整理の初期値**なので、
// いちばん多い行き先である「現行」を先に置きます。
var defaultMachineStages = []string{"現行", "旧型", "試作"}

// MachineStages はいま効いている段の一覧を返します（並び順つき）。
// 返した配列は書き換えないこと（参照側が共有しています）。
func MachineStages() []string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings != nil && len(settings.MachineStages) > 0 {
		return settings.MachineStages
	}
	return defaultMachineStages
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

// defaultAttachmentExtensions は汎用添付の既定の拡張子です（ワンノート実データの15種
// ——.eml を含む——から、専用の口を持つ .pdf を除いた14種。「.eml の扱いは添付から
// 始めましょう」——2026-08-31 ユーザー決定）。
var defaultAttachmentExtensions = []string{
	".dxf", ".rpcd", ".xlsx", ".zip", ".docx", ".dwg", ".step", ".x_t",
	".slddrw", ".igs", ".eml", ".pub", ".lbx", ".mp4",
}

// MaxUploadBytes はいま効いている添付1件あたりの上限（バイト）を返します。
func MaxUploadBytes() int64 {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings != nil && settings.MaxUploadMiB > 0 {
		return int64(settings.MaxUploadMiB) << 20
	}
	return 32 << 20
}

// GenericAttachmentExts はいま効いている汎用添付の拡張子集合を返します。
func GenericAttachmentExts() map[string]bool {
	settingsMu.RLock()
	list := defaultAttachmentExtensions
	if settings != nil && len(settings.AttachmentExtensions) > 0 {
		list = settings.AttachmentExtensions
	}
	settingsMu.RUnlock()
	out := make(map[string]bool, len(list))
	for _, e := range list {
		out[strings.ToLower(strings.TrimSpace(e))] = true
	}
	return out
}
