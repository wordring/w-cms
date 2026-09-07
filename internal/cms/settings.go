package cms

// ─────────────────────────────────────────────────────────────────────────
// 設定ファイル（config/settings.json）
//
// **正本はファイル。DBには置きません。** `cms.db` は全再構築で全テーブルが消える
// 派生索引なので、設定をDBにだけ置くと設定も消えます（docs/アーキテクチャとDBスキーマ.md
// §8.2 と §9 の決定ログ D-5）。「文書（ファイル）が主」の原則の、設定への適用です。
//
// 住人は**語→型の推論辞書**・添付の上限と拡張子・装置の段・文字の置き換え表。
// ユーザーの決定（2026-08-30）:「**運用中に増やしてDB再構築します**」——語を増やす
// たびに再ビルドが要らないよう、ファイルに置きます。増やしたら **DB再構築**
// （`POST /api/rebuild-db`）で読み直され、既存ページの索引にも反映されます。
//
// ── 2026-09-07 の変更: 既定値をコードから無くし、ファイルを必須にしました ──
//
// ユーザー:「コードに埋め込まれる規定を無くし、Settings.jsonを必須にし、Githubに
// 入れてはどうでしょう？」。それまでは Goコードの map が既定値を持ち、ファイルが
// 無ければそれを書き出す作りでした。やめた理由は2つです。
//
//  1. **同じことを2か所が言っていた。** コードに語を足しても、**既にファイルのある
//     環境には届きません**（ファイルの中身が既定を置き換えるため）。2026-09-06 に
//     `ref`・`datetime` を足したとき、実際にこれで参照リンクが消えかけました。
//  2. **不揃いだった。** `type_inference` だけが「置き換え」、他の3つは「未指定なら
//     既定」——どちらか分からない設定が4つ並んでいました。
//
// いまは**ファイルが唯一の正本**です。置き場所も `data/`（環境ごとの状態）から
// `config/`（製品の構成）へ移しました——ページ・DB・トークンとは種類が違うものです。
// **Git管理**なので、語を足せば `git pull` で全環境へ届きます。
//
// **秘密は入れません。** 鍵・トークンの類は環境変数（`~/OneDrive/デスクトップ/w-cms.env`）
// が持ちます。このファイルは公開リポジトリに入るので、**秘密を書けば公開されます**。
//
// 壊れたとき・無いときは**起動を止めます**。既定値で黙って埋めると、運用者が足した語が
// 消えて**集計の内容が黙って変わります**（§8.4「派生から正本へ書き戻さない」と同じ流儀）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
)

// SettingsPath は設定ファイルの位置です（正本）。
const SettingsPath = "config/settings.json"

// Settings は config/settings.json の中身です。
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

	// CharFolding は**比較の前に置き換える文字**です（`Φ`→`φ` など）。
	//
	// ユーザー:「材料や図面にΦという記号が多く出てきます。大文字小文字などの揺れを
	// 正規化しましょう。こういった文字は、設定できた方が良いかも」（2026-09-06）。
	//
	// **NFKC では畳めません**——`Φ`（U+03A6）と `φ`（U+03C6）は大小の違いで、
	// Unicode の正規化は大小を変換しないためです。直径記号にいたっては
	// `⌀`・`Ø`・`φ` と**別系統の文字**が同じ意味で使われます。どれを同じとみなすかは
	// **業種の知識**なので、コードではなく設定に置きます。
	//
	// 未指定なら既定（`defaultCharFolding`）。**足したらDB再構築で効きます**
	// ——語→型の推論辞書と同じ流儀。
	CharFolding map[string]string `json:"char_folding,omitempty"`

	// WebDAVHidden は WebDAV に見せないページの題です（トップ直下でなくても効きます）。
	//
	// ユーザー:「設定で見せないとするもの以外は見せて良いのでは？」（2026-09-07）。
	// **コアは業務の言葉を知りません**——`通信箱` を名前で特別扱いするコードを書くと、
	// 仕組みの側に語彙が漏れます。未指定なら**全部見せます**。
	WebDAVHidden []string `json:"webdav_hidden,omitempty"`
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
// **起動時に無ければ止めます**（既定値をコードに持たないため。2026-09-07）。
func LoadSettings() error {
	s, err := readSettings(SettingsPath)
	if err != nil {
		// **起動時は止めます**（settings がまだ nil）。
		//
		// **読み直しのときは、いま持っているものを守ります。** DB再構築は運用中に
		// 走るので、そこでファイルが見当たらないからと語彙を空にすると、**索引が
		// 静かに痩せます**——設定を消したことより、集計が黙って変わるほうが害が大きい。
		settingsMu.RLock()
		loaded := settings != nil
		settingsMu.RUnlock()
		if loaded && errors.Is(err, os.ErrNotExist) {
			log.Printf("設定ファイル %s が見当たりません。読み込み済みの設定のまま続けます", SettingsPath)
			return nil
		}
		return err
	}
	settingsMu.Lock()
	settings = s
	charFolder = newCharFolder(s.CharFolding)
	settingsMu.Unlock()
	return nil
}

// LoadSettingsFrom は指定した場所から設定を読み込みます。
//
// **作業ディレクトリに依らずに読む**ための口です。`LoadSettings` は
// `config/settings.json` を相対で読むので、一時ディレクトリへ移る経路（テスト）や、
// 別の場所に設定を置く運用では、こちらを使います。
func LoadSettingsFrom(path string) error {
	s, err := readSettings(path)
	if err != nil {
		return err
	}
	settingsMu.Lock()
	settings = s
	charFolder = newCharFolder(s.CharFolding)
	settingsMu.Unlock()
	return nil
}

// readSettings は設定ファイルを読みます。**無ければ止めます**（2026-09-07）。
//
// 既定値で作り直す作りをやめたのは、コードとファイルが同じことを2か所で言うのを
// 断つためです。ファイルはGit管理なので、クローンすれば必ず在ります——**無いのは
// 異常**（消した・別の場所から起動した）で、黙って埋めるより止めるほうが親切です。
func readSettings(path string) (*Settings, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// **`%w` で包みます**——呼ぶ側（LoadSettings）が「無い」と「壊れている」を
		// `errors.Is` で見分けます。包み忘れると読み直しの守りが効きません。
		return nil, fmt.Errorf("設定ファイル %s がありません（Git管理の必須ファイルです。"+
			"リポジトリの根から起動しているか確かめ、消したなら git checkout で戻してください）: %w",
			path, err)
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
	for from, to := range s.CharFolding {
		if strings.TrimSpace(from) == "" {
			return fmt.Errorf("%s: char_folding に空の置き換え元があります", path)
		}
		if from == to {
			return fmt.Errorf("%s: char_folding の %q が自分自身への置き換えになっています", path, from)
		}
		// **置き換え先が別の置き換え元だと、順序で結果が変わります**（`Φ`→`φ`→`f` の類）。
		// 書いた時点で断るほうが、あとで「なぜか畳まれない」を追うより安いです。
		if _, chained := s.CharFolding[to]; chained {
			return fmt.Errorf("%s: char_folding の %q → %q は、さらに置き換えられる文字を指しています", path, from, to)
		}
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

// activeTypeInference はいま効いている推論辞書を返します。
// 返したマップは書き換えないこと（参照側が共有しています）。
func activeTypeInference() map[string]ColumnType {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings == nil {
		return nil
	}
	return settings.TypeInference
}

// MaxUploadBytes は設定の添付1件あたりの上限（バイト）を返します。
//
// **ここだけコードに数を持ちます。** 設定を読む前でも上限ゼロで受けてしまわないため
// ——上限は語彙ではなく安全の柵で、「無ければ無制限」が最悪の既定になります。
func MaxUploadBytes() int64 {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings != nil && settings.MaxUploadMiB > 0 {
		return int64(settings.MaxUploadMiB) << 20
	}
	return 32 << 20
}

// GenericAttachmentExts は設定の汎用添付の拡張子集合を返します。
func GenericAttachmentExts() map[string]bool {
	settingsMu.RLock()
	var list []string
	if settings != nil {
		list = settings.AttachmentExtensions
	}
	settingsMu.RUnlock()
	out := make(map[string]bool, len(list))
	for _, e := range list {
		out[strings.ToLower(strings.TrimSpace(e))] = true
	}
	return out
}

// charFolder はいま効いている置き換え表の Replacer です。
//
// **設定が差し替わったときだけ作り直します**（Replacer の生成は安くないので、
// 正規化のたびに作るのは無駄）。settings と同じロックで守ります。
var charFolder *strings.Replacer

// activeCharFolder は置き換え用の Replacer を返します（設定が無ければ nil＝畳まない）。
func activeCharFolder() *strings.Replacer {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return charFolder
}

// newCharFolder は表から Replacer を作ります（空なら nil）。
func newCharFolder(m map[string]string) *strings.Replacer {
	if len(m) == 0 {
		return nil
	}
	// **並びを固定します**——map の走査順は毎回変わるので、そのまま渡すと
	// 同じ設定でも実行ごとに置き換えの優先順位が変わりえます。
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(m)*2)
	for _, k := range keys {
		pairs = append(pairs, k, m[k])
	}
	return strings.NewReplacer(pairs...)
}

// MachineStages は設定の段の一覧を返します（並び順つき。**先頭が整理の初期値**）。
// 返した配列は書き換えないこと（参照側が共有しています）。
func MachineStages() []string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings == nil {
		return nil
	}
	return settings.MachineStages
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

// WebDAVHidden は WebDAV に見せないページの題です（設定 `webdav_hidden`）。
//
// ユーザー:「通信箱も取引先もプラグインの領域ですが、クリックして開くは w-cms 本体の
// 機能なので、**設定で見せないとするもの以外は見せて良いのでは？**」（2026-09-07）。
//
// **コアは業務の言葉を知りません。** `通信箱` を特別扱いするコードを書くと、
// 仕組みの側に語彙が漏れます——見せる／見せないは運用者が名前で指定します。
// 既定は**空**（全部見せる）。
func WebDAVHidden() []string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings == nil {
		return nil
	}
	return settings.WebDAVHidden
}

// hiddenWebDAVTitles は引きやすい形（集合）で返します。
func hiddenWebDAVTitles() map[string]bool {
	list := WebDAVHidden()
	out := make(map[string]bool, len(list))
	for _, t := range list {
		if v := strings.TrimSpace(t); v != "" {
			out[v] = true
		}
	}
	return out
}
