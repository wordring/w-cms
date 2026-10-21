package toho

// 部材見積依頼の返事ページから Gemini に読ませる（OCR・2026-10-21 夜）。
//
// > 2026-10-21 利用者:「部材見積依頼の返事ページからGeminiにOCRを依頼できるようにしてください」
//
// それまで、返事を Gemini に読ませる入口は2つでした——通信記録の 🤖 解析（返事ページを作るとき）と、部材見積依頼書の
// 「🤖 返事を読む」（単価の案を出すとき）。返事ページそのものには無く、手で書く返事ページや、あとから FAX・写真を置いた
// 返事ページは、読んだままの表を人が全部写すしかありませんでした。
//
//	POST /api/rfq-reply/ocr {page_id（返事ページ）, source_page, source_file}
//	  … 選んだ返事（原本＝受信元の添付かメールの本文・このページに置いた PDF と画像）を Gemini に読ませ、
//	    「業者の返事（読んだまま）」の表を読んだもので置き換える。仕入先・読んだ部材見積依頼番号は空のときだけ埋める。
//	    まだ結んでいなければ、候補が1つ（仕入先も食い違わない）のとき結ぶ（makeRFQReply と同じ決まり）。
//
// ⚠ 読ませてよいのは、この返事ページの候補（rfqReplyOwnSources）だけ——口に渡されたページ・ファイルを候補と突き合わせる。
// ⚠ 値は書かれているまま（計算させない）。単価を部材見積依頼明細表へ写すのは、いままでどおり部材見積依頼書の「🤖 返事を読む」か、
// 「この返事から部材見積依頼書ページを作る」で、人が案を確かめてから。

import (
	"encoding/json"
	"errors"
	stdhtml "html"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/generative-ai-go/genai"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// rfqReplyOwnSources は、返事ページ replyID の 🤖 で読める候補です——原本（受信元の添付、無ければメールの本文）と、
// このページに置いた PDF・画像（新しい順）。
func rfqReplyOwnSources(user *auth.User, replyID string, tags map[string][]string) []rfqReplySource {
	var out []rfqReplySource
	host, attach, hasAttach := strings.Cut(cms.FirstTag(tags, SourceRefTag), "-")
	if hostID, ok := page.NormalizeID(strings.TrimSpace(host)); ok && page.CanView(user, pageNum(hostID)) {
		if !hasAttach {
			out = append(out, rfqReplySource{Label: "原本: メールの本文", PageID: hostID})
		} else if stored, name, ok := cms.AttachmentOfRef(user, cms.FileRef{PageID: hostID, ID: attach}); ok {
			if _, okExt := rfqReadableExt[strings.ToLower(filepath.Ext(stored))]; okExt {
				out = append(out, rfqReplySource{Label: "原本: " + name, PageID: hostID, File: stored})
			}
		}
	}
	return append(out, pageFileSources(replyID, "このページ: ", nil)...)
}

// rfqReplyFilePrompt は返事（ファイルかメールの本文）を、返事ページの「読んだまま」の表に読ませる指示です（%TEXT% にメールの本文）。
const rfqReplyFilePrompt = `あなたは、弊社が業者へ出した見積依頼への**業者の返事**を読む係です。返事は添付のファイル（業者の見積書、弊社の見積依頼書に手書きで書き込んだFAX、図面に数量と単価を書き込んだもの、写真）か、下のメールの本文です。
書かれていることを、次の形式のJSONオブジェクトだけで答えてください（マークダウンのコードブロック修飾は付けない）:
{"rfq_no": "弊社の見積依頼書の番号（「№」の後ろの数字）が書かれていればそのまま（無ければ空文字）",
 "supplier": "返事をくれた業者の会社名（無ければ空文字）",
 "date": "返事の日付を YYYY-MM-DD で（無ければ空文字）",
 "source_table": {"headers": ["表の見出し（書かれている言葉で）"], "rows": [["1行ぶんの値を書かれているまま"]]}}
- source_table には、返事に書かれた品物（品番・図面番号・品名・寸法）・数量・単価・金額・条件などを、1品1行の表にしてください。
- 値は書かれているまま写してください。⚠ 計算はしないでください（合計や単価を求めない）。読めない字は「?」にしてください。
- 図面への書き込みは、図面の表題欄の図面番号・図面名称を品物として行に入れてください（「50ヶ @ 2,900」なら数量 50・単価 2,900）。
- 「>」で始まる行は前のやり取り（弊社の見積依頼）の引用です。返事の値段ではないので表に入れないでください。
%TEXT%`

// rfqReplyOCR は返事を読む入口です（試験が偽物へ差し替えられるよう変数）。
var rfqReplyOCR = func(prompt string, blobs ...genai.Blob) (string, error) {
	return cms.GeminiGenerateBlobs(prompt, blobs...)
}

// parseRFQReplyFile は 🤖 の返答を読みます。
func parseRFQReplyFile(resp string) (*rfqReplyJudgment, error) {
	var j rfqReplyJudgment
	if err := json.Unmarshal([]byte(cms.StripJSONFence(resp)), &j); err != nil {
		head := []rune(strings.TrimSpace(cms.StripJSONFence(resp)))
		if len(head) > 300 {
			head = append(head[:300], '…')
		}
		return nil, errors.New("読んだ結果を読めません: " + err.Error() + "（返ってきたもの: " + string(head) + "）")
	}
	j.SourceTable = parseSourceTable(j.SourceTableRaw)
	return &j, nil
}

// applyRFQReplyOCR は返事ページの本文 body に、読んだもの r を入れます——「業者の返事（読んだまま）」を r の表で置き換え
// （枠が無ければ末尾に作る）、仕入先・読んだ部材見積依頼番号は、本文で空のときだけ埋めます。
// 表が読めなければ ok=false（何も変えない）。
//
// 仕入先・読んだ番号が空かは、渡された body（ロックの中で読んだ本文）で見る——索引で見ると、読んでいる何十秒かのあいだに
// 人が書いた値を上書きした（2026-10-22 の見直し）。
func applyRFQReplyOCR(body string, r *rfqReplyJudgment, supplier string) (string, bool) {
	tbl := sourceTableHTML(r.SourceTable)
	if tbl == "" {
		return body, false
	}
	have := func(name string) bool { return strings.TrimSpace(bodyTagValue(body, name)) != "" }
	d := cms.EditBody(body)
	if !have(SupplierTag) && strings.TrimSpace(supplier) != "" {
		d.SetTag(SupplierTag, supplier)
	}
	if no := cms.NormalizeNameForIngest(r.RFQNo); no != "" && !have(RFQReplyReadNoTag) {
		d.SetTag(RFQReplyReadNoTag, no)
	}
	if box, ok := d.Container(rfqReplySourceCaption); ok {
		box.SetContent(tbl)
		return d.HTML(), true
	}
	return strings.TrimRight(d.HTML(), "\n") + "\n<details open><summary>" + rfqReplySourceCaption + "</summary>" + tbl + "</details>", true
}

// RFQReplyOCRAPIHandler は POST /api/rfq-reply/ocr です（入力: {page_id（返事ページ）, source_page, source_file}）。
func RFQReplyOCRAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID     string `json:"page_id"`
		SourcePage string `json:"source_page"`
		SourceFile string `json:"source_file"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	replyID, ok := gateWritablePage(w, r, req.PageID)
	if !ok {
		return
	}
	tags, err := cms.TagsOfPage(database.DB, pageNum(replyID))
	if err != nil || !isRFQReplyTags(tags) {
		cms.JSONFail(w, http.StatusBadRequest, "部材見積依頼の返事ページではありません")
		return
	}
	srcPage, _ := page.NormalizeID(strings.TrimSpace(req.SourcePage))
	srcFile := strings.TrimSpace(req.SourceFile)
	var src *rfqReplySource
	for _, s := range rfqReplyOwnSources(user, replyID, tags) {
		if s.PageID == srcPage && s.File == srcFile {
			s := s
			src = &s
			break
		}
	}
	if src == nil {
		cms.JSONFail(w, http.StatusNotFound, "読む返事が見つかりません（この返事ページの原本か、このページに置いた PDF・画像を選んでください）")
		return
	}
	var blobs []genai.Blob
	text := ""
	if src.File != "" {
		mime := rfqReadableExt[strings.ToLower(filepath.Ext(src.File))]
		path, okPath := page.AttachmentPath(src.PageID, src.File)
		data, err := os.ReadFile(path)
		if !okPath || err != nil {
			cms.JSONFail(w, http.StatusNotFound, "返事のファイルを読めません")
			return
		}
		if len(data) > rfqReplyMaxBytes {
			cms.JSONFail(w, http.StatusRequestEntityTooLarge, "返事のファイルが大きすぎます（18MBまで）")
			return
		}
		blobs = append(blobs, genai.Blob{MIMEType: mime, Data: data})
	} else {
		srcBody, err := cms.ReadPageBody(src.PageID)
		mail := ""
		if err == nil {
			mail = strings.TrimSpace(comm.RecordBodyText(srcBody))
		}
		if mail == "" {
			cms.JSONFail(w, http.StatusBadRequest, "メールの本文が空です（PDF・画像を選んでください）")
			return
		}
		text = "返事のメールの本文:\n" + mail
	}
	resp, err := rfqReplyOCR(strings.Replace(rfqReplyFilePrompt, "%TEXT%", text, 1), blobs...)
	if err != nil {
		code, msg := aiFailure(err, "読めませんでした")
		cms.JSONFail(w, code, msg)
		return
	}
	j, err := parseRFQReplyFile(resp)
	if err != nil {
		cms.JSONFail(w, http.StatusBadGateway, err.Error())
		return
	}
	supplier := ""
	if strings.TrimSpace(j.Supplier) != "" {
		supplier = contacts.OrgNameForPage(user, j.Supplier)
	}
	applied := false
	if !rewriteBodyOrFail(w, replyID, user.Username, func(cur string) string {
		out, ok := applyRFQReplyOCR(cur, j, supplier)
		applied = ok
		return out
	}) {
		return
	}
	if !applied {
		cms.JSONFail(w, http.StatusBadGateway, "返事から表を読めませんでした（読めた品物がありません）")
		return
	}
	auth.Audit(user.Username, "rfq-reply.ocr", replyID+" ← "+src.PageID+"/"+src.File)
	say := "読みました。「" + rfqReplySourceCaption + "」の表を、読んだもので置き換えました（" + strconv.Itoa(len(j.SourceTable.Rows)) + " 行）。"
	linked := ""
	if strings.TrimSpace(cms.FirstTag(tags, RFQReplyLinkTag)) == "" {
		now, _ := cms.TagsOfPage(database.DB, pageNum(replyID))
		host, _, _ := strings.Cut(cms.FirstTag(now, SourceRefTag), "-")
		sup := cms.FirstTag(now, SupplierTag)
		cands := rfqReplyCandidates(user, host, cms.FirstTag(now, RFQReplyReadNoTag), sup)
		if len(cands) == 1 && (sup == "" || sameCompany(sup, cms.PageTagValue(database.DB, pageNum(cands[0]), SupplierTag))) {
			if err := linkRFQReply(user, replyID, cands[0]); err == nil {
				linked = cands[0]
				say += rfqReplySay(rfqReplyMade{PageID: replyID, Linked: linked})
			}
		}
	}
	cms.WriteJSON(w, map[string]any{"success": true, "rows": len(j.SourceTable.Rows), "linked": linked, "say": say})
}

// rfqReplyOCRPanelHTML は返事ページの「🤖 Gemini で読む（OCR）」の欄です（書ける人だけ・候補が無ければ断り）。
func rfqReplyOCRPanelHTML(user *auth.User, replyID string, tags map[string][]string) string {
	if !canWritePage(user, pageNum(replyID)) {
		return ""
	}
	srcs := rfqReplyOwnSources(user, replyID, tags)
	var b strings.Builder
	b.WriteString(`<p class="unorder-help rfq-reply-ocr-row">🤖 Gemini で読む（OCR）: `)
	if len(srcs) == 0 {
		b.WriteString(`読めるものがありません（返事の PDF か画像を、このページに置いてください）。</p>`)
		return b.String()
	}
	b.WriteString(`<select class="rfq-reply-ocr-src">`)
	for _, s := range srcs {
		b.WriteString(`<option value="` + stdhtml.EscapeString(s.PageID+"|"+s.File) + `">` + stdhtml.EscapeString(s.Label) + `</option>`)
	}
	b.WriteString(`</select> <button type="button" class="chip-btn rfq-reply-ocr" data-reply="` + replyID +
		`" title="選んだ返事を Gemini に読ませ、「` + rfqReplySourceCaption + `」の表を読んだもので置き換えます（値は書かれているまま）">🤖 読む</button> <span class="rfq-reply-ocr-say"></span></p>`)
	return b.String()
}
