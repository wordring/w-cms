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

	// ProductCodeTags は「**加工製品ページを言い当てる番号**」のタグ名です
	// （2026-09-21）。受注明細の `品番` からページを特定するときに、この名前の
	// タグを**順に**引きます。
	//
	// ⚠ **客先によって、番号がどのタグに入るかが違います**——みなと商店は図番で
	// 発注してくるのでページの `図面番号` に当たり、品番で発注してくる客先は
	// `品番` に当たります。図面の無い製品は `品番` しか持ちません。
	// **タグ名を問わず値で当てる**のはそのためです。
	//
	// ⚠ **`code` 型の語にすること**（設定の `vocabulary`）。`text` だと長音を
	// 畳まないので、`レーザー` と `レ-ザ-` のような揺れを越えられません。
	// **未指定なら結びません**（既定の一覧はありません）。
	ProductCodeTags []string `json:"product_code_tags,omitempty"`

	// PDFFont は**発注書のPDFに埋め込む日本語フォント**（`.ttf` のパス）です
	// （2026-09-21）。
	//
	// ⚠ **日本語のPDFはフォントを埋め込まないと読めません。** そして**同梱できるかは
	// ライセンス次第**なので、**パスを設定で指す形**にしました——w-cms は MIT で公開
	// されており、フォントを無条件に同梱はできません。
	//
	// ⚠ **`.ttc`（コレクション）も読めます**（2026-09-23）。gopdf は読めませんが、
	// こちらで**1書体を取り出して**渡します（[ttc.go](ttc.go)）——**Windows の太い
	// 和文フォントは全部 `.ttc`** なので、それまでは**いちばん読みやすいフォントを
	// 締め出して**いました。どの書体かは `pdf_font_face`。
	//
	// ⚠ **細いフォントを指さないこと。** `NotoSansJP-VF.ttf` は**可変フォント**で、
	// `wght` の既定値が **100（Thin）**です——gopdf は可変軸を扱わないので
	// **いちばん細い形が刷られ**、FAXでかすれます（2026-09-23 ユーザー報告）。
	//
	// **未指定ならPDFを作りません**（起動は止めません——Gemini キーと同じ扱いで、
	// 画面に「設定されていません」と出します）。
	PDFFont string `json:"pdf_font,omitempty"`

	// PDFFontFace は `.ttc` の中の何番目の書体かです（既定は0）。
	//
	// ⚠ **コレクションには似た書体が何本も入っています**——`BIZ-UDGothicB.ttc` なら
	// 0 が BIZ UDゴシック B、1 が BIZ UDPゴシック B（プロポーショナル）。
	// **どちらが好みかは人が決めます。**
	PDFFontFace int `json:"pdf_font_face,omitempty"`

	// Company は**発注書の差出人**です（2026-09-21）。実物の発注書に入っていた項目。
	//
	// ⚠ **設定に置きます。** 自社を表すページを作る案もありますが、`取引：自社` の
	// タグは 2026-09-18 に全廃しており（誰も読んでいなかった）、**同じものを2度作る**
	// ことになります。設定なら `git pull` で全環境へ届きます。
	// ⚠ **秘密ではありません**（相手に渡す紙に印刷する情報です）。
	Company companyInfo `json:"company,omitempty"`
}

// companyInfo は発注書に刷る差出人です（実物の見出しに合わせた項目）。
type companyInfo struct {
	Name    string `json:"name"`
	Zip     string `json:"zip"`
	Address string `json:"address"`
	Person  string `json:"person"`
	Tel     string `json:"tel"`
	Fax     string `json:"fax"`
}

var (
	stagesMu      sync.RWMutex
	machineStages []string
	// ⚠ **`stagesMu` を共有します。** 設定の反映は1回で両方を差し替えるので、
	// 別の錠にすると「段は新しいが番号のタグは古い」という中途半端な瞬間ができます。
	productCodeTags []string
	pdfFont         string
	pdfFontFace     int
	companyInf      companyInfo
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
	// ⚠ **重複と空だけ断ります。** 型（`code` であること）はここでは見ません——
	// この検査はコアが語彙を効かせる**前**に走ることがあり、見ると「まだ読み込まれて
	// いない語」を誤って断ります（2026-09-20 に `vocab_formats` で同じ形を踏みました）。
	seenTag := map[string]bool{}
	for _, t := range s.ProductCodeTags {
		v := strings.TrimSpace(t)
		if v == "" {
			return nil, fmt.Errorf("product_code_tags に空のタグ名があります")
		}
		if seenTag[v] {
			return nil, fmt.Errorf("product_code_tags に %q が2回あります", t)
		}
		seenTag[v] = true
	}
	// ⚠ **拡張子だけ見ます**（在るかどうかは見ません——設定を読むのはDB再構築でも
	// 走るので、そのときファイルが一時的に見えない環境で起動を止めたくありません）。
	// ⚠ **`.ttc` も通します**（2026-09-23）——1書体を取り出して渡すので読めます。
	if f := strings.TrimSpace(s.PDFFont); f != "" {
		low := strings.ToLower(f)
		if !strings.HasSuffix(low, ".ttf") && !strings.HasSuffix(low, ".ttc") {
			return nil, fmt.Errorf("pdf_font は .ttf か .ttc を指してください（%q）", s.PDFFont)
		}
	}
	if s.PDFFontFace < 0 {
		return nil, fmt.Errorf("pdf_font_face は0以上です（%d）", s.PDFFontFace)
	}
	stages := s.MachineStages
	codeTags := s.ProductCodeTags
	font := strings.TrimSpace(s.PDFFont)
	face := s.PDFFontFace
	company := s.Company
	return func() {
		stagesMu.Lock()
		machineStages = stages
		productCodeTags = codeTags
		pdfFont = font
		pdfFontFace = face
		companyInf = company
		stagesMu.Unlock()
	}, nil
}

// PDFFont は発注書のPDFへ埋め込むフォントのパスを返します（未設定なら空）。
func PDFFont() string {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	return pdfFont
}

// Company は発注書の差出人を返します。
func Company() companyInfo {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	return companyInf
}

// ProductCodeTags は「加工製品ページを言い当てる番号」のタグ名を返します（並び順つき）。
// 返した配列は書き換えないこと（参照側が共有しています）。
func ProductCodeTags() []string {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	return productCodeTags
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

// PDFFontFace は `.ttc` の中の何番目の書体を使うかを返します（既定は0）。
func PDFFontFace() int {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	return pdfFontFace
}
