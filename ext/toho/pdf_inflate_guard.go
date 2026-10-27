package toho

import (
	"bytes"
	"compress/zlib"
	"io"
	"regexp"
	"strconv"
)

// PDF の展開の大きさの見張り（2026-10-26・【考察】コードの矛盾 §4.3）。
//
// 部材発注書・部材見積依頼書に綴じる資料の PDF は、客先や業者から届いたものです。PDF の中の絵や文字は Flate で縮めて
// あり、PDF を取り込む部品（gofpdi）は、それを大きさの上限なしにメモリへ展開します。小さなファイルが展開すると何 GB にも
// なる PDF（展開爆弾）を綴じると、サーバーのメモリが尽きて止まりました（読み込みの時間の上限は、展開の途中には効かない）。
//
// gofpdi に渡す前に、PDF の中の stream を1つずつ、上限つきで展開してみて、合計が pdfInflateBudget を超えるものは断ります。
// 展開できない stream（Flate でないもの・壊れたもの）は数えない（gofpdi がどう扱うかは、そのまま——ここは大きさだけを見る）。
//
// ⚠ 2026-10-27 の見直しで、2つの穴が見つかりました。
//   - stream の切り方: それまでは最初の文字の `endstream` で切っていたので、縮めたデータの中に `endstream` を混ぜると、その先を
//     展開せずに「大丈夫」と答えた（gofpdi は /Length のとおり全部を展開する）。いまは gofpdi と同じく /Length で切る。
//   - /Length そのもの: gofpdi は /Length の大きさのメモリを先に取るので、ファイルより大きな /Length（1 TB など）だけで落ちた。
//     /Length がファイルの残りより大きい PDF は断る。
// 外から届いた PDF は、先に cms.CleanPDF（mutool）で作り直してからここへ来る（order_docs.go）。作り直した PDF の /Length は
// 中身と合っていて、ここの /Length で切る見張りが正しく効く。

// pdfInflateBudget は、1つの PDF の stream を全部展開したときの大きさの上限です（図面の束でもふつうは数十 MB）。
var pdfInflateBudget int64 = 256 << 20

// pdfLengthRe は stream の辞書の /Length です（直に数か、間接の参照「N G R」）。
var pdfLengthRe = regexp.MustCompile(`/Length\s+(\d+)(?:\s+(\d+)\s+R)?`)

// pdfObjIntRe は「N G obj 数 endobj」の形の、数だけを持つ間接のオブジェクトです（/Length の参照先）。
func pdfObjInt(data []byte, num, gen string) (int64, bool) {
	re, err := regexp.Compile(`(?:^|[^0-9])` + num + `\s+` + gen + `\s+obj\s*(\d+)\s*endobj`)
	if err != nil {
		return 0, false
	}
	m := re.FindSubmatch(data)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(string(m[1]), 10, 64)
	return n, err == nil
}

// pdfInflateWithinBudget は、data の中の stream を展開した合計が上限の内か（と、どの /Length もファイルに収まるか）を返します。
// strict なら、/Length の読めない stream も断る（cms.CleanPDF で作り直した PDF は、/Length を必ず直に書く）。
func pdfInflateWithinBudget(data []byte, strict bool) bool {
	left := pdfInflateBudget
	pos := 0
	for {
		i := bytes.Index(data[pos:], []byte("stream"))
		if i < 0 {
			return true
		}
		at := pos + i
		pos = at + len("stream")
		// 「endstream」の stream ではないか。
		if at >= 3 && bytes.Equal(data[at-3:at], []byte("end")) {
			continue
		}
		// stream の辞書（直前の「obj」から「stream」まで）の /Length。
		head := data[:at]
		from := bytes.LastIndex(head, []byte("obj"))
		if from < 0 {
			from = 0
		}
		start := pos
		for start < len(data) && (data[start] == '\r' || data[start] == '\n' || data[start] == ' ' || data[start] == '\t') {
			start++
		}
		length := int64(-1)
		if m := pdfLengthRe.FindSubmatch(head[from:]); m != nil {
			if m[2] == nil {
				length, _ = strconv.ParseInt(string(m[1]), 10, 64)
			} else if n, ok := pdfObjInt(data, string(m[1]), string(m[2])); ok {
				length = n
			}
		}
		var body []byte
		switch {
		case length > int64(len(data)-start):
			return false // ファイルより大きな /Length——gofpdi がその大きさのメモリを取って落ちる
		case length >= 0:
			body = data[start : start+int(length)]
		case strict:
			return false
		default:
			// /Length が読めない（作り直していない PDF）——次の endstream まで（すり抜けの穴は残る・家の開発機だけ）。
			end := bytes.Index(data[start:], []byte("endstream"))
			if end < 0 {
				end = len(data) - start
			}
			body = data[start : start+end]
		}
		zr, err := zlib.NewReader(bytes.NewReader(body))
		if err == nil {
			n, _ := io.Copy(io.Discard, io.LimitReader(zr, left+1))
			zr.Close()
			left -= n
			if left < 0 {
				return false
			}
		}
		pos = start + len(body)
	}
}
