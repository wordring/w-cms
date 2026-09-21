package subcon

// ─────────────────────────────────────────────────────────────────────────
// 寸法を、検索できる形に分解する（2026-09-21）
//
// ユーザー:「材料の見積もりや発注には価格があるので、あとで**材質形状寸法で検索**
// したいときがあります。**問題は寸法**ですが、検索できるように**分解してDBに入れる**
// ことは出来ますか？これは**汎用の表検索とは別のDB**に入れることになると思います」。
//
// 実データの寸法はこういう形です:
//
//	□75*75*t3.2*1090   角パイプ
//	t4.5*75*1090        FB（平鋼）
//	Φ40*t7*301          パイプ
//	C125*65*t6*507      チャンネル
//	t3.4*定尺           ⚠ 数でない語が混ざる
//
// ⚠ **表記そのものが言っていることだけを読みます。** `t` は厚み、`φ` は径、`□` は角
// ——**これは書いてあります**。残りの裸の数は**役割が分かりません**ので、順不同の
// `寸法値` として持ちます。
//
// ⚠ **いちばん大きい数を「長さ」と決めつけません。** たいていはそうですが、
// `Φ16*55` の 55 は長さ、`Φ20*43.5` の 43.5 も長さ……と例外がすぐ出ます。
// **機械は言えることだけ言う**——役割を当てにいくと、当たったときだけ便利で、
// 外れたときは**黙って間違えます**。
//
// ⚠ **役割を全部に付けるには「形状ごとのスキーマ」が要ります**（`□75*75` の 75 は辺、
// `t4.5*75` の 75 は幅）。形状は自由文（`角パイプ`・`チャンネル`・`FB`）なので、
// **形状が増えるたびに定義が要ります**。順不同の `寸法値` なら
// 「厚み3.2 かつ 1090 を含む」で引けて、定義は要りません。
//
// 正本は [docs/【考察】材料の単価と価格の履歴.md]。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"w-cms/internal/cms"
)

// 寸法の断片が持つ役割です。⚠ **表記が言っているものだけ**が名前を持ちます。
const (
	RoleThickness = "厚み"  // `t3.2`
	RoleDiameter  = "径"   // `Φ40`（畳むと `φ40`）
	RoleSquare    = "角"   // `□75`
	RoleThread    = "ねじ"  // `M16`
	RoleValue     = "寸法値" // 裸の数（役割は分からない）
	RoleMark      = "印"   // `定尺` のような数でない語
)

// DimPart は寸法を分解した1片です。
type DimPart struct {
	Seq  int     // 書かれた順（0始まり）
	Role string  // 上の定数
	Num  float64 // 数（`印` のときは 0）
	Raw  string  // 書かれたまま（正本）
}

// HasNum は数を持つ片かを返します（`印` だけが false）。
func (p DimPart) HasNum() bool { return p.Role != RoleMark }

// ParseDimension は寸法の文字列を分解します。
//
// ⚠ **区切りは `*`・`×`・`x`・`X`** です（実データに出るもの）。
// ⚠ **`NormalizeText` を通してから読みます**——`Φ`・`⌀`・`Ø` は設定の畳み表
// （`char_folding`）で `φ` に揃います。**直径記号は系統から違う文字**なので、
// 揃えないと `Φ40` と `φ40` が別物になります。
//
// ⚠ **読めない断片も落としません**（`定尺` は `印` として残す）。畳めなかった納期を
// 落とさないのと同じ——**落とすと、書いてあるのに出てこない**ことになります。
func ParseDimension(s string) []DimPart {
	s = cms.NormalizeText(s)
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []DimPart
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		return r == '*' || r == '×' || r == 'x' || r == 'X'
	}) {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		p := DimPart{Seq: len(out), Raw: tok, Role: RoleMark}
		if prefix, num, ok := splitDimToken(tok); ok {
			p.Num, p.Role = num, roleOfPrefix(prefix)
		}
		out = append(out, p)
	}
	return out
}

// splitDimToken は「接頭辞＋数」に割ります（数で終わらなければ ok=false）。
//
// ⚠ **接尾辞は認めません**——`75mm` のような書き方は実データに出ませんし、
// 認めると `t3.2定尺` のような**続けて書かれた語**まで数として読んでしまいます。
func splitDimToken(tok string) (prefix string, num float64, ok bool) {
	i := 0
	for i < len(tok) {
		r, size := utf8.DecodeRuneInString(tok[i:])
		if unicode.IsDigit(r) || r == '.' || r == '-' {
			break
		}
		if unicode.IsLetter(r) || isDimSymbol(r) {
			prefix += string(r)
			i += size
			continue
		}
		return "", 0, false // 認めない記号が混ざっている
	}
	rest := tok[i:]
	if rest == "" {
		return "", 0, false
	}
	v, err := strconv.ParseFloat(rest, 64)
	if err != nil {
		return "", 0, false
	}
	return prefix, v, true
}

// isDimSymbol は寸法の接頭辞として認める記号かを返します。
// ⚠ `Φ`・`⌀`・`Ø` は `NormalizeText` が `φ` へ畳んだあとなので、ここには `φ` だけ来ます。
func isDimSymbol(r rune) bool { return r == '□' || r == 'φ' }

// roleOfPrefix は接頭辞から役割を決めます。
//
// ⚠ **知らない接頭辞は `寸法値` にします**（`C125` の `C` はチャンネルの記号）。
// 役割は名乗れませんが、**数は検索できる**ほうが役に立ちます——「読めないから捨てる」
// のではなく「読めたところまで入れる」。
func roleOfPrefix(prefix string) string {
	switch strings.ToLower(prefix) {
	case "t":
		return RoleThickness
	case "φ":
		return RoleDiameter
	case "□":
		return RoleSquare
	case "m":
		return RoleThread
	}
	return RoleValue
}
