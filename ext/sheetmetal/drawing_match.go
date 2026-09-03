package sheetmetal

// ─────────────────────────────────────────────────────────────────────────
// 図面PDFと添付DXFの突き合わせ（2026-09-03）
//
// ユーザー:「主役がメール添付のPDFであるのは間違いないです。**PDFにプラスして
// DXFがある場合が大部分**です。そのため、DXFの図面名称と図面番号をPDFと付き合わせて
// **同じ部品の図面と認識するために解析が必要**ということです」
//
// 1通のメールにPDFが複数・DXFが複数入るとき、**どのDXFがどの部品のものか**を決めます。
// 手掛かりは図面番号——PDF側は Gemini の解析結果から、DXF側は表題欄から
// （`dxf.go`・**Gemini不要・無料・決定的**）。ユーザー:「PDF図面はGeminiで解析すれば、
// 突き合わせも簡単では」——その通りで、**高い側は既に払っている解析1回だけ**で済みます。
//
// メールで届くDXFはお客様の図面型で、実データでは**11件中10件から表題欄が取れます**
// （自社のCAM用DXFは表題欄を持たないが、そちらは取り込み口を通らない。
// 相関表は docs/作業引き継ぎ.md）。
//
// **図面番号は一意ではありません**——ユーザー:「別の製品の図面番号が一致してしまう
// 場合もあり、その場合には、社内で識別番号を割り当てるしかありません」。だから
// ここでやるのは**同じ1通のメールの中での突き合わせ**に限ります。過去のページを
// 番号で探して勝手に束ねることはしません（同一性を担うのは常にページID）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"os"
	"path/filepath"
	"strings"

	"w-cms/internal/cms/page"
)

// matchedDXF は図面PDFと対応づいたDXF添付です。
type matchedDXF struct {
	AttachID string // 添付ID（＝保存名から拡張子を除いたもの）。参照タグの値に使う
	Number   string // DXFの表題欄から読んだ図面番号
	Name     string // 同・図面名称
}

// normalizeDrawingNo は図面番号を突き合わせ用に畳みます。
//
// **表示は畳みません**——畳むのは比較のときだけ。索引へ入るタグの値は
// 見た目のままです（「利用者が、『名前：値』のタグは見た目のままにDBに入れられると
// 信じるためには、実際にそうである必要があります」）。
//
// 畳むのは、実データで実際にぶつかった揺れだけ: 空白（`X008-135-4_ 架台Assy` の
// ように混ざる）・アンダースコア（`PW050_167`）・全角ハイフン・英字の大小・全角英数。
//
// **区切りの有無までは畳みません**（`W470-L090` と `W470L090` は別扱い）——
// 畳み過ぎて別の部品を1つにするほうが、取りこぼしより害が大きい。
func normalizeDrawingNo(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ' || r == '\u3000':
			continue
		case r == '_':
			// **アンダースコアはハイフンとみなす**（消さない）。実データの
			// `PW050_167-ソレノイド補助具` は、ハイフンが来る位置に `_` が入っている。
			b.WriteRune('-')
		case r == '\uff0d' || r == '\u2010' || r == '\u2011' || r == '\u2012' ||
			r == '\u2013' || r == '\u2014' || r == '\u30fc':
			b.WriteRune('-') // 全角ハイフン・ダッシュ・長音の類を半角ハイフンへ
		case r >= '\uff21' && r <= '\uff3a': // 全角英大文字
			b.WriteRune(r - 0xff21 + 'A')
		case r >= '\uff41' && r <= '\uff5a': // 全角英小文字
			b.WriteRune(r - 0xff41 + 'A')
		case r >= '\uff10' && r <= '\uff19': // 全角数字
			b.WriteRune(r - 0xff10 + '0')
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// MatchDXFAttachments は、ページに付いているDXF添付のうち図面番号が一致するものを返します。
//
// drawingNo が空なら何も返しません——**空文字どうしが一致してしまう**と、表題欄が
// 未記入のDXF（構想図など）が無関係な図面へ全部ぶら下がります。
func MatchDXFAttachments(pageID, drawingNo string) []matchedDXF {
	want := normalizeDrawingNo(drawingNo)
	if want == "" {
		return nil
	}
	entries, err := os.ReadDir(page.AttachmentDir(pageID))
	if err != nil {
		return nil
	}
	var out []matchedDXF
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".dxf") {
			continue
		}
		path, found := page.AttachmentPath(pageID, e.Name())
		if !found {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		f := DXFTitleBlock(ParseDXFTexts(content))
		if normalizeDrawingNo(f["図面番号"]) != want {
			continue
		}
		out = append(out, matchedDXF{
			AttachID: strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())),
			Number:   f["図面番号"],
			Name:     f["図面名称"],
		})
	}
	return out
}
