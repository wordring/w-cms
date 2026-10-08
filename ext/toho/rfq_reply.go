package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積依頼の返事を見積依頼書ページへ（2026-10-05・段4の前半）
//
// 【要求】見積依頼 §4 の未決4「返事の取り込み」——利用者の答え（2026-10-05）: 返事は「見積依頼書に手書きで FAX」
// 「業者の見積書 PDF をメール」「メールの本文に値段」の3通り。作るのは「回答を記録」と「🤖 返事を読む」の**両方**。
// 返事の単価の置き場は**見積依頼明細の単価の列**（10-02 に決まっている——§2）。
//
//	POST /api/rfq/answered      … ✓ 回答を記録: 単価の入った未回答の行を「回答あり」に・回答日に今日（手で書いたあと押す）
//	GET  /api/rfq/reply-sources … 🤖 で読む返事の候補——このページに置いたファイル（作った見積依頼書の PDF は除く）と、
//	                              題に「№ <このページ>」を含む受信メール（本文とその添付）
//	POST /api/rfq/read-reply    … 🤖 返事を読む: 選んだ返事を Gemini に読ませ、行ごとの単価の**案**を返す（何も書かない）
//	POST /api/rfq/apply-reply   … 人が確かめた案を書く（単価・状態〔回答あり／辞退〕・備考・回答日）
//
// ⚠ **Gemini は読むだけ**——どの行の値段かを対応づけて写すだけで、計算はさせない（合計しか無ければ単価は空にして備考へ）。
// ⚠ **書くのは人が確かめてから**（PDF 解析と同じ人間ゲート——Gemini は人が押した直後だけ）。行は「何の値段か」の文で
// 突き合わせ、読んだあとにページが変わっていたら書かない。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"golang.org/x/net/html"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// rfqReadReplyAI は返事を読む口です（試験が偽物へ差し替えられるよう変数にしてある——本物は Gemini へ送る）。
var rfqReadReplyAI = cms.GeminiGenerateBlobs

// rfqReplyMaxBytes は 🤖 に渡す返事のファイルの上限です（図面の見比べと同じ・Gemini の1回の要求に収まる大きさ）。
const rfqReplyMaxBytes = 18 << 20

// rfqReplyWhatLabels は「何の値段か」の文に並べる列です（見積依頼明細の列の名前）。
var rfqReplyWhatLabels = []string{"種類", "品番", "品名", "加工内容", "材質", "形状", "寸法", "表面", "仕様"}

// rfqTableView は見積依頼書ページの本文の、見積依頼明細の表を読んだものです（書き換えは nodes を描き直す）。
type rfqTableView struct {
	nodes []*html.Node
	col   map[string]int
	rows  []*html.Node // 中身のある行（見出しの行・tfoot を除く）——読む・書くの両方がこの順で数える
}

func readRFQTable(body string) (*rfqTableView, error) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil, errors.New("本文を読めません")
	}
	tables := tablesOfType(nodes, RFQItemsType)
	if len(tables) == 0 {
		return nil, errors.New("見積依頼明細の表がありません")
	}
	trs := rowsOf(tables[0])
	if len(trs) == 0 {
		return nil, errors.New("見積依頼明細に見出しの行がありません")
	}
	v := &rfqTableView{nodes: nodes, col: headerIndex(trs[0])}
	for _, tr := range trs[1:] {
		if tr.Parent != nil && tr.Parent.Data == "tfoot" {
			continue
		}
		for _, c := range cellsOf(tr) {
			if strings.TrimSpace(textOf(c)) != "" {
				v.rows = append(v.rows, tr)
				break
			}
		}
	}
	return v, nil
}

func (v *rfqTableView) cell(tr *html.Node, label string) *html.Node {
	i, ok := v.col[label]
	if !ok {
		return nil
	}
	cells := cellsOf(tr)
	if i >= len(cells) {
		return nil
	}
	return cells[i]
}

func (v *rfqTableView) text(tr *html.Node, label string) string {
	if c := v.cell(tr, label); c != nil {
		return strings.TrimSpace(textOf(c))
	}
	return ""
}

// what は行の「何の値段か」です（種類・品名・材質…と「×数量単位」）。
func (v *rfqTableView) what(tr *html.Node) string {
	var parts []string
	for _, l := range rfqReplyWhatLabels {
		if s := v.text(tr, l); s != "" {
			parts = append(parts, s)
		}
	}
	if q := v.text(tr, "数量"); q != "" {
		parts = append(parts, "×"+q+v.text(tr, "単位"))
	}
	return strings.Join(parts, " ")
}

// rfqReplyRow は画面へ渡す行です（1始まり）。
type rfqReplyRow struct {
	Row    int    `json:"row"`
	What   string `json:"what"`
	Price  string `json:"price"`
	Status string `json:"status"`
}

func (v *rfqTableView) replyRows() []rfqReplyRow {
	out := make([]rfqReplyRow, 0, len(v.rows))
	for i, tr := range v.rows {
		out = append(out, rfqReplyRow{Row: i + 1, What: v.what(tr), Price: v.text(tr, "単価"), Status: v.text(tr, "状態")})
	}
	return out
}

// today は回答日に書く今日の日付です。
func rfqToday() string { return time.Now().In(time.Local).Format("2006-01-02") }

// markRFQAnswered は、単価の入った未回答（か空）の行を「回答あり」にし、回答日に today を書いた本文を返します。
func markRFQAnswered(body, today string) (string, int, error) {
	v, err := readRFQTable(body)
	if err != nil {
		return body, 0, err
	}
	n := 0
	for _, tr := range v.rows {
		if v.text(tr, "単価") == "" {
			continue
		}
		st := v.cell(tr, "状態")
		if st == nil {
			return body, 0, errors.New("見積依頼明細に「状態」の列がありません")
		}
		if s := strings.TrimSpace(textOf(st)); s != "" && s != rfqLineUnanswered {
			continue
		}
		setCellText(st, rfqLineAnswered)
		n++
	}
	if n == 0 {
		return body, 0, errors.New("単価の入った未回答の行がありません（単価を書いてから押してください）")
	}
	return withTagValues(htmldoc.Render(v.nodes), RFQAnsweredTag, []string{today}, true), n, nil
}

// rfqReplyLine は書く1行です（画面で人が確かめたもの）。What は読んだときの「何の値段か」——ずれていたら書かない。
type rfqReplyLine struct {
	Row      int    `json:"row"`
	What     string `json:"what"`
	Price    string `json:"price"`
	Declined bool   `json:"declined"`
	Note     string `json:"note"`
}

// cleanPrice は単価を数字の形へ寄せます（全角・カンマ・円・¥ を落とす）。数として読めなければ書いたまま。
func cleanPrice(s string) string {
	s = strings.TrimSpace(s)
	t := cms.NormalizeText(s)
	t = strings.NewReplacer(",", "", "円", "", "¥", "", "￥", "", " ", "", "\\", "").Replace(t)
	if _, err := strconv.ParseFloat(t, 64); err == nil {
		return t
	}
	return s
}

// applyRFQReply は確かめた行を書いた本文を返します（1つでも行がずれていれば何も書かない）。
func applyRFQReply(body, today string, lines []rfqReplyLine) (string, int, error) {
	v, err := readRFQTable(body)
	if err != nil {
		return body, 0, err
	}
	for _, need := range []string{"単価", "状態"} {
		if _, ok := v.col[need]; !ok {
			return body, 0, errors.New("見積依頼明細に「" + need + "」の列がありません")
		}
	}
	for _, l := range lines {
		if l.Row < 1 || l.Row > len(v.rows) {
			return body, 0, errors.New(strconv.Itoa(l.Row) + "行目はありません（ページが変わったかもしれません——読み直してください）")
		}
		if got := v.what(v.rows[l.Row-1]); got != l.What {
			return body, 0, errors.New(strconv.Itoa(l.Row) + "行目が読んだときと違います（いま「" + got + "」——ページが変わったかもしれません。読み直してください）")
		}
	}
	n := 0
	for _, l := range lines {
		tr := v.rows[l.Row-1]
		price, note := cleanPrice(l.Price), strings.TrimSpace(l.Note)
		switch {
		case l.Declined:
			setCellText(v.cell(tr, "状態"), rfqLineDeclined)
			if price != "" {
				setCellText(v.cell(tr, "単価"), price)
			}
		case price != "":
			setCellText(v.cell(tr, "単価"), price)
			setCellText(v.cell(tr, "状態"), rfqLineAnswered)
		case note == "":
			continue
		}
		if note != "" {
			c := v.cell(tr, "備考")
			if c == nil {
				return body, 0, errors.New("見積依頼明細に「備考」の列がありません")
			}
			if cur := strings.TrimSpace(textOf(c)); cur != "" && !strings.Contains(cur, note) {
				note = cur + "／" + note
			} else if cur != "" {
				note = cur
			}
			setCellText(c, note)
		}
		n++
	}
	if n == 0 {
		return body, 0, errors.New("書く行がありません（単価・辞退・備考のどれかが要ります）")
	}
	return withTagValues(htmldoc.Render(v.nodes), RFQAnsweredTag, []string{today}, true), n, nil
}

// gateRFQReply は口の入口です——書ける見積依頼書ページか（テンプレートの外・編集中でない）。
func gateRFQReply(w http.ResponseWriter, r *http.Request, raw string) (string, bool) {
	pageID, ok := gateWritablePage(w, r, raw)
	if !ok {
		return "", false
	}
	if !isRFQPage(pageID) {
		cms.JSONFail(w, http.StatusBadRequest, "見積依頼書ページではありません")
		return "", false
	}
	return pageID, true
}

// RFQAnsweredAPIHandler は POST /api/rfq/answered です（入力: {page_id}）。
func RFQAnsweredAPIHandler(w http.ResponseWriter, r *http.Request) {
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
	pageID, ok := gateRFQReply(w, r, req.PageID)
	if !ok {
		return
	}
	writeRFQRewrite(w, user, pageID, "rfq.answered", func(cur string) (string, int, error) { return markRFQAnswered(cur, rfqToday()) })
}

// RFQApplyReplyAPIHandler は POST /api/rfq/apply-reply です（入力: {page_id, lines:[{row, what, price, declined, note}]}）。
func RFQApplyReplyAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string         `json:"page_id"`
		Lines  []rfqReplyLine `json:"lines"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, ok := gateRFQReply(w, r, req.PageID)
	if !ok {
		return
	}
	writeRFQRewrite(w, user, pageID, "rfq.reply", func(cur string) (string, int, error) { return applyRFQReply(cur, rfqToday(), req.Lines) })
}

// writeRFQRewrite は、先にいまの本文で確かめてから書きます（断るときに版を作らない——RewriteBody はいつも版を残す）。
func writeRFQRewrite(w http.ResponseWriter, user *auth.User, pageID, action string, fn func(string) (string, int, error)) {
	cur, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "ページを読めません: "+err.Error())
		return
	}
	if _, _, err := fn(cur); err != nil {
		cms.JSONFail(w, http.StatusConflict, err.Error())
		return
	}
	changed := 0
	var why error
	if !rewriteBodyOrFail(w, pageID, user.Username, func(now string) string {
		out, n, err := fn(now)
		changed, why = n, err
		return out
	}) {
		return
	}
	if why != nil {
		cms.JSONFail(w, http.StatusConflict, why.Error())
		return
	}
	auth.Audit(user.Username, action, pageID+" "+strconv.Itoa(changed)+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID, "changed": changed})
}

// rfqReplySource は 🤖 で読む返事の候補です。File が空ならメールの本文。
type rfqReplySource struct {
	Label  string `json:"label"`
	PageID string `json:"page_id"`
	File   string `json:"file"`
}

// rfqReadableExt は 🤖 に渡せる返事のファイルの種類と MIME です。
var rfqReadableExt = map[string]string{
	".pdf": "application/pdf", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp",
}

// pageFileSources は、ページの添付のうち 🤖 に渡せるものを新しい順に返します（skip が真のものは除く）。
func pageFileSources(pageID, prefix string, skip func(name string) bool) []rfqReplySource {
	type f struct{ stored, name, at string }
	var fs []f
	for stored, m := range cms.ReadAttachmentMetas(pageID) {
		if _, ok := rfqReadableExt[strings.ToLower(filepath.Ext(stored))]; !ok {
			continue
		}
		name := m.Name
		if name == "" {
			name = stored
		}
		if skip != nil && skip(name) {
			continue
		}
		fs = append(fs, f{stored, name, m.SavedAt})
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].at > fs[j].at })
	out := make([]rfqReplySource, 0, len(fs))
	for _, x := range fs {
		out = append(out, rfqReplySource{Label: prefix + x.name, PageID: pageID, File: x.stored})
	}
	return out
}

// RFQReplySourcesAPIHandler は GET /api/rfq/reply-sources?page_id= です。
func RFQReplySourcesAPIHandler(w http.ResponseWriter, r *http.Request) {
	pageID, _, user, ok := cms.GateJSONPageRead(w, r, r.URL.Query().Get("page_id"))
	if !ok {
		return
	}
	if !isRFQPage(pageID) {
		cms.JSONFail(w, http.StatusBadRequest, "見積依頼書ページではありません")
		return
	}
	// 0. この見積依頼書に結んだ返事ページ（通信記録の 🤖 解析から・2026-10-05・rfq_reply_page.go）の原本——いちばん上に。
	out := rfqReplyPageSources(user, pageID)
	// 1. このページに置いたファイル（FAX の取り込み・写真・業者の見積書）——作った見積依頼書の PDF は除く。
	out = append(out, pageFileSources(pageID, "このページ: ", func(name string) bool { return strings.HasPrefix(name, "見積依頼書 ") })...)
	// 2. 題に「№ <このページ>」を含む受信メール（見積依頼のメールの件名「見積依頼（№ …）」への返信）——新しい順。
	rows, err := database.DB.Query(`SELECT id, title FROM pages WHERE title LIKE ? ORDER BY id DESC LIMIT 20`, "%№ "+pageID+"%")
	if err == nil {
		type m struct {
			id    int
			title string
		}
		var mails []m
		for rows.Next() {
			var x m
			if rows.Scan(&x.id, &x.title) == nil {
				mails = append(mails, x)
			}
		}
		rows.Close()
		for _, x := range mails {
			if !page.CanView(user, x.id) || cms.PageTagValue(database.DB, x.id, comm.DirectionTag) != comm.DirectionIn {
				continue
			}
			mid := page.FormatID(x.id)
			out = append(out, rfqReplySource{Label: "メール: " + x.title + "（本文）", PageID: mid})
			out = append(out, pageFileSources(mid, "メール: "+x.title+" の ", nil)...)
		}
	}
	cms.WriteJSON(w, map[string]any{"success": true, "sources": out})
}

// rfqReplyPrompt は返事を読ませる指示です（%ROWS% に行の一覧・%TEXT% にメールの本文）。
const rfqReplyPrompt = `あなたは、弊社が業者へ出した見積依頼への**業者の返事**を読む係です。返事は添付のファイル（弊社の見積依頼書に手書きで書き込んだFAX、業者の見積書、または**図面に数量と単価を書き込んで返してきたもの**——「50ヶ @ 2,900」なら50個のときの1個 2,900円）か、下のメールの本文です。図面への書き込みは、図面の表題欄の図面番号・図面名称で行に対応づけてください。
弊社が聞いた品物は次の行です（行番号: 何の値段か）:
%ROWS%
返事に書かれた単価を、どの行のものか対応づけて、次の JSON だけで答えてください（前後に文を付けない）:
{"rows":[{"row":1,"unit_price":"1200","declined":false,"note":""}],"summary":"返事の要点を1〜2文で"}
- unit_price は返事に書かれた1個（1単位）あたりの値段を、数字だけで書いてください（カンマ・円・税の記号は除く）。書かれていなければ空文字。
- ⚠ 計算はしないでください。合計・総額しか書かれていない行は unit_price を空にし、書かれた金額を note に写してください。
- 「できません」「辞退」など断られた行は declined を true にしてください。
- 数量やロットの条件、納期、材質の変更など、単価以外の大事な書き込みは note に短く書いてください。
- どの行か分からない値段は rows に入れず、summary に書いてください。返事に出てこない行は rows に入れなくてかまいません。
%TEXT%`

// rfqReplyGuess は 🤖 が返した1行です。
type rfqReplyGuess struct {
	Row       int    `json:"row"`
	UnitPrice string `json:"unit_price"`
	Declined  bool   `json:"declined"`
	Note      string `json:"note"`
}

// parseRFQReply は 🤖 の返答を読みます（Gemini を呼ばずに試せるよう切り出してある）。行の番号が範囲の外のものは捨てる。
func parseRFQReply(text string, nRows int) ([]rfqReplyGuess, string, error) {
	var v struct {
		Rows    []rfqReplyGuess `json:"rows"`
		Summary string          `json:"summary"`
	}
	if err := json.Unmarshal([]byte(cms.StripJSONFence(text)), &v); err != nil {
		head := []rune(strings.TrimSpace(cms.StripJSONFence(text)))
		if len(head) > 200 {
			head = append(head[:200], '…')
		}
		return nil, "", errors.New("読んだ結果を読めません: " + err.Error() + "（返ってきたもの: " + string(head) + "）")
	}
	out := []rfqReplyGuess{}
	seen := map[int]bool{}
	for _, g := range v.Rows {
		if g.Row < 1 || g.Row > nRows || seen[g.Row] {
			continue
		}
		seen[g.Row] = true
		g.UnitPrice = cleanPrice(g.UnitPrice)
		g.Note = strings.TrimSpace(g.Note)
		out = append(out, g)
	}
	return out, strings.TrimSpace(v.Summary), nil
}

// RFQReadReplyAPIHandler は POST /api/rfq/read-reply です（入力: {page_id, source_page, source_file}）。
// 応答は {success, rows（いまの行）, guesses（🤖 の案）, summary}——**何も書きません**。
func RFQReadReplyAPIHandler(w http.ResponseWriter, r *http.Request) {
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
	pageID, ok := gateRFQReply(w, r, req.PageID)
	if !ok {
		return
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "ページを読めません: "+err.Error())
		return
	}
	v, err := readRFQTable(body)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}
	rows := v.replyRows()
	if len(rows) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "見積依頼明細に行がありません")
		return
	}
	srcID, ok := page.NormalizeID(strings.TrimSpace(req.SourcePage))
	srcInt, _ := strconv.Atoi(srcID)
	if !ok || !page.CanView(user, srcInt) {
		cms.JSONFail(w, http.StatusNotFound, "返事が見つかりません")
		return
	}
	var blobs []genai.Blob
	text := ""
	if f := strings.TrimSpace(req.SourceFile); f != "" {
		mime, okExt := rfqReadableExt[strings.ToLower(filepath.Ext(f))]
		stored, _, okRef := cms.AttachmentOfRef(user, cms.FileRef{PageID: srcID, ID: strings.TrimSuffix(f, filepath.Ext(f))})
		path, okPath := page.AttachmentPath(srcID, stored)
		if !okExt || !okRef || !okPath {
			cms.JSONFail(w, http.StatusNotFound, "返事のファイルが見つかりません（PDF・画像だけ読めます）")
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			cms.JSONFail(w, http.StatusNotFound, "返事のファイルを読めません")
			return
		}
		if len(data) > rfqReplyMaxBytes {
			cms.JSONFail(w, http.StatusRequestEntityTooLarge, "返事のファイルが大きすぎます（18MBまで）")
			return
		}
		blobs = append(blobs, genai.Blob{MIMEType: mime, Data: data})
	} else {
		srcBody, err := cms.ReadPageBody(srcID)
		if err != nil {
			cms.JSONFail(w, http.StatusNotFound, "返事のメールを読めません")
			return
		}
		mail := strings.TrimSpace(comm.RecordBodyText(srcBody))
		if mail == "" {
			cms.JSONFail(w, http.StatusBadRequest, "メールの本文が空です（添付のファイルを選んでください）")
			return
		}
		text = "返事のメールの本文:\n" + mail
	}
	var list strings.Builder
	for _, row := range rows {
		list.WriteString(strconv.Itoa(row.Row) + ": " + row.What + "\n")
	}
	prompt := strings.NewReplacer("%ROWS%", list.String(), "%TEXT%", text).Replace(rfqReplyPrompt)
	resp, err := rfqReadReplyAI(prompt, blobs...)
	if err != nil {
		if errors.Is(err, cms.ErrNoGeminiKey) {
			cms.JSONFail(w, http.StatusServiceUnavailable, "サーバーに GEMINI_API_KEY 環境変数が設定されていません。設定してから起動し直してください。")
			return
		}
		cms.JSONFail(w, http.StatusBadGateway, "読めませんでした: "+err.Error())
		return
	}
	guesses, summary, err := parseRFQReply(resp, len(rows))
	if err != nil {
		cms.JSONFail(w, http.StatusBadGateway, err.Error())
		return
	}
	auth.Audit(user.Username, "rfq.read-reply", pageID+" ← "+srcID+"/"+req.SourceFile)
	cms.WriteJSON(w, map[string]any{"success": true, "rows": rows, "guesses": guesses, "summary": summary})
}
