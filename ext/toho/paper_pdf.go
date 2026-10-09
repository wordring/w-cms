package toho

// ─────────────────────────────────────────────────────────────────────────
// 紙の PDF に共通の段取り（2026-10-09）
//
// 発注書（order_pdf.go）・見積書（estimate_pdf.go）・見積依頼書（rfq_pdf.go）の3つが、同じ段取りを写していた:
// フォントの確かめ → 紙を始める → 差出人を右に → 備考を紙の幅で折って刷る → 書き出す／作って添付に残しページに
// 表示する／口の前口上と応答。紙ごとに違うのは中身（題・宛名・明細）だけなので、段取りをここに寄せる。
// 資料を綴じた1本（order_docs.go）も同じ口の形。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/signintech/gopdf"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// paperFont は紙に使うフォントの場所です（設定が無い・ファイルが無ければ ErrNoPDFFont を包んで返す）。
// ⚠ 紙の中身を読むより先に確かめます（フォントが無いことを先に言う——口は 503 で答える）。
func paperFont() (string, error) {
	font := PDFFont()
	if font == "" {
		return "", ErrNoPDFFont
	}
	if _, err := os.Stat(font); err != nil {
		return "", fmt.Errorf("%w（いまの設定: %s）", ErrNoPDFFont, font)
	}
	return font, nil
}

// startPaper は A4 の紙を1枚始め、フォントを載せて本文の大きさにします。
func startPaper(font string) (*gopdf.GoPdf, error) {
	p := &gopdf.GoPdf{}
	p.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	p.AddPage()
	if err := addPDFFont(p, font); err != nil {
		return nil, err
	}
	if err := p.SetFont("jp", "", pdfFontSz); err != nil {
		return nil, err
	}
	return p, nil
}

// finishPaper は紙を PDF のバイト列に書き出します。
func finishPaper(p *gopdf.GoPdf) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := p.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// pdfSender は差出人（署名）の行を右側に top から刷り、左側の y と比べて下の方を返します（明細はその下から）。
func pdfSender(p *gopdf.GoPdf, top, y float64, lines []string) float64 {
	ry := top
	for _, ln := range lines {
		ry = pdfText(p, 330, ry, pdfFontSz, ln)
	}
	if ry > y {
		return ry
	}
	return y
}

// pdfNote は「見出し：」の下に備考を1字下げて刷り、次の行の y を返します。
//
// ⚠ **長い行は紙の幅で折り、紙の下に来たら改ページします**——備考は人が自由に書く欄なので、1行に収まる保証が
// ありません（はみ出すと右端で切れて消えます・2026-09-24）。
func pdfNote(p *gopdf.GoPdf, y float64, heading string, lines []string) float64 {
	y = pdfText(p, pdfLeft, y, pdfFontSz, heading+"：")
	for _, ln := range lines {
		parts, err := p.SplitText(ln, pdfRight-pdfLeft-12)
		if err != nil || len(parts) == 0 {
			parts = []string{ln}
		}
		for _, s := range parts {
			if y+pdfFontSz+4 > pdfBottom {
				p.AddPage()
				y = pdfTop
			}
			y = pdfText(p, pdfLeft+12, y, pdfFontSz, s)
		}
	}
	return y
}

// failPaper は紙を組めなかったことを答えます（フォントが無いのは 503・本文が読めないなどは 400）。
func failPaper(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, ErrNoPDFFont) {
		code = http.StatusServiceUnavailable
	}
	cms.JSONFail(w, code, err.Error())
}

// paperKind は紙の種類ごとに違うところです。
type paperKind struct {
	build func(body string, viewer *auth.User) ([]byte, error) // 本文から PDF を組む
	name  func(body, pageID string) string                      // 添付の名前の頭（うしろに日時と .pdf を足す）
	audit string                                                // 監査の名前
	// show は作った PDF をページに表示し、表示できなかった理由を返します（出せたなら空）。
	show func(user *auth.User, pageID, attachID string) string
}

// stampedPDFName は添付の名前です——⚠ **日時を入れます**（同じページで作り直すたびに増えるので、どれがいつのものか
// 分からないと困る・添付は上書きされない）。
func stampedPDFName(head string) string {
	return head + " " + time.Now().Format("20060102-150405") + ".pdf"
}

// makePaper は紙の PDF を作って添付に残し、ページに表示します（関門は呼ぶ側が通す）。断るときは応答を書いて false。
//
// ⚠ **表示に失敗しても PDF は取り消しません**——**紙のほうが重い**ので、「画面に出ない」は人が貼り直せば済みます。
// 理由を添えるだけにします（madeOrderPDF.ViewNote）。
func makePaper(w http.ResponseWriter, user *auth.User, pageID string, k paperKind) (madeOrderPDF, bool) {
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "ページを読めません: "+err.Error())
		return madeOrderPDF{}, false
	}
	pdf, err := k.build(body, user)
	if err != nil {
		failPaper(w, err)
		return madeOrderPDF{}, false
	}
	attachID, fileName, err := cms.SaveAttachmentFrom(pageID, user.Username, stampedPDFName(k.name(body, pageID)), "pdf", pdf)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "保存できません: "+err.Error())
		return madeOrderPDF{}, false
	}
	auth.Audit(user.Username, k.audit, pageID+" "+fileName)
	return madeOrderPDF{AttachID: attachID, File: fileName, ViewNote: k.show(user, pageID, attachID)}, true
}

// showBelowItems は、作った PDF を明細の表（tableType・名前は label）の直後に表示する show です
// （既にマーカーがあれば参照を差し替える——placePDFView）。
func showBelowItems(tableType, label string) func(user *auth.User, pageID, attachID string) string {
	return func(user *auth.User, pageID, attachID string) string {
		ref := pageID + "-" + attachID
		changed := false
		if err := cms.RewriteBody(pageID, user.Username, func(cur string) string {
			out, ok := placePDFView(cur, ref, tableType)
			changed = ok
			return out
		}); err != nil {
			return "⚠ PDFは作りましたが、ページに表示できません: " + err.Error()
		}
		if !changed {
			// ⚠ **表が無ければ置き場所が決まりません。** 黙ると「作ったのに出ない」になるので、そう言います。
			return "⚠ PDFは作りましたが、置き場所が分かりません（" + label + "の表がありません）。添付からは開けます。"
		}
		return ""
	}
}

// servePaper は紙の PDF を作る口（POST・入力 {page_id}）の共通の形です。
//
// 添付を足し、本文にも PDF を開くマーカーを置くので、write 権限と編集中でないこと（gateWritablePage）を見ます
// （⚠ 2026-09-22 から本文も触るようになったので関門が要る——`handler_gate.go`）。
func servePaper(w http.ResponseWriter, r *http.Request, makeFn func(w http.ResponseWriter, user *auth.User, pageID string) (madeOrderPDF, bool)) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	made, ok := makeFn(w, user, pageID)
	if !ok {
		return
	}
	out := map[string]any{"success": true, "attach_id": made.AttachID, "file": made.File, "url": "/" + pageID + "/" + made.File}
	if made.ViewNote != "" {
		out["view_note"] = made.ViewNote
	}
	json.NewEncoder(w).Encode(out)
}
