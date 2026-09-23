package subcon

// ─────────────────────────────────────────────────────────────────────────
// `.ttc`（フォントコレクション）から1書体を取り出す（2026-09-23）
//
// ユーザー:「**PDFの文字が細くてかすれてるような気がします**」。
//
// ⚠ **原因はフォントの既定の太さでした。** それまでの設定は Windows の
// `NotoSansJP-VF.ttf`——**可変フォント**で、`fvar` を読むと **`wght` の既定値が
// 100（Thin）**です。⚠ **gopdf は可変軸を扱わないので、いちばん細い形がそのまま
// 刷られていました**（実測で確認）。
//
// ⚠ **太い和文フォントは、Windows では全部 `.ttc` です**（メイリオ・游ゴシック・
// BIZ UD・MSゴシック）。gopdf は `.ttc` を読めないので、**それまでは選べません**でした
// ——「`.ttc` は読めません」という断り書きが、実は**いちばん読みやすいフォントを
// 締め出していた**わけです。
//
// ⚠ **重ね刷り（疑似ボールド）は採りませんでした。** 同じ文字を少しずらして4回書けば
// 太く見えますが、⚠ **PDFから取り出す文字が4重になります**——`発注書発注書発注書発注書`。
// 相手がコピーしたときも、**こちらが後でその紙を機械に読ませるとき**も壊れます。
// **見た目のために、中身を壊さない。**
//
// ── `.ttc` の形 ────────────────────────────────────────────────────────
//
//	'ttcf' | 版 | 書体の数 | 書体0の位置 | 書体1の位置 | …
//	                          ↓
//	                      ふつうのTTFの表の目次（表の位置は**ファイル先頭からの絶対値**）
//
// **共有されている表を各書体が指す**のがコレクションの要点で、だから取り出すときは
// **目次を作り直して、指している表の中身を並べ直す**だけで済みます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/signintech/gopdf"
)

// ErrNotTTC は「これはコレクションではない」です（呼ぶ側が素の TTF として扱う印）。
var ErrNotTTC = errors.New("フォントコレクション（.ttc）ではありません")

// addPDFFont は設定のフォントを読み込みます（`.ttc` なら1書体を取り出して渡す）。
//
// ⚠ **書体は設定で選べます**（`pdf_font_face`・既定は0）。`.ttc` には**似た書体が
// 何本も入っています**——`BIZ-UDGothicB.ttc` なら 0 が BIZ UDゴシック B、
// 1 が BIZ UDPゴシック B（プロポーショナル）。**どちらが好みかは人が決めます。**
func addPDFFont(p *gopdf.GoPdf, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("フォントを読めません（%s）: %w", path, err)
	}
	if !IsTTC(data) {
		if err := p.AddTTFFontData("jp", data); err != nil {
			return fmt.Errorf("フォントを読めません（%s）: %w", path, err)
		}
		return nil
	}
	face, err := TTCFace(data, PDFFontFace())
	if err != nil {
		return fmt.Errorf("フォントを読めません（%s）: %w", path, err)
	}
	if err := p.AddTTFFontData("jp", face); err != nil {
		return fmt.Errorf("フォントを読めません（%s の書体 %d）: %w",
			path, PDFFontFace(), err)
	}
	return nil
}

// IsTTC は先頭の4バイトで見分けます（⚠ 拡張子では見ません——名前は変えられます）。
func IsTTC(data []byte) bool {
	return len(data) >= 4 && string(data[:4]) == "ttcf"
}

// TTCFaceCount はコレクションに入っている書体の数を返します。
func TTCFaceCount(data []byte) (int, error) {
	if !IsTTC(data) {
		return 0, ErrNotTTC
	}
	if len(data) < 12 {
		return 0, errors.New("フォントコレクションの頭が壊れています")
	}
	return int(binary.BigEndian.Uint32(data[8:12])), nil
}

// TTCFace はコレクションの index 番目を、独立した TTF のバイト列にして返します。
//
// ⚠ **表の中身は写します**（指し直しません）。コレクションは表を共有しているので、
// 位置だけ書き換えると**他の書体の表を指したまま**になります。
//
// ⚠ **`head` のチェックサムは直しません。** PDFに埋め込むだけなら誰も検算せず、
// 直すには**ファイル全体の総和**が要ります（鶏と卵）。⚠ **フォントを配る用途には
// 使わないこと**——ここは「刷るために読む」ためだけの口です。
func TTCFace(data []byte, index int) ([]byte, error) {
	n, err := TTCFaceCount(data)
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= n {
		return nil, fmt.Errorf("書体 %d はありません（このコレクションには %d 書体）", index, n)
	}
	off := binary.BigEndian.Uint32(data[12+4*index : 16+4*index])
	if int(off)+12 > len(data) {
		return nil, errors.New("書体の位置がファイルの外を指しています")
	}
	numTables := int(binary.BigEndian.Uint16(data[off+4 : off+6]))
	if numTables == 0 || int(off)+12+16*numTables > len(data) {
		return nil, errors.New("表の目次が壊れています")
	}

	type table struct {
		tag      string
		checksum uint32
		data     []byte
	}
	// ⚠ **表の記録は `tag(4) / checkSum(4) / offset(4) / length(4)`** です。
	//    切り出しを取り違えると、**長さを位置として読んで**黙って別の場所を写します
	//    ——フォントは壊れますが、**エラーは出ません**（豆腐が並ぶだけ）。
	tables := make([]table, 0, numTables)
	for i := 0; i < numTables; i++ {
		r := int(off) + 12 + 16*i
		tag := string(data[r : r+4])
		sum := binary.BigEndian.Uint32(data[r+4 : r+8])
		tblOff := int(binary.BigEndian.Uint32(data[r+8 : r+12]))
		tblLen := int(binary.BigEndian.Uint32(data[r+12 : r+16]))
		if tblOff < 0 || tblLen < 0 || tblOff+tblLen > len(data) {
			return nil, fmt.Errorf("表 %q がファイルの外を指しています", tag)
		}
		tables = append(tables, table{tag: tag, checksum: sum, data: data[tblOff : tblOff+tblLen]})
	}
	// 目次は tag の昇順（仕様）。
	sort.Slice(tables, func(i, j int) bool { return tables[i].tag < tables[j].tag })

	// 出力を組む: 頭（12）＋ 目次（16×表の数）＋ 表の中身（4バイト境界に揃える）。
	head := 12 + 16*len(tables)
	total := head
	for _, t := range tables {
		total += (len(t.data) + 3) &^ 3
	}
	out := make([]byte, head, total)
	copy(out[0:4], data[off:off+4]) // sfntVersion（0x00010000 か 'OTTO'）
	binary.BigEndian.PutUint16(out[4:6], uint16(len(tables)))
	// searchRange / entrySelector / rangeShift。⚠ **読み手はたいてい使いませんが、
	//    0 のままにすると検査の厳しい実装で弾かれます**。
	sel := 0
	for 1<<(sel+1) <= len(tables) {
		sel++
	}
	binary.BigEndian.PutUint16(out[6:8], uint16(16*(1<<sel)))
	binary.BigEndian.PutUint16(out[8:10], uint16(sel))
	binary.BigEndian.PutUint16(out[10:12], uint16(16*len(tables)-16*(1<<sel)))

	for i, t := range tables {
		r := 12 + 16*i
		copy(out[r:r+4], t.tag)
		binary.BigEndian.PutUint32(out[r+4:r+8], t.checksum)
		binary.BigEndian.PutUint32(out[r+8:r+12], uint32(len(out)))
		binary.BigEndian.PutUint32(out[r+12:r+16], uint32(len(t.data)))
		out = append(out, t.data...)
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
	}
	return out, nil
}
