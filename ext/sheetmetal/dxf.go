package sheetmetal

// ─────────────────────────────────────────────────────────────────────────
// DXF の表題欄を読む（2026-09-03）
//
// 図面（DXF）から**図面番号・図面名称・装置名称…**を取り出します。これは
// 下請け加工屋の業務ジャンルの知識なので、コアではなくこの拡張が持ちます。
//
// **Gemini も画像化も要りません。** 当初は「DXFを画像にしてGeminiに読ませる」案が
// 出ましたが、ASCII形式のDXFは**テキストファイル**で、表題欄の文字は `TEXT`／
// `MTEXT`／`ATTRIB` の値としてそのまま入っています。標準ライブラリ＋既存の
// `x/text` だけで、決定的に・無料で・オフラインで取れます
// （実物の図面で確認: `X008-135-4` / `架台Assy(溶接図）` / `ロングスローボール
// マシーンホッパー付` を抽出）。画像化にはCADレンダラが要り、外部依存の追加は
// 開発方針 §1 の制限にかかります。
//
// **踏んだ罠（実装を触るときは必ず読むこと）**: 和文は Shift_JIS で、
// **復号してから MTEXT の書式コードを剥がす**こと。Shift_JIS は2バイト目が
// ASCII 域に入り、`図` は `0x90 0x7D`——2バイト目が `}` です。生バイトのまま
// `{}` や `\…;` を消すと**文字の途中を切って壊します**（最初は `図面番号` が
// `趨ﾊ番号` に化けました）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// dxfText は図面の中の1つの文字（挿入点つき）です。
type dxfText struct {
	Text string
	X, Y float64
	H    float64 // 文字の高さ（グループコード40）。表題欄の行間の目安になる
}

// mtextFormatRe は MTEXT の書式コード（`\fフォント…;` や囲みの `{}`）です。
// **復号後の文字列にだけ**当てること（上のコメントの罠）。
var mtextFormatRe = regexp.MustCompile(`\\[A-Za-z][^;\\]*;|[{}]`)

// mtextBreakRe は MTEXT の改行（`\P`）です。
var mtextBreakRe = regexp.MustCompile(`\\P`)

// decodeSJIS は Shift_JIS を UTF-8 へ直します（読めなければ原文のまま）。
// 和文CADの既定は Shift_JIS で、UTF-8 で書かれた DXF もそのまま通ります
// （ASCII の範囲は両者で同じ）。
func decodeSJIS(s string) string {
	out, _, err := transform.String(japanese.ShiftJIS.NewDecoder(), s)
	if err != nil {
		return s
	}
	return out
}

// ParseDXFTexts は ASCII DXF から文字を持つ要素（TEXT/MTEXT/ATTRIB）を
// 文書順で取り出します。DXF は「グループコードの行」と「値の行」が交互に並び、
// 要素はコード0で始まり、文字はコード1（MTEXT の続きは3）、挿入点は 10/20 です。
//
// バイナリDXF・DWG は対象外です（先頭が `AutoCAD Binary DXF` なら nil を返す）。
func ParseDXFTexts(content []byte) []dxfText {
	if bytes.HasPrefix(content, []byte("AutoCAD Binary DXF")) {
		return nil // バイナリ形式は読まない（変換器が要る）
	}
	sc := bufio.NewScanner(bytes.NewReader(content))
	sc.Buffer(make([]byte, 1<<20), 1<<20)

	var out []dxfText
	kind, text := "", ""
	var x, y, h float64
	flush := func() {
		if kind != "" && strings.TrimSpace(text) != "" {
			t := decodeSJIS(text) // ← 復号が先（罠）
			t = mtextBreakRe.ReplaceAllString(t, " ")
			t = mtextFormatRe.ReplaceAllString(t, "")
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, dxfText{Text: t, X: x, Y: y, H: h})
			}
		}
		kind, text, x, y, h = "", "", 0, 0, 0
	}
	for sc.Scan() {
		code := strings.TrimSpace(sc.Text())
		if !sc.Scan() {
			break
		}
		value := strings.TrimRight(sc.Text(), "\r")
		switch code {
		case "0":
			flush()
			switch value {
			case "TEXT", "MTEXT", "ATTRIB":
				kind = value
			}
		case "1", "3":
			if kind != "" {
				text += value
			}
		case "10":
			if kind != "" {
				x, _ = strconv.ParseFloat(strings.TrimSpace(value), 64)
			}
		case "20":
			if kind != "" {
				y, _ = strconv.ParseFloat(strings.TrimSpace(value), 64)
			}
		case "40":
			if kind != "" {
				h, _ = strconv.ParseFloat(strings.TrimSpace(value), 64)
			}
		}
	}
	flush()
	return out
}

// titleBlockLabels は表題欄で拾う項目名です。**このジャンルの知識**なので
// ここに置きます（他社の様式なら足す・差し替える）。
//
// 総当たりで「上にある文字をラベルとみなす」形にしないのは、図面本体の寸法値
// どうしが勝手に対になって**ゴミが大量に混じる**ためです。知っている項目だけを
// 拾うほうが、実データに対して確実に効きます。
var titleBlockLabels = []string{
	// 拾いたい項目
	"図面番号", "図面名称", "装置名称", "部品番号", "品番", "品名", "名称",
	"材質", "材料", "板厚", "表面処理", "処理", "質量", "尺度", "個数", "数量",
	"日付", "客先", "客先名", "納期", "注文番号", "製番", "図番",
	// **値としては拾わないが、ラベルだと知っている必要がある語**。
	// 辞書に無い語は「値」と見なされるので、隣のラベルを値として拾ってしまう
	// （実データの構想図で `作成: 確認` という捏造が出た。2026-09-03 ユーザー提案
	// 「ラベルになりやすい文字列もあるので、辞書があっても良いかもしれません」）。
	"作成", "検図", "承認", "確認", "発行", "設計", "製図", "投影法", "単位",
	"備考", "改訂", "訂正",
}

// 値はラベルの**真下**に置かれる、というのが表題欄の作りです。どこまでを
// 「真下」とみなすかが難しいところで、**文字の高さの倍数で決めるのは失敗しました**
// ——実データ911件の調査（2026-09-03）で、行の高さは様式ごとに文字高の2.2倍〜3.5倍まで
// ばらつき、狭い窓では PFC2・P200 の表題欄が丸ごと空振りしました。
//
// そこで窓は**広く取り、代わりに「一番近いラベルのものになる」で決めます**
// （ユーザー提案の「枠線を認識して同じ枠内で対応付ける」の、線を読まない版——
// 枠線が定めているのは要するに「どのラベルの領分か」であり、
// それは最近傍のラベルで決められる）。
//
// 枠線（LINE/LWPOLYLINE）を実際に読む案は、より忠実ですが表題欄が BLOCK 参照に
// なっている図面で INSERT の座標変換を解く必要があり、まだ踏み込んでいません。
// 近接割り当てで取りこぼす様式が出たら、そのときの正当な次の一手です。
const (
	// 和英併記の英語ラベル（`DRAWING NUMBER`・`Quantity`）が和文ラベルの
	// すぐ下か同じ高さに来る。ほぼ同じ高さの文字は値ではない。
	minBelowH = 0.7  // 文字の高さの何倍から下を値とみなすか
	maxBelowH = 12.0 // 広く取る（絞りは下の「最近傍のラベル」が担う）
	minRightH = -2.0
	maxRightH = 8.0
	defaultH  = 2.5 // 高さが取れない要素の既定（MTEXT は 40 を持たないことがある）
)

// asciiWordsRe は「英字と記号だけ・数字を含まない」文字列です。
// 和英併記の**英語ラベル**（`CHECKED BY`・`Material`）を値と取り違えないための網
// ——実際の値は数字を含む（`X008-135-4`・`1`・`2024.09.26`）か、和文を含みます
// （`架台Assy(溶接図）`・`5.3㎏`）。英字だけの値（`ZINC` 等）は取りこぼしますが、
// 英語ラベルを値として記録するより無害です。
var asciiWordsRe = regexp.MustCompile(`^[A-Za-z][A-Za-z &/.,'()-]*$`)

// DXFTitleBlock は表題欄の「ラベル：値」を返します。
//
// 値は**ラベルの真下にある最も近い文字**です。英語の併記（`DRAWING NUMBER` 等）が
// 同じ値を狙うことがありますが、拾うのは `titleBlockLabels` の和文だけなので
// 二重になりません。
func DXFTitleBlock(texts []dxfText) map[string]string {
	isLabel := func(t string) bool {
		return labelSet[strings.ReplaceAll(strings.ReplaceAll(t, " ", ""), "　", "")]
	}

	// 値になりうる文字だけを先に選り分ける（ラベル自身と英語併記を除く）。
	var values []dxfText
	for _, v := range texts {
		if isLabel(v.Text) || asciiWordsRe.MatchString(v.Text) {
			continue
		}
		values = append(values, v)
	}

	// 各値を「一番近いラベル」へ割り当てる——枠線の代わり。広い窓でも
	// 隣の項目の値を盗まないのは、その値がより近いラベルに取られるから。
	type claim struct {
		text string
		dist float64
	}
	best := map[string]claim{}
	for _, v := range values {
		var owner string
		ownerDist := 0.0
		for _, label := range texts {
			if !isLabel(label.Text) {
				continue
			}
			h := label.H
			if h <= 0 {
				h = defaultH
			}
			dy := (label.Y - v.Y) / h
			dx := (v.X - label.X) / h
			if dy < minBelowH || dy > maxBelowH || dx < minRightH || dx > maxRightH {
				continue
			}
			d := dx*dx + dy*dy
			if owner == "" || d < ownerDist {
				owner, ownerDist = strings.ReplaceAll(strings.ReplaceAll(label.Text, " ", ""), "　", ""), d
			}
		}
		if owner == "" {
			continue
		}
		// 1つのラベルが複数の値を主張されたら、近いほうが勝つ。
		if c, ok := best[owner]; !ok || ownerDist < c.dist {
			best[owner] = claim{v.Text, ownerDist}
		}
	}

	out := map[string]string{}
	for k, c := range best {
		out[k] = c.text
	}
	return out
}

// labelSet は titleBlockLabels の逆引きです。
var labelSet = func() map[string]bool {
	m := map[string]bool{}
	for _, l := range titleBlockLabels {
		m[l] = true
	}
	return m
}()

// KnownTitleBlockLabels は辞書に載っているラベルを返します（調査・テスト用）。
func KnownTitleBlockLabels() []string { return append([]string(nil), titleBlockLabels...) }
