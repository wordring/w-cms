package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積依頼の返事ページ——通信記録の 🤖 解析から（2026-10-05）
//
// 利用者:「見積依頼の返事は通信記録に入るので、解析ボタンを押して解析し、見積依頼の返事とわかれば、見積依頼の返事ページを
// 作り、GeminiによってOCRして、どの見積依頼の返事か突き合わせる形になると思います」。問いへの答え（2026-10-05）:
//   - 突き合わせは「**候補が1つなら自動で結ぶ**」（2つ以上・0なら人が選ぶ）
//   - 見積依頼明細の単価へは「**案の表で確かめてから**」写す（rfq_reply.go の 🤖 返事を読む・案の表・書き込む）
//   - 返事ページは突き合わせたら「**見積依頼書ページの子へ移す**」
//
// 流れ:
//
//	通信記録（メール）── 🤖 解析（添付の PDF）／作るページ「見積依頼の返事ページ」（メールの本文）
//	  └ 見積依頼の返事ページ（テンプレート「見積依頼の返事」）——仕入先・回答日・読んだ見積依頼番号・受信元・原本・読んだままの表
//	     ↓ 候補（①紙に読めた№ ②メールの件名の№ ③こちらが送った見積依頼のメールへの返信のつながり ④同じ仕入先の回答待ち）
//	     ↓ 1つなら結ぶ（タグ `見積依頼` に見積依頼書のページ番号・見積依頼書の子へ移す）——0・2つ以上なら返事ページで人が選ぶ
//	見積依頼書ページ ── 「🤖 返事を読む」の候補のいちばん上に返事ページの原本 → 案の表 → 書き込む
//
// ⚠ ①②③（番号とメールのつながり）は強い手掛かり、④（仕入先）は弱い手掛かり——①②③で候補があれば④は足さない。
// ⚠ 自動で結ぶのは候補がちょうど1つで、**仕入先が食い違わない**ときだけ（紙の№の読み違いで、別の業者の見積依頼に
// 結ばないため——食い違えば候補として出して人が選ぶ）。
// ⚠ 返事ページは `見積依頼番号` のタグを持たない（持つと見積依頼書ページと見分けられない——isRFQPage）。読んだ番号は
// `読んだ見積依頼番号`、結んだ先は `見積依頼`。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"errors"
	stdhtml "html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

const (
	// RFQReplyTemplate は見積依頼の返事ページを作るテンプレートの題です。
	RFQReplyTemplate = "見積依頼の返事"
	// RFQReplyLinkTag は返事ページが結ばれた見積依頼書（ページ番号）です。
	RFQReplyLinkTag = "見積依頼"
	// RFQReplyReadNoTag は返事から読めた見積依頼番号（紙の「№」）です——読めたままの値。
	RFQReplyReadNoTag = "読んだ見積依頼番号"
	// rfqReplySourceCaption は業者の返事を読んだままの表を入れる枠の題です。
	rfqReplySourceCaption = "業者の返事（読んだまま）"
	// RFQReplyMatchViewType は返事ページの「見積依頼書との突き合わせ」の欄です。
	RFQReplyMatchViewType = "rfq-reply-match"
	// RFQReplyMailKind はメールの「作るページ」の種類です。
	RFQReplyMailKind = "見積依頼の返事ページ"
	// rfqReplyKind は解析済みの印などに出す種類の名前です。
	rfqReplyKind = "返事"
)

func init() {
	cms.RegisterPageTemplate(cms.PageTemplate{
		Title:     RFQReplyTemplate,
		Extension: "toho",
		Why: "通信記録の 🤖 解析が業者の見積依頼の返事と判定したとき（とメールの「作るページ」）に写します。タグ 仕入先・回答日・" +
			"読んだ見積依頼番号・見積依頼・受信元、原本のファイル表示、「業者の返事（読んだまま）」の枠、「見積依頼の返事の突き合わせ」の欄は、あれば埋めます。",
	})
	cms.RegisterVocab(cms.VocabDef{
		Type: RFQReplyMatchViewType, DisplayName: "見積依頼の返事の突き合わせ", Category: "ビュー", Icon: "🔗", Element: "section",
		View: true,
	})
	cms.RegisterView(RFQReplyMatchViewType, rfqReplyMatchViewHTML)
	comm.RegisterRecordMaker(comm.RecordMaker{
		Name: RFQReplyMailKind, Order: 30, Directions: []string{"受信"},
		Hint: "🤖 本文を読んで見積依頼の返事ページを作ります（業者がメールの本文に単価を書いてきた返事）",
		Make: makeRFQReplyFromMail,
		Made: func(user *auth.User, pageID string) []comm.MadePage {
			var out []comm.MadePage
			for _, id := range rfqReplyPagesFrom(user, pageID) {
				out = append(out, comm.MadePage{PageID: id, Title: pageTitleOf(id), Kind: rfqReplyKind})
			}
			return out
		},
	})
}

// rfqReplyJudgment は返事から読んだもの（解析の判定 `rfq_reply` と、メールの本文の読みが同じ形）です。
type rfqReplyJudgment struct {
	RFQNo          string           `json:"rfq_no"`
	Supplier       string           `json:"supplier"`
	Date           string           `json:"date"`
	SourceTableRaw json.RawMessage  `json:"source_table"`
	SourceTable    orderSourceTable `json:"-"`
}

// isRFQReplyTags は返事ページのタグか——`回答日` と `受信元` を持ち、`見積依頼番号` を持たない（見積依頼書ページは持つ）。
func isRFQReplyTags(tags map[string][]string) bool {
	return cms.FirstTag(tags, RFQAnsweredTag) != "" && cms.FirstTag(tags, SourceRefTag) != "" && cms.FirstTag(tags, RFQNoTag) == ""
}

// rfqReplyPagesFrom は、記録 pageID（メールのページ）から作った返事ページを返します（読めるものだけ）。
func rfqReplyPagesFrom(user *auth.User, pageID string) []string {
	rows, err := database.DB.Query(`SELECT page_id FROM page_tags WHERE name = ? AND (value = ? OR value LIKE ?)`, SourceRefTag, pageID, pageID+"-%")
	if err != nil {
		return nil
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	var out []string
	for _, id := range ids {
		if !page.CanView(user, id) {
			continue
		}
		if tags, err := cms.TagsOfPage(database.DB, id); err == nil && isRFQReplyTags(tags) {
			out = append(out, page.FormatID(id))
		}
	}
	return out
}

// mailDateOf は記録の受信日時の日付（YYYY-MM-DD）です（読めなければ空）。
func mailDateOf(pageIDInt int) string {
	tags, _ := cms.TagsOfPage(database.DB, pageIDInt)
	if n, ok := cms.NormalizeValue(cms.ColDate, cms.FirstTag(tags, comm.ReceivedAtTag)); ok {
		return n
	}
	if v := cms.FirstTag(tags, comm.ReceivedAtTag); len(v) >= 10 {
		return v[:10]
	}
	return ""
}

// buildRFQReplyPageHTML は返事ページの本文を、テンプレート tmpl を埋めて組みます。attachID が空ならメールの本文から。
func buildRFQReplyPageHTML(tmpl, hostPageID, attachID, supplier, date string, r *rfqReplyJudgment) (string, error) {
	d := cms.NewPageDraft(RFQReplyTemplate, tmpl)
	title := "見積依頼の返事"
	if supplier != "" {
		title += "　" + supplier
	}
	d.SetTitle(title)
	setHeaderTag(d.DraftBlock, SupplierTag, supplier)
	setHeaderTag(d.DraftBlock, RFQAnsweredTag, date)
	setHeaderTag(d.DraftBlock, RFQReplyReadNoTag, cms.NormalizeNameForIngest(r.RFQNo))
	ref := hostPageID
	if attachID != "" {
		ref = hostPageID + "-" + attachID
		d.SetFileView(ref)
	} else {
		d.DropFileView()
	}
	setHeaderTag(d.DraftBlock, SourceRefTag, ref)
	if box, ok := d.Container(rfqReplySourceCaption); ok {
		if tbl := sourceTableHTML(r.SourceTable); tbl != "" {
			box.SetContent(tbl)
		} else {
			d.Remove(box.Node())
		}
	}
	return d.HTML(), nil
}

// rfqNoRe は件名・紙の「№ 001864」を拾います（全角の数字も）。
var rfqNoRe = regexp.MustCompile(`№\s*([0-9０-９]{1,6})`)

// rfqPageOf は番号 raw（全角・前後の文字を許す）が読める見積依頼書ページなら、その6桁のページ番号を返します。
func rfqPageOf(user *auth.User, raw string) (string, bool) {
	var digits strings.Builder
	for _, c := range cms.NormalizeText(raw) {
		if c >= '0' && c <= '9' {
			digits.WriteRune(c)
		}
	}
	if digits.Len() == 0 || digits.Len() > 6 {
		return "", false
	}
	id, ok := page.NormalizeID(digits.String())
	n, _ := strconv.Atoi(id)
	if !ok || !isRFQPage(id) || !page.CanView(user, n) {
		return "", false
	}
	return id, true
}

// sameCompany は2つの社名が同じ会社か（法人格を落とし、文字を畳んで比べる）。
func sameCompany(a, b string) bool {
	return cms.NormalizeText(cms.FoldCompanyName(a)) == cms.NormalizeText(cms.FoldCompanyName(b))
}

// rfqReplyCandidates は返事の候補の見積依頼書ページを返します（強い手掛かりがあればそれだけ・無ければ同じ仕入先の回答待ち）。
func rfqReplyCandidates(user *auth.User, hostPageID, readNo, supplier string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(id string, ok bool) {
		if ok && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	// ① 紙に読めた見積依頼番号。
	if strings.TrimSpace(readNo) != "" {
		add(rfqPageOf(user, readNo))
	}
	if hostInt, err := strconv.Atoi(hostPageID); err == nil {
		// ② メールの件名の「№」（見積依頼のメールの件名「見積依頼（№ …）」への返信）。
		for _, m := range rfqNoRe.FindAllStringSubmatch(cms.PageTitleByID(hostInt), -1) {
			add(rfqPageOf(user, m[1]))
		}
		// ③ こちらが送った見積依頼のメール（返信元メッセージID の先の控え）の件名の「№」。
		tags, _ := cms.TagsOfPage(database.DB, hostInt)
		if irt := strings.TrimSpace(cms.FirstTag(tags, comm.InReplyToTag)); irt != "" {
			var sent int
			if database.DB.QueryRow(`SELECT page_id FROM page_tags WHERE name = ? AND value = ? LIMIT 1`, comm.MessageIDTag, irt).Scan(&sent) == nil {
				for _, m := range rfqNoRe.FindAllStringSubmatch(cms.PageTitleByID(sent), -1) {
					add(rfqPageOf(user, m[1]))
				}
			}
		}
	}
	if len(out) > 0 || strings.TrimSpace(supplier) == "" {
		return out
	}
	// ④ 同じ仕入先で回答待ち（回答日の無い）見積依頼書——新しい順。
	rows, err := cms.TagRowsNamed(database.DB, SupplierTag)
	if err != nil {
		return out
	}
	var ids []int
	for _, t := range rows {
		if sameCompany(t.Value, supplier) {
			ids = append(ids, t.PageID)
		}
	}
	for i := len(ids) - 1; i >= 0; i-- {
		id := page.FormatID(ids[i])
		if isRFQPage(id) && cms.PageTagValue(database.DB, ids[i], RFQAnsweredTag) == "" && page.CanView(user, ids[i]) {
			add(id, true)
		}
	}
	return out
}

// linkRFQReply は返事ページを見積依頼書に結びます——タグ `見積依頼` に見積依頼書のページ番号を書き、見積依頼書の子へ移す。
func linkRFQReply(user *auth.User, replyID, rfqID string) error {
	if err := cms.RewriteBody(replyID, user.Username, func(b string) string {
		return withTagValues(b, RFQReplyLinkTag, []string{rfqID}, true)
	}); err != nil {
		return err
	}
	if _, _, err := cms.SetPageParent(user, replyID, rfqID); err != nil {
		return err
	}
	auth.Audit(user.Username, "rfq-reply.link", replyID+" → "+rfqID)
	return nil
}

// rfqReplyMade は返事ページを作った結果です。
type rfqReplyMade struct {
	PageID     string
	Linked     string   // 自動で結んだ見積依頼書（空なら結んでいない）
	Candidates []string // 結ばなかったときの候補
}

// makeRFQReply は返事ページを記録 hostPageID の子に作り、候補が1つ（仕入先も食い違わない）なら結びます。
func makeRFQReply(user *auth.User, hostPageID, attachID string, r *rfqReplyJudgment) (rfqReplyMade, error) {
	tmpl, err := cms.PageTemplateBody(RFQReplyTemplate)
	if err != nil {
		return rfqReplyMade{}, &comm.MakeError{Status: http.StatusConflict,
			Message: "見積依頼の返事と判定しましたが、ページを作るテンプレートがありません: " + err.Error()}
	}
	hostInt, _ := strconv.Atoi(hostPageID)
	supplier := contacts.OrgNameForPage(user, r.Supplier)
	if strings.TrimSpace(supplier) == "" {
		if addr := recordSenderAddress(hostInt); addr != "" {
			supplier, _ = contacts.PartnerTitleForAddress(user, addr)
		}
	}
	date := r.Date
	if n, ok := cms.NormalizeValue(cms.ColDate, date); ok {
		date = n
	} else if date = mailDateOf(hostInt); date == "" {
		date = time.Now().In(time.Local).Format("2006-01-02")
	}
	body, err := buildRFQReplyPageHTML(tmpl, hostPageID, attachID, supplier, date, r)
	if err != nil {
		return rfqReplyMade{}, err
	}
	newID, err := cms.CreateChildPage(hostPageID, user.Username, body)
	if err != nil {
		return rfqReplyMade{}, err
	}
	auth.Audit(user.Username, "analyze-rfq-reply", newID+" from "+hostPageID+"/"+attachID)
	made := rfqReplyMade{PageID: newID}
	cands := rfqReplyCandidates(user, hostPageID, r.RFQNo, supplier)
	if len(cands) == 1 && (supplier == "" || sameCompany(supplier, cms.PageTagValue(database.DB, pageNum(cands[0]), SupplierTag))) {
		if err := linkRFQReply(user, newID, cands[0]); err == nil {
			made.Linked = cands[0]
			return made, nil
		}
	}
	made.Candidates = cands
	return made, nil
}

// rfqReplySay は作った結果の一言です。
func rfqReplySay(m rfqReplyMade) string {
	if m.Linked != "" {
		return "見積依頼書 /" + m.Linked + " への返事として結び、その下へ移しました。見積依頼書の「🤖 返事を読む」で単価を確かめて写してください。"
	}
	if len(m.Candidates) > 1 {
		return "どの見積依頼書への返事か、候補が " + strconv.Itoa(len(m.Candidates)) + " つあります——返事ページで選んでください。"
	}
	return "どの見積依頼書への返事か分かりませんでした——返事ページで見積依頼番号を入れて結んでください。"
}

// ── メールの本文から（作るページ「見積依頼の返事ページ」）──────────────────────────────

// rfqReplyMailPrompt はメールの本文を返事として読ませる指示です。
const rfqReplyMailPrompt = `（ここで渡すのはメールの件名・差出人・日付・本文です。）このメールが、当社が業者へ出した見積依頼への**業者の返事**（単価・見積を答えてきたもの）かを判定し、返事なら中身を読んでください。
次の形式のJSONオブジェクトのみを出力してください（マークダウンのコードブロック修飾は付けない）:
{"is_rfq_reply": 返事なら true・それ以外は false,
 "rfq_no": "当社の見積依頼書の番号（「№」の後ろの数字）が書かれていればそのまま（無ければ空文字）",
 "supplier": "返事をくれた業者の会社名（本文・署名から。無ければ空文字）",
 "date": "返事の日付を YYYY-MM-DD で（本文に無ければメールの日付）",
 "source_table": {"headers": ["表の見出し（本文の言葉で）"], "rows": [["1行ぶんの値を書かれているまま"]]}}
- source_table には、返事に書かれた品物・寸法・数量・単価・条件などを表にして入れてください。値は書かれているまま——計算はしないでください。
- 「>」で始まる行は前のやり取り（当社の見積依頼）の引用です。返事の値段ではないので表に入れないでください。`

// rfqReplyMailJudgment はメールの本文の読みです。
type rfqReplyMailJudgment struct {
	IsRFQReply bool `json:"is_rfq_reply"`
	rfqReplyJudgment
}

// judgeRFQReplyMail はメールの本文の読みの入口です（試験が偽物へ差し替えられるよう変数）。
var judgeRFQReplyMail = func(text string) (*rfqReplyMailJudgment, error) {
	resp, err := cms.GeminiGenerateBlobs(rfqReplyMailPrompt, genai.Blob{MIMEType: "text/plain", Data: []byte(text)})
	if err != nil {
		return nil, err
	}
	return parseRFQReplyMail(resp)
}

func parseRFQReplyMail(resp string) (*rfqReplyMailJudgment, error) {
	var j rfqReplyMailJudgment
	if err := json.Unmarshal([]byte(cms.StripJSONFence(resp)), &j); err != nil {
		head := []rune(strings.TrimSpace(cms.StripJSONFence(resp)))
		if len(head) > 300 {
			head = append(head[:300], '…')
		}
		return nil, errors.New("応答をJSONとして読めません: " + err.Error() + "（返ってきたもの: " + string(head) + "）")
	}
	j.SourceTable = parseSourceTable(j.SourceTableRaw)
	return &j, nil
}

// makeRFQReplyFromMail はメールの記録の本文を読んで返事ページを作ります（comm の口 POST /api/record-make から）。
func makeRFQReplyFromMail(user *auth.User, pageID string) (comm.MakeResult, error) {
	idInt, _ := strconv.Atoi(pageID)
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return comm.MakeResult{}, errors.New("本文を読めません: " + err.Error())
	}
	if strings.TrimSpace(comm.RecordBodyText(body)) == "" {
		return comm.MakeResult{}, &comm.MakeError{Status: http.StatusBadRequest, Message: "このメールには本文がありません（添付の 🤖 解析を使ってください）"}
	}
	if _, err := cms.PageTemplateBody(RFQReplyTemplate); err != nil {
		return comm.MakeResult{}, &comm.MakeError{Status: http.StatusConflict, Message: "見積依頼の返事ページのテンプレートがありません: " + err.Error()}
	}
	j, err := judgeRFQReplyMail(mailForJudgment(idInt, cms.PageTitleByID(idInt), body))
	if err != nil {
		if errors.Is(err, cms.ErrNoGeminiKey) {
			return comm.MakeResult{}, &comm.MakeError{Status: http.StatusServiceUnavailable,
				Message: "サーバーに GEMINI_API_KEY 環境変数が設定されていません。設定してから起動し直してください。"}
		}
		return comm.MakeResult{}, &comm.MakeError{Status: http.StatusBadGateway, Message: "読めませんでした: " + err.Error()}
	}
	if !j.IsRFQReply {
		return comm.MakeResult{Say: "見積依頼の返事ではないと判定されました（ページは作っていません）。"}, nil
	}
	m, err := makeRFQReply(user, pageID, "", &j.rfqReplyJudgment)
	if err != nil {
		return comm.MakeResult{}, err
	}
	return comm.MakeResult{Pages: []comm.MadePage{{PageID: m.PageID, Title: pageTitleOf(m.PageID), Kind: rfqReplyKind}}, Say: rfqReplySay(m)}, nil
}

// rfqReplyPageSources は、見積依頼書 rfqID に結んだ返事ページの原本を「🤖 返事を読む」の候補にします（新しい順）——
// 添付から作った返事は元のメールのその添付、メールの本文から作った返事はそのメールの本文。
func rfqReplyPageSources(user *auth.User, rfqID string) []rfqReplySource {
	rows, err := database.DB.Query(`SELECT page_id FROM page_tags WHERE name = ? AND value = ? ORDER BY page_id DESC`, RFQReplyLinkTag, rfqID)
	if err != nil {
		return nil
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	var out []rfqReplySource
	for _, id := range ids {
		tags, err := cms.TagsOfPage(database.DB, id)
		if err != nil || !isRFQReplyTags(tags) || !page.CanView(user, id) {
			continue
		}
		title := pageTitleOf(page.FormatID(id))
		ref := cms.FirstTag(tags, SourceRefTag)
		host, attach, hasAttach := strings.Cut(ref, "-")
		hostInt, err := strconv.Atoi(host)
		if err != nil || !page.CanView(user, hostInt) {
			continue
		}
		if !hasAttach {
			out = append(out, rfqReplySource{Label: "返事ページ: " + title + "（メールの本文）", PageID: page.FormatID(hostInt)})
			continue
		}
		if stored, name, ok := cms.AttachmentOfRef(user, cms.FileRef{PageID: page.FormatID(hostInt), ID: attach}); ok {
			out = append(out, rfqReplySource{Label: "返事ページ: " + title + " の " + name, PageID: page.FormatID(hostInt), File: stored})
		}
	}
	return out
}

// ── 返事ページの「見積依頼書との突き合わせ」の欄・結ぶ口 ────────────────────────────

// rfqReplyMatchViewHTML は返事ページの突き合わせの欄です——結んでいれば見積依頼書へのリンク、結んでいなければ候補と番号の欄。
func rfqReplyMatchViewHTML(user *auth.User, pageIDInt int) string {
	pid := page.FormatID(pageIDInt)
	if user == nil || cms.IsTemplateArea(pid) {
		return ""
	}
	tags, err := cms.TagsOfPage(database.DB, pageIDInt)
	if err != nil || !isRFQReplyTags(tags) {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<h3 class="materials-title">🔗 見積依頼書との突き合わせ</h3>`)
	rfqLabel := func(id string) string {
		n := pageNum(id)
		label := "/" + id + "　" + cms.PageTagValue(database.DB, n, SupplierTag)
		if d := cms.PageTagValue(database.DB, n, RFQDateTag); d != "" {
			label += "（見積依頼日 " + d + "）"
		}
		return label
	}
	if linked := strings.TrimSpace(cms.FirstTag(tags, RFQReplyLinkTag)); linked != "" {
		if n, err := strconv.Atoi(linked); err == nil && page.CanView(user, n) {
			b.WriteString(`<p class="unorder-help">✅ この返事は見積依頼書 <a href="/` + stdhtml.EscapeString(page.FormatID(n)) + `">` +
				stdhtml.EscapeString(rfqLabel(page.FormatID(n))) + `</a> への返事です。 <a class="chip-btn" href="/` + stdhtml.EscapeString(page.FormatID(n)) +
				`#rfq-reply">見積依頼書で単価を写す（🤖 返事を読む）</a></p>`)
			return b.String()
		}
	}
	if !canWritePage(user, pageIDInt) {
		b.WriteString(`<p class="unorder-help">まだどの見積依頼書にも結ばれていません。</p>`)
		return b.String()
	}
	host := cms.FirstTag(tags, SourceRefTag)
	if i := strings.Index(host, "-"); i > 0 {
		host = host[:i]
	}
	cands := rfqReplyCandidates(user, host, cms.FirstTag(tags, RFQReplyReadNoTag), cms.FirstTag(tags, SupplierTag))
	if len(cands) > 0 {
		b.WriteString(`<p class="unorder-help">どの見積依頼書への返事かを選んでください（選ぶと、その見積依頼書の下へ移します）:</p><ul class="rfq-reply-cands">`)
		for _, id := range cands {
			b.WriteString(`<li><button type="button" class="chip-btn rfq-reply-link" data-reply="` + pid + `" data-rfq="` + id + `">この見積依頼書への返事</button> ` +
				`<a href="/` + id + `">` + stdhtml.EscapeString(rfqLabel(id)) + `</a></li>`)
		}
		b.WriteString(`</ul>`)
	} else {
		b.WriteString(`<p class="unorder-help">どの見積依頼書への返事か分かりませんでした。</p>`)
	}
	b.WriteString(`<p class="unorder-help">見積依頼番号で結ぶ: <input type="text" class="rfq-reply-no" placeholder="001864"> ` +
		`<button type="button" class="chip-btn rfq-reply-link" data-reply="` + pid + `">結ぶ</button> <span class="rfq-reply-link-say"></span></p>`)
	// 見積依頼の記録が無い返事（移行期）——この返事から見積依頼書ページを作る（2026-10-05・rfq_reply_make.go）。
	b.WriteString(`<p class="unorder-help">見積依頼の記録が無い返事（移行期など）は: <button type="button" class="chip-btn rfq-reply-make" data-reply="` + pid +
		`" title="読んだままの表を見積依頼明細の列に読み替えた案を出します（弊社品番は図面番号・品番から当てます）。直してから作ります">` +
		`この返事から見積依頼書ページを作る</button></p><div class="rfq-reply-make-panel"></div>`)
	return b.String()
}

// RFQReplyLinkAPIHandler は POST /api/rfq-reply/link です（入力: {page_id（返事ページ）, rfq（見積依頼番号）}）。
func RFQReplyLinkAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		RFQ    string `json:"rfq"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	replyID, ok := gateWritablePage(w, r, req.PageID)
	if !ok {
		return
	}
	if tags, err := cms.TagsOfPage(database.DB, pageNum(replyID)); err != nil || !isRFQReplyTags(tags) {
		cms.JSONFail(w, http.StatusBadRequest, "見積依頼の返事ページではありません")
		return
	}
	rfqID, ok := rfqPageOf(user, req.RFQ)
	if !ok {
		cms.JSONFail(w, http.StatusNotFound, "見積依頼書が見つかりません（見積依頼番号 "+strings.TrimSpace(req.RFQ)+"）")
		return
	}
	if !canWritePage(user, pageNum(rfqID)) {
		cms.JSONFail(w, http.StatusForbidden, "その見積依頼書の下へ移せません（書く権限がありません）")
		return
	}
	if err := linkRFQReply(user, replyID, rfqID); err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "結べません: "+err.Error())
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "rfq": rfqID})
}
