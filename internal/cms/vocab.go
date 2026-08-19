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

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
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
	// Field は data-field（機械キー）。**③計算形式だけ**が持ち、骨格生成時に
	// エディタが自動付与します。空なら見出しテキストがそのまま鍵になります
	// （「見える文字が既定・機械属性は例外」——語彙モデル §5.1）。
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
	Items     string `json:"items,omitempty"`     // 明細表の形式名（section 直下の table[data-type]）
	Container string `json:"container,omitempty"` // 挿入骨格を包む容器の形式名（例: "file"）
	Hidden    bool   `json:"hidden,omitempty"`    // スラッシュメニューに出さない（明細表など、単独で挿入しない形式）

	// View は計算ビュー（表示専用）。本文には空のマーカー <section data-type> だけを
	// 保存し、中身はサーバーがページ合成時に埋める（view_render.go。ユーザー決定
	// 2026-08-19: サーバー事前描画）。エディタは骨格＝空 section を挿し、
	// 中身（.vocab-chrome）は保存しない。
	View bool `json:"view,omitempty"`
}

// vocabRegistry が宣言テーブルの本体です。語彙を増やすときはここへ1件足します。
//
// サンプル語彙「検査記録」は縦切り第1段（語彙モデル §8.4）の実証用で、
// 既存のカスタム要素9種とは独立しています（記録だけの形式なので Field は持たない）。
var vocabRegistry = []VocabDef{
	// 移行第2段（語彙モデル §8.4-2）の移行先形式。
	// "tags" は <m-tag>（ページ横断メタ）の後継——名前は自由語で、dt の表示文字が
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
	// "part-materials" は <m-material>（部品の構成部材）の後継。部材手配計算
	// （plugin_materials.go の Sync / /api/required-materials）が読む③計算形式なので、
	// 列は data-field（機械キー）を持ち、骨格生成時にエディタが自動付与する。
	// ③の語彙宣言をプラグイン側へ移す（①と③の合成）のは将来課題で、v1 は
	// コード内の単一テーブルに同居させる。
	{
		Type:        "part-materials",
		DisplayName: "部材定義",
		Category:    "業務",
		Icon:        "🔩",
		Element:     "table",
		Columns: []VocabColumn{
			{Field: "item-name", Label: "部材名", Type: ColText},
			{Field: "cost", Label: "単価", Type: ColNumber},
			{Field: "supplier-name", Label: "仕入先", Type: ColText},
			{Field: "quantity", Label: "数量", Type: ColNumber},
		},
	},
	// ── 移行第3段（受発注4種＋容器。語彙モデル §8.1・§8.2 論点A=案1） ──
	// 業務文書ブロックは <section data-type> がヘッダ <dl>（data-type 無し・dd に
	// data-field 自動付与）と明細 <table data-type> を包む。PDF容器は
	// <section data-type="file" data-src>（配線＝属性）＋可視のファイル名リンク（中身）。
	{
		Type:        "file",
		DisplayName: "ファイル（PDF）",
		Category:    "業務",
		Icon:        "📎",
		Element:     "section",
	},
	{
		Type:        "client-order",
		DisplayName: "顧客の発注書",
		Category:    "業務",
		Icon:        "📩",
		Element:     "section",
		Items:       "client-order-items",
		Container:   "file",
		Columns: []VocabColumn{
			{Field: "order-no", Label: "発注書番号", Type: ColText},
			{Field: "client-name", Label: "発注元", Type: ColText},
			{Field: "ordered-at", Label: "発注日", Type: ColDate},
		},
	},
	{
		Type:    "client-order-items",
		DisplayName: "受注明細",
		Category:    "業務",
		Icon:        "📩",
		Element: "table",
		Hidden:  true,
		Columns: []VocabColumn{
			{Field: "item-id", Label: "品番", Type: ColText},
			{Field: "item-name", Label: "品名", Type: ColText},
			{Field: "price", Label: "単価", Type: ColNumber},
			{Field: "quantity", Label: "数量", Type: ColNumber},
			{Field: "status", Label: "状態", Type: ColEnum, Enum: []string{"未着手", "加工中", "検査中", "納品済"}},
		},
	},
	{
		Type:        "our-order",
		DisplayName: "自社の発注書",
		Category:    "業務",
		Icon:        "📤",
		Element:     "section",
		Items:       "our-order-items",
		Container:   "file",
		Columns: []VocabColumn{
			{Field: "order-no", Label: "発注書番号", Type: ColText},
			{Field: "supplier-name", Label: "発注先", Type: ColText},
			{Field: "ordered-at", Label: "発注日", Type: ColDate},
		},
	},
	{
		Type:    "our-order-items",
		DisplayName: "発注明細",
		Category:    "業務",
		Icon:        "📤",
		Element: "table",
		Hidden:  true,
		Columns: []VocabColumn{
			{Field: "item-name", Label: "品名", Type: ColText},
			{Field: "cost", Label: "単価", Type: ColNumber},
			{Field: "quantity", Label: "数量", Type: ColNumber},
			{Field: "status", Label: "状態", Type: ColEnum, Enum: []string{"未納品", "納品済"}},
		},
	},
	{
		Type:        "our-estimate",
		DisplayName: "弊社の見積もり",
		Category:    "業務",
		Icon:        "💴",
		Element:     "dl",
		Columns: []VocabColumn{
			{Field: "item-id", Label: "品番", Type: ColText},
			{Field: "client-name", Label: "顧客", Type: ColText},
			{Field: "price", Label: "見積金額", Type: ColNumber},
			{Field: "estimated-at", Label: "見積日", Type: ColDate},
		},
	},
	{
		Type:        "supplier-estimate",
		DisplayName: "材料屋の見積もり",
		Category:    "業務",
		Icon:        "🏭",
		Element:     "dl",
		Columns: []VocabColumn{
			{Field: "item-name", Label: "部材名", Type: ColText},
			{Field: "supplier-name", Label: "仕入先", Type: ColText},
			{Field: "cost", Label: "見積金額", Type: ColNumber},
			{Field: "estimated-at", Label: "見積日", Type: ColDate},
		},
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
	// <m-child-list>・<m-required-materials> の後継。列を持たず、
	// 中身はサーバー事前描画（RenderComputedViews）。
	{
		Type:        "child-list",
		DisplayName: "子ページ一覧",
		Category:    "ビュー",
		Icon:        "📂",
		Element:     "section",
		View:        true,
	},
	{
		Type:        "required-materials",
		DisplayName: "手配状況リスト",
		Category:    "ビュー",
		Icon:        "📊",
		Element:     "section",
		View:        true,
	},
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

// columnFor は形式定義から鍵（data-field の値または見出しテキスト）に対応する列を探します。
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

// typeInference は見出し語（trim後の完全一致）から列型を推論する辞書です。
var typeInference = map[string]ColumnType{
	"金額": ColNumber, "単価": ColNumber, "価格": ColNumber, "数量": ColNumber,
	"納期": ColDate, "日付": ColDate, "検査日": ColDate, "発注日": ColDate, "納品日": ColDate,
	"写真": ColImage, "画像": ColImage,
}

// InferColumnType は見出し語から列型を推論します。辞書に無ければ text です。
func InferColumnType(label string) ColumnType {
	if t, ok := typeInference[strings.TrimSpace(label)]; ok {
		return t
	}
	return ColText
}

// TypeInferenceDict は語→型の推論辞書の写しを返します。
// エディタは /api/tag-schema 経由でこれを受け取り、**同じ辞書**で入力を検証・通知します
// （形式知識の3原則の1: エディタに手書きの語彙を置かない——語彙モデル §7）。
func TypeInferenceDict() map[string]ColumnType {
	out := make(map[string]ColumnType, len(typeInference))
	for k, v := range typeInference {
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

// UnknownVocabTypes は本文中の table/dl が持つ data-type のうち、レジストリに
// 未定義のものを重複なくソートして返します。
//
// 未知の data-type は**通します**（決定ログ: 値は不活性で害がなく、索引に載るだけ。
// 落とす方式はレジストリを安全性の門に変質させる）。この関数は保存時の**告知**
// （拒否ではなくエコーバックの流儀）のために使います。
func UnknownVocabTypes(htmlStr string) []string {
	nodes, err := htmldoc.ParseFragment(htmlStr)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, root := range nodes {
		WalkElements(root, func(n *html.Node) {
			if n.Data != "table" && n.Data != "dl" {
				return
			}
			dt := Attr(n, "data-type")
			if dt == "" || seen[dt] {
				return
			}
			if _, ok := VocabDefByType(dt); !ok {
				seen[dt] = true
			}
		})
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
