package toho

import (
	"bytes"
	"compress/zlib"
	"io"
)

// PDF の展開の大きさの見張り（2026-10-26・【考察】コードの矛盾 §4.3）。
//
// 部材発注書・部材見積依頼書に綴じる資料の PDF は、客先や業者から届いたものです。PDF の中の絵や文字は Flate で縮めて
// あり、PDF を取り込む部品（gofpdi）は、それを大きさの上限なしにメモリへ展開します。小さなファイルが展開すると何 GB にも
// なる PDF（展開爆弾）を綴じると、サーバーのメモリが尽きて止まりました（読み込みの時間の上限は、展開の途中には効かない）。
//
// gofpdi に渡す前に、PDF の中の stream を1つずつ、上限つきで展開してみて、合計が pdfInflateBudget を超えるものは断ります。
// 展開できない stream（Flate でないもの・壊れたもの）は数えない（gofpdi がどう扱うかは、そのまま——ここは大きさだけを見る）。

// pdfInflateBudget は、1つの PDF の stream を全部展開したときの大きさの上限です（図面の束でもふつうは数十 MB）。
var pdfInflateBudget int64 = 256 << 20

// pdfInflateWithinBudget は、data の中の stream を展開した合計が上限の内かを返します。
func pdfInflateWithinBudget(data []byte) bool {
	left := pdfInflateBudget
	rest := data
	for {
		i := bytes.Index(rest, []byte("stream"))
		if i < 0 {
			return true
		}
		rest = rest[i+len("stream"):]
		// 「endstream」の stream ではないか・stream のあとの改行（CRLF か LF）。
		if i >= 3 && bytes.HasSuffix(data[:len(data)-len(rest)-len("stream")], []byte("end")) {
			continue
		}
		switch {
		case bytes.HasPrefix(rest, []byte("\r\n")):
			rest = rest[2:]
		case bytes.HasPrefix(rest, []byte("\n")):
			rest = rest[1:]
		default:
			continue
		}
		end := bytes.Index(rest, []byte("endstream"))
		if end < 0 {
			end = len(rest)
		}
		zr, err := zlib.NewReader(bytes.NewReader(rest[:end]))
		if err == nil {
			n, _ := io.Copy(io.Discard, io.LimitReader(zr, left+1))
			zr.Close()
			left -= n
			if left < 0 {
				return false
			}
		}
		rest = rest[end:]
	}
}
