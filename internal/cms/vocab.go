package cms

// ─────────────────────────────────────────────────────────────────────────
// ① 語彙レジストリ（3層モデルの①。docs/【考察】語彙モデル.md §4）
//
// 「マーカー付き標準HTML」——繰り返し明細＝ <table data-type="…">、名前:値＝
// <dl data-type="…"> ——の形式定義を宣言データとして持ちます。語彙の追加は
// このテーブルへの宣言1件で済み、Goコード（計算プラグイン＝③）を要しません。
//
// v1 は決定ログ（同書 §9）どおり **Goコード内の宣言テーブル** から始めます
// （定義スキーマが固まる前にDB化すると移行が二度手間になるため。将来は
// (b) 管理UIのDB → (c) 語彙定義ページへ段階的に育てる）。
//
// **レジストリは安全性の門ではありません。** サニタイズの許可リストは htmldoc が
// 属性名だけを要素限定で持ち、data-type の値は検査しません。未知の data-type も
// 保存を通し（＋保存時に告知）、②汎用索引（vocab_index.go）にも載ります。
// レジストリが担うのは編集支援（スラッシュメニュー・骨格生成・入力補助）と
// 型推論・正規化の語彙だけです。
//
// 定義スキーマはサンプル実装先行（決定ログ）: まず「検査記録」で end-to-end に
// 動かし、過不足を知ってから実運用スキーマを確定します。
// ─────────────────────────────────────────────────────────────────────────

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// ColumnType は列の型です。列型の集合は固定で、型を増やしても汎用表エディタの
// 実装は変わりません（要件定義書 §2.5 の「型数に不変」）。
// 通貨は独立の型とせず number に含めます（正規化が ¥・桁区切りを吸収する）。
type ColumnType string

const (
	ColText   ColumnType = "text"
	ColNumber ColumnType = "number"
	ColDate   ColumnType = "date"
	ColEnum   ColumnType = "enum"
	ColImage  ColumnType = "image"
)

// validColumnTypes は th の data-type 属性（列型の明示）として受け付ける値です。
var validColumnTypes = map[ColumnType]bool{
	ColText: true, ColNumber: true, ColDate: true, ColEnum: true, ColImage: true,
}

// VocabColumn は形式の1列（dl では1項目）の定義です。
type VocabColumn struct {
	// Field は③計算プラグインが使う機械キーです。**本文には現れません**——
	// 本文の鍵は常に見出しの表示文字（th / dt）で、Label を通じてこの Field へ解決されます
	// （「見える文字がすべて」——語彙モデル §5.1。かつては dd/th の data-field として
	// 本文へ書き出していましたが 2026-08-20 に撤去しました）。
	// 空なら機械キーを持たない列＝表示文字がそのまま鍵になります。
	Field string     `json:"field,omitempty"`
	Label string     `json:"label"`          // 見出しの表示文字（dt の文字）
	Type  ColumnType `json:"type"`           // 列型
	Enum  []string   `json:"enum,omitempty"` // Type==ColEnum のときの選択肢
}

// VocabDef は1つの形式（data-type）の定義です。
type VocabDef struct {
	Type        string        `json:"type"`         // data-type の値（kebab-case・レジストリ全体で一意）
	DisplayName string        `json:"display_name"` // スラッシュメニュー等の表示名
	Category    string        `json:"category"`     // 分類（メニューのグルーピング用）
	Icon        string        `json:"icon"`         // メニューのアイコン（絵文字）
	Element     string        `json:"element"`      // "table"（繰り返し明細）| "dl"（名前:値）| "section"（業務文書ブロック・論点A案1）
	Columns     []VocabColumn `json:"columns"`      // 列（dl では項目、section ではヘッダ dl の項目）の並び

	// 以下は業務文書ブロック（Element=="section"・語彙モデル §8.2 論点A）用。
	Items  string `json:"items,omitempty"`  // 明細表の形式名（素の表をこの形式として読む。D-2）
	Hidden bool   `json:"hidden,omitempty"` // スラッシュメニューに出さない（明細表など、単独で挿入しない形式）

	// File は「この形式はPDF原本を伴う」宣言です。エディタがドロップゾーン・
	// プレビュー・明細AI解析をこのセクション自身へ配線します（PDFの所在は
	// 本文の可視のファイル名リンクが運ぶ——見える文字がデータの手掛かり）。
	// かつては専用の file 容器（section[data-type="file" data-src]）で受発注を
	// 包んでいたが、D-1 で容器を読むGoコードが消え、取り付け台としての仕事しか
	// 残らなかったため 2026-08-31 に廃止した（「data-typeがfileのsectionは
	// 必要ないのでは？」——ユーザー指摘）。既存本文の file 容器は互換のため
	// 引き続き動く（レジストリの file 宣言と data-src の許可は残る）。
	File bool `json:"file,omitempty"`

	// View は計算ビュー（表示専用）。本文には空のマーカー <section data-type> だけを
	// 保存し、中身はサーバーがページ合成時に埋める（view_render.go。ユーザー決定
	// 2026-08-19: サーバー事前描画）。エディタは骨格＝空 section を挿し、
	// 中身（.vocab-chrome）は保存しない。
	View bool `json:"view,omitempty"`

	// RequiresTag は「この形式が③計算で使う、ページ横断メタ（<dl data-type="tags"）の
	// 見出し」です。部材定義が部品番号を鍵に受注明細と突き合わせるように、形式の外に
	// 鍵がある場合に宣言します。
	//
	// これが無かったころ、鍵は plugin_materials.go に直書きされた日本語の魔法文字列
	// でした。見出しを改名すると集計が丸ごと空になるのに告知はゼロ——「Field を持たない
	// 列は改名しても壊れない」という UnresolvedVocabFields の前提が、ここだけ破れていた。
	// 宣言にすることで、読み取りと告知が同じ1箇所を見る。
	RequiresTag string `json:"requires_tag,omitempty"`
}

// vocabRegistry が宣言テーブルの本体です。語彙を増やすときはここへ1件足します。
//
// サンプル語彙「検査記録」は縦切り第1段（語彙モデル §8.4）の実証用で、
// 移行前から独立した形式です（記録だけの形式なので Field は持たない）。
var vocabRegistry = []VocabDef{
	// 移行第2段（語彙モデル §8.4-2）の移行先形式。
	// "tags" はページ横断メタ——名前は自由語で、dt の表示文字が
	// そのまま鍵になる（§5.3）。骨格は1項目だけ生成し、項目操作UIで増やす。
	{
		Type:        "tags",
		DisplayName: "可変タグ",
		Category:    "メタ",
		Icon:        "🏷️",
		Element:     "dl",
		Columns: []VocabColumn{
			{Label: "新規タグ", Type: ColText},
		},
	},
	// ── 移行第3段（受発注4種＋容器。語彙モデル §8.1・§8.2 論点A=案1） ──
	// 業務文書ブロックは <section data-type> がヘッダ <dl>（data-type 無し・鍵は
	// dt の表示文字）と明細 <table data-type> を包む。PDF容器は
	// <section data-type="file" data-src>（配線＝属性）＋可視のファイル名リンク（中身）。
	{
		Type:        "file",
		DisplayName: "ファイル（PDF）",
		Category:    "業務",
		Icon:        "📎",
		Element:     "section",
	},
	{
		Type:        "inspection-record",
		DisplayName: "検査記録",
		Category:    "記録",
		Icon:        "📋",
		Element:     "table",
		Columns: []VocabColumn{
			{Label: "品番", Type: ColText},
			{Label: "判定", Type: ColEnum, Enum: []string{"合格", "不合格"}},
			{Label: "検査写真", Type: ColImage},
			{Label: "検査日", Type: ColDate},
		},
	},

	// ── 移行第4段（語彙モデル §8.4-4）: 表示専用の計算ビュー ──
	// 列を持たず、中身はサーバー事前描画（RenderComputedViews）。
	{
		Type:        "child-list",
		DisplayName: "子ページ一覧",
		Category:    "ビュー",
		Icon:        "📂",
		Element:     "section",
		View:        true,
	},
	{
		// 未処理の受信——まだ手を付けていない通信記録（view_unhandled.go）。
		// ユーザー:「未処理のメールやFAXを一覧できる方法が必要かも」（2026-09-03）。
		// 判定に新しい入力を求めず、**子ページが在ること自体を「手を付けた」の印**
		// として使います。
		Type:        "unhandled-intake",
		DisplayName: "未処理の受信",
		Category:    "ビュー",
		Icon:        "📥",
		Element:     "section",
		View:        true,
	},
}

// RegisterVocab は形式の宣言を①語彙レジストリへ足します。**拡張の `init()` から
// 呼びます**（`Register`／`RegisterIntake` と同じ流儀）。
//
// コアが持つのは器と汎用の語彙（`tags`・`file`・計算ビュー・サンプルの検査記録）だけで、
// **業務の語彙は拡張が持ち込みます**——「語彙とプラグインは運用者のもの」
// （[要件定義書.md](../../docs/要件定義書.md) §1.1・§4.5）を、置き場でも成立させる形です。
// 板金部の受発注・部材・見積は `ext/sheetmetal` が登録します。
//
// 同じ `Type` の二重登録は**その場で落とします**。黙って後勝ちにすると、どちらの
// 宣言が効いているのか画面からは分からず、列の型だけが静かに変わります。
func RegisterVocab(defs ...VocabDef) {
	for _, d := range defs {
		for _, existing := range vocabRegistry {
			if existing.Type == d.Type {
				panic("語彙の形式名が重複しています: " + d.Type)
			}
		}
		vocabRegistry = append(vocabRegistry, d)
	}
}

// VocabDefs は登録済みの全形式定義を Type 順で返します（/api/tag-schema の応答が
// 呼び出しごとに変わらないようにするため）。
func VocabDefs() []VocabDef {
	out := make([]VocabDef, len(vocabRegistry))
	copy(out, vocabRegistry)
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// VocabDefByType は data-type の値で形式定義を探します。
func VocabDefByType(t string) (VocabDef, bool) {
	for _, d := range vocabRegistry {
		if d.Type == t {
			return d, true
		}
	}
	return VocabDef{}, false
}

// VocabDefByHeading は**機能見出しの言葉**で形式定義を探します（D-2・2026-08-30 決定）。
// 言葉の正は**レジストリの表示名**です（語彙モデル §11.5-6。th の鍵が Label で引けるのと
// 同じ規律——見える文字とメニューに出る文字が同じ1つの宣言を指す）。
// 一致しない言葉は「形式なし」＝ただのセクションで、告知はしません——見出しは
// data-type と違って**全セクションが普通に持つもの**なので、未登録語への告知は
// 「作業メモ」のような普通の見出しへの誤検知の洪水になります（エージェント判断）。
func VocabDefByHeading(word string) (VocabDef, bool) {
	if word == "" {
		return VocabDef{}, false
	}
	for _, d := range vocabRegistry {
		if d.DisplayName == word {
			return d, true
		}
	}
	return VocabDef{}, false
}

// columnFor は形式定義から鍵（見出しの表示文字＝Label。機械キー Field でも引ける）に
// 対応する列を探します。
func (d VocabDef) columnFor(key string) (VocabColumn, bool) {
	for _, c := range d.Columns {
		if (c.Field != "" && c.Field == key) || c.Label == key {
			return c, true
		}
	}
	return VocabColumn{}, false
}

// kebabCaseRe は識別子の文字種規約（決定ログ: kebab-case）です。
// レジストリの検証（テスト）で使います。値の検査には使いません（値は不活性）。
var kebabCaseRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ─────────────────────────────────────────────────────────────────────────
// 語→型の推論辞書と正規化（レジストリの一部。語彙モデル §5.1）
//
// 型の決定順序: th の data-type 明示 > レジストリ宣言 > この辞書 > text。
// 推論・正規化は**通知と併記のため**であり、入力の拒否には使いません
// （自由な記述をシステムが認識する、というコア・コンセプトを壊さないため）。
// 辞書の初期内容とパース規則は未決事項（同書 §10）——現状はサンプルの種。
// ─────────────────────────────────────────────────────────────────────────

// defaultTypeInference は見出し語（trim後の完全一致）から列型を推論する辞書の**既定値**です。
//
// **実際に効くのは data/settings.json の `type_inference`**（settings.go）で、このマップは
// ファイルが無いときの初期内容として書き出されます。運用者が語を足すのはファイル側で、
// **DB再構築で読み直されます**（ユーザー決定 2026-08-30:「運用中に増やしてDB再構築します」）。
// ここに並ぶのは板金部の語彙——**同梱の既定セットであって、コアの語彙ではありません**
// （「語彙とプラグインは運用者のもの」——要件定義書 §4.5）。
var defaultTypeInference = map[string]ColumnType{
	"金額": ColNumber, "単価": ColNumber, "価格": ColNumber, "数量": ColNumber,
	"納期": ColDate, "日付": ColDate, "検査日": ColDate, "発注日": ColDate, "納品日": ColDate,
	"写真": ColImage, "画像": ColImage,
}

// InferColumnType は見出し語から列型を推論します。辞書に無ければ text です。
func InferColumnType(label string) ColumnType {
	if t, ok := activeTypeInference()[strings.TrimSpace(label)]; ok {
		return t
	}
	return ColText
}

// TypeInferenceDict は語→型の推論辞書の写しを返します。
// エディタは /api/tag-schema 経由でこれを受け取り、**同じ辞書**で入力を検証・通知します
// （形式知識の3原則の1: エディタに手書きの語彙を置かない——語彙モデル §7）。
func TypeInferenceDict() map[string]ColumnType {
	dict := activeTypeInference()
	out := make(map[string]ColumnType, len(dict))
	for k, v := range dict {
		out[k] = v
	}
	return out
}

// NormalizeValue は列型に応じて値の正規化値を返します。
// 解釈できないときは ok=false（正規化値は**併記**であり、生テキストが常に正本）。
func NormalizeValue(t ColumnType, raw string) (norm string, ok bool) {
	s := toHalfWidth(strings.TrimSpace(raw))
	switch t {
	case ColNumber:
		return normalizeNumber(s)
	case ColDate:
		return normalizeDate(s)
	default:
		return "", false // text / enum / image は正規化しない
	}
}

// toHalfWidth は全角の数字・記号を半角へ寄せます（正規化の前処理）。
func toHalfWidth(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '０' && r <= '９':
			return r - '０' + '0'
		case r == '．':
			return '.'
		case r == '／':
			return '/'
		case r == '－' || r == 'ー' || r == '−':
			return '-'
		}
		return r
	}, s)
}

// numberRe は正規化後に数値として受け付ける形です。
var numberRe = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// normalizeNumber は通貨記号・桁区切り・単位（円）を取り除いた数値文字列を返します。
func normalizeNumber(s string) (string, bool) {
	s = strings.NewReplacer("¥", "", "￥", "", "$", "", ",", "", "，", "", " ", "", "　", "").Replace(s)
	s = strings.TrimSuffix(s, "円")
	if s == "" || !numberRe.MatchString(s) {
		return "", false
	}
	return s, true
}

// dateRe は年月日の区切りを許容する形です（YYYY-M-D / YYYY/M/D / YYYY年M月D日）。
var dateRe = regexp.MustCompile(`^([0-9]{4})[-/年]([0-9]{1,2})[-/月]([0-9]{1,2})日?$`)

// normalizeDate は日付表記を YYYY-MM-DD へ揃えます（実在しない日付は ok=false）。
func normalizeDate(s string) (string, bool) {
	m := dateRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	norm := m[1] + "-" + pad2(m[2]) + "-" + pad2(m[3])
	if _, err := time.Parse("2006-01-02", norm); err != nil {
		return "", false
	}
	return norm, true
}

func pad2(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}
