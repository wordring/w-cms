package subcon

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

	"w-cms/internal/cms"
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
// **中身はコアの `cms.NormalizeCode`**（2026-09-04 に引き上げ）。ここで先に
// 見つけた揺れ——空白（`P103-227-6_ 台座Assy`）・アンダースコア（`TQ060_284`）・
// 全角ハイフン・英字の大小・全角英数——が、そのままコアの `code` 型の畳み方に
// なりました。索引の `norm_value` と**同じ規則**なので、突き合わせと検索が食い違いません。
//
// **表示は畳みません**——畳むのは比較のときだけ（「利用者が、『名前：値』のタグは
// 見た目のままにDBに入れられると信じるためには、実際にそうである必要があります」）。
//
// **区切りの有無までは畳みません**（`R520-M070` と `R520M070` は別扱い）——
// 畳み過ぎて別の部品を1つにするほうが、取りこぼしより害が大きい。
func normalizeDrawingNo(s string) string {
	return cms.NormalizeCode(s)
}

// MatchDXFAttachments は、ページに付いているDXF添付のうち図面番号が一致するものを返します。
//
// ⚠ **ZIP の中は見ません**（2026-09-17）。メールの取り込みが ZIP を展開して中身を
// 添付にするので、DXF は常にページ直下に在ります。それまでは `matchDXFInZip` が
// ZIP を読み、参照は ZIP のブロックを指していました（中のファイルにはブロックが無い）。
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
