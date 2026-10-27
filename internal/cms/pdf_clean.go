package cms

// ─────────────────────────────────────────────────────────────────────────
// 外から届いた PDF を、読む前に作り直す（2026-10-27 の見直し）
//
// PDF を取り込む部品（拡張の gofpdi など）は、PDF の中の数（stream の /Length）をそのまま信じて、その大きさのメモリを取ります。
// 数百バイトの PDF に `/Length 1099511627776` と書くだけで、w-cms のプロセスごと落ちました（Go の out of memory は recover で
// 拾えない）。展開の大きさの見張りも、stream の中に文字の `endstream` を混ぜるとすり抜けられました。
//
// そこで、PDF を絵にする道具（設定 `pdf_pages` の tool・mutool）で、まず作り直します（`mutool clean`）。
//   - 作り直した PDF は、stream の /Length が中身と合い、使われていないものが落ちる。
//   - 道具は別のプロセスで、メモリ（1 GiB）と時間（1分）の上限つき。壊れた PDF・爆弾で倒れても、w-cms は倒れない。
//   - 道具が無い（家の Windows など）・設定が無いときは、作り直さずに返す（呼び手が自分の見張りだけで読む）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const (
	pdfCleanTimeout  = time.Minute
	pdfCleanMemBytes = 1 << 30 // 道具の使ってよいメモリ（prlimit --as）
)

// ErrPDFUnreadable は、作り直せなかった PDF です（壊れている・大きすぎる・時間切れ）。
var ErrPDFUnreadable = errors.New("PDF を読めません（壊れているか、大きすぎます）")

// CleanPDF は、外から届いた PDF を mutool で作り直して返します。cleaned は作り直したか（道具が無ければ false で data を返す）。
// 作り直した PDF が maxBytes を超えたら ErrPDFUnreadable。
func CleanPDF(data []byte, maxBytes int64) (out []byte, cleaned bool, err error) {
	conf := PDFPagesConfig()
	if conf == nil {
		return data, false, nil
	}
	tool, err := exec.LookPath(conf.Tool)
	if err != nil {
		return data, false, nil
	}
	dir, err := os.MkdirTemp("", "w-cms-pdf-clean-")
	if err != nil {
		return nil, false, err
	}
	defer os.RemoveAll(dir)
	in, outPath := filepath.Join(dir, "in.pdf"), filepath.Join(dir, "out.pdf")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return nil, false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), pdfCleanTimeout)
	defer cancel()
	args := []string{"clean", "-gg", "-D", in, outPath}
	name := tool
	if pl, err := exec.LookPath("prlimit"); err == nil {
		args = append([]string{"--as=" + strconv.Itoa(pdfCleanMemBytes), "--", tool}, args...)
		name = pl
	}
	if err := exec.CommandContext(ctx, name, args...).Run(); err != nil {
		return nil, false, ErrPDFUnreadable
	}
	fi, err := os.Stat(outPath)
	if err != nil || fi.Size() == 0 || fi.Size() > maxBytes {
		return nil, false, ErrPDFUnreadable
	}
	b, err := os.ReadFile(outPath)
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}
