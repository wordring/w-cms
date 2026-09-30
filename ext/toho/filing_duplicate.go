package toho

// ─────────────────────────────────────────────────────────────────────────
// 同じ図面が既にあるとき——知らせる・取り込まない・中身を比べる（2026-09-30）
//
// 利用者:「同じ図面が何度も来ることがあるのですが、図番が一致した場合、どうなりますか？」→「同じものがあるという
// ことを提示して欲しいのと、同じものがあるので取り込まない選択肢が欲しいです」「PDFの中身も比較してくれるなら、
// それに越したことは無いです」。
//
// 手掛かりは強い順に3つ:
//
//	同じファイル     … 図面ブロックが指す添付の中身（sha256）が同じ——送り直された同じPDF
//	同じ図面番号     … 版の印まで同じ番号。ファイルが違えば、図番を変えない改定か、PDFを作り直しただけか
//	🤖 中身を比べる  … ファイルが違うとき、Gemini に2つのPDFを見比べさせる（**押したときだけ**——Gemini は人の操作の直後だけ）
//
// ⚠ **決めるのは人**です（図面番号でページを自動的に束ねない）。「重複（取り込まない）」は整理の欄の選択肢の1つで、
// 画面が初期値にするのは「同じ番号で同じファイル」のときだけです。
//
// 取り込まないときは、解析で作った仮のページをごみ箱へ移し、**既にある加工製品の図面ブロックに `受信元` を1つ足します**
// ——このメールでも同じ図面が届いたことが加工製品に残り、メールの添付の「解析済み」の印（`受信元` の逆引き・
// analyzed.go）も既にある加工製品を指し続けます。足さないと印が消え、同じ添付をもう一度解析しかねません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/google/generative-ai-go/genai"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// drawingFacts は図面ブロック1つの手掛かりです。
type drawingFacts struct {
	No     string        // 図面番号（本文のまま）
	Source string        // 最初の受信元（`ページID-添付ID`）
	Refs   []cms.FileRef // 指している添付（ファイル表示・リンク・画像）
}

// factsOfBlock は図面ブロックから手掛かりを読みます。⚠ 本文を木にして読みます——エディタで保存した本文は
// タグの dt と dd のあいだに字下げが入るので、`<dt>図面番号</dt><dd>` の文字列では当たらないことがある。
func factsOfBlock(block string) drawingFacts {
	var f drawingFacts
	nodes, err := htmldoc.ParseFragment(block)
	if err != nil {
		return f
	}
	for _, n := range nodes {
		if f.No == "" {
			f.No = strings.TrimSpace(cms.TagValue(n, DrawingNoTag))
		}
		if f.Source == "" {
			f.Source = strings.TrimSpace(cms.TagValue(n, SourceRefTag))
		}
		f.Refs = append(f.Refs, cms.FileRefsIn(n)...)
	}
	return f
}

// exactDrawingKey は図面番号を「同じ番号か」で比べる形にします——**版の印は残します**（`sameDrawingKey` は外す。
// あちらは「同じ品物の候補」を探す物差し、こちらは「同じ図面か」の物差し）。
func exactDrawingKey(no string) string {
	return cms.NormalizeCode(strings.TrimSpace(cms.NormalizeNameForIngest(no)))
}

// fileHashCache は添付の sha256 の控えです（鍵は場所・大きさ・更新時刻）。整理の欄は打ち替えるたびに聞き直すので、
// 同じPDFを何度も読まないために持ちます。⚠ 添付は上書きできる（WebDAV・ローカル編集）ので、大きさか時刻が
// 変われば別の鍵になります。項目は見た添付の数だけ（1つ百数十バイト）で、捨てません。
var fileHashCache sync.Map

// fileHashOf は path の中身の sha256 を返します。
func fileHashOf(path string) (string, bool) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return "", false
	}
	key := path + "|" + strconv.FormatInt(st.Size(), 10) + "|" + strconv.FormatInt(st.ModTime().UnixNano(), 10)
	if v, ok := fileHashCache.Load(key); ok {
		return v.(string), true
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false
	}
	sum := hex.EncodeToString(h.Sum(nil))
	fileHashCache.Store(key, sum)
	return sum, true
}

// refFile は参照の指す添付の置き場です（読めない・無いなら ok=false）。
func refFile(user *auth.User, ref cms.FileRef) (path, name string, ok bool) {
	stored, name, ok := cms.AttachmentOfRef(user, ref)
	if !ok {
		return "", "", false
	}
	path, ok = page.AttachmentPath(ref.PageID, stored)
	return path, name, ok
}

// refHashes は参照の指す添付の sha256 の集合です（読めないものは入らない）。
func refHashes(user *auth.User, refs []cms.FileRef) map[string]bool {
	out := map[string]bool{}
	for _, ref := range refs {
		if path, _, ok := refFile(user, ref); ok {
			if sum, ok := fileHashOf(path); ok {
				out[sum] = true
			}
		}
	}
	return out
}

// drawingMatch は既にある加工製品の中で、届いた図面と同じと見える図面ブロックです。
type drawingMatch struct {
	Index    int  // 何番目の図面ブロックか（0から）
	SameNo   bool // 版の印まで同じ図面番号
	SameFile bool // 指している添付の中身が同じ
}

// matchDrawing は既にある加工製品の本文 body から、届いた図面 draft と同じと見える図面ブロックを探します。
// 同じ番号で同じファイルのものを第一に、次に同じ番号、次に同じファイル。無ければ ok=false。
func matchDrawing(user *auth.User, draft drawingFacts, draftHashes map[string]bool, body string) (drawingMatch, bool) {
	want := exactDrawingKey(draft.No)
	best, found := drawingMatch{}, false
	score := func(m drawingMatch) int {
		s := 0
		if m.SameNo {
			s += 2
		}
		if m.SameFile {
			s++
		}
		return s
	}
	for i, sec := range drawingSectionsOf(body) {
		f := factsOfBlock(sec)
		m := drawingMatch{Index: i, SameNo: want != "" && exactDrawingKey(f.No) == want}
		if len(draftHashes) > 0 {
			for sum := range refHashes(user, f.Refs) {
				if draftHashes[sum] {
					m.SameFile = true
					break
				}
			}
		}
		if score(m) > 0 && (!found || score(m) > score(best)) {
			best, found = m, true
		}
	}
	return best, found
}

// draftDrawing は整理を待つ仮のページ（解析が作った加工製品ページ）の最初の図面ブロックを読みます。
func draftDrawing(user *auth.User, draftID string) (drawingFacts, map[string]bool, bool) {
	idInt, err := strconv.Atoi(draftID)
	if err != nil || !page.CanView(user, idInt) {
		return drawingFacts{}, nil, false
	}
	body, err := cms.ReadPageBody(draftID)
	if err != nil {
		return drawingFacts{}, nil, false
	}
	block := drawingBlockOf(body)
	if strings.TrimSpace(block) == "" {
		return drawingFacts{}, nil, false
	}
	f := factsOfBlock(block)
	return f, refHashes(user, f.Refs), true
}

// markDuplicateCandidates は候補に「同じ図面番号」「同じファイル」の印を付けます（整理の欄が知らせに使う）。
func markDuplicateCandidates(user *auth.User, draftID string, cands []productCandidate) {
	draft, hashes, ok := draftDrawing(user, draftID)
	if !ok {
		return
	}
	for i := range cands {
		if cands[i].PageID == draftID {
			continue
		}
		body, err := cms.ReadPageBody(cands[i].PageID)
		if err != nil {
			continue
		}
		if m, ok := matchDrawing(user, draft, hashes, body); ok {
			cands[i].SameNo, cands[i].SameFile = m.SameNo, m.SameFile
		}
	}
}

// discardDuplicate は「重複（取り込まない）」です——仮のページをごみ箱へ移し、既にある加工製品の同じ図面に
// このメールの `受信元` を書き足します。
//
// ⚠ **同じ番号か同じファイルでなければ断ります**——画面はその2つのどちらかが在るときだけ選択肢を出すので、
// ここへ来るのは古い画面か打ち間違いです。何も共有しないページを「重複」として届いた図面を捨てると、
// 図面がどこにも残りません（ファイルはメールの添付に残るが、加工製品には辿り着けない）。
func discardDuplicate(user *auth.User, pageID, dupRaw string) filingResult {
	skip := func(msg string) filingResult {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: msg}
	}
	dupID, ok := page.NormalizeID(dupRaw)
	if !ok || dupID == pageID {
		return filingResult{PageID: pageID, Outcome: "needs_choice",
			Message: "重複の相手（既にある加工製品）が決まっていません。候補から選び直してください"}
	}
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !canWritePage(user, idInt) {
		return skip("このページを片付ける権限がありません")
	}
	dupInt, err := strconv.Atoi(dupID)
	if err != nil || !page.CanView(user, dupInt) {
		return skip("重複の相手 /" + dupID + " が見つかりません")
	}
	if !canWritePage(user, dupInt) {
		return skip("既にある加工製品 /" + dupID + " へ書き込む権限がありません（このメールで届いたことを書き足せない）")
	}
	for _, id := range []int{idInt, dupInt} {
		if holder, open := editlock.Locks.EditorOpen(id); open {
			return skip("/" + formatID(id) + " は編集中です（" + holder + "）。閉じてからもう一度お試しください")
		}
	}
	// 子ページを持つページはごみ箱へ移しません（一緒に消える）。解析が作ったページは子を持たないのが普通。
	if kids, err := cms.ChildPages(database.DB, idInt); err != nil || len(kids) > 0 {
		return skip("このページには子ページがあるので、取り込まない扱いにはしません（先に子ページを動かしてください）")
	}

	draft, hashes, ok := draftDrawing(user, pageID)
	if !ok {
		return skip("このページに図面ブロックがありません")
	}
	dupBody, err := cms.ReadPageBody(dupID)
	if err != nil {
		return skip("既にある加工製品を読めません: " + err.Error())
	}
	m, ok := matchDrawing(user, draft, hashes, dupBody)
	if !ok {
		return skip("/" + dupID + " には同じ図面番号の図面も同じファイルもありません。重複としては片付けられません")
	}

	noted := ""
	if src := draft.Source; src != "" && !strings.Contains(dupBody, src) {
		pair := `<dt>` + htmlEscape(SourceRefTag) + `</dt><dd>` + htmlEscape(src) + `</dd>`
		wrote := false
		if err := cms.RewriteBody(dupID, user.Username, func(current string) string {
			out, ok := addTagToDrawing(current, m.Index, pair)
			wrote = ok
			return out
		}); err != nil {
			return skip("既にある加工製品に受信元を書き足せません: " + err.Error())
		}
		if wrote {
			noted = "・このメールの受信元を書き足しました"
		}
	}
	if _, err := cms.DeletePageToTrash(pageID); err != nil {
		return skip("このページをごみ箱へ移せません: " + err.Error())
	}
	auth.Audit(user.Username, "file-drawing.duplicate", pageID+" -> "+dupID)
	why := "同じ図面番号"
	switch {
	case m.SameNo && m.SameFile:
		why = "同じ図面番号・同じファイル"
	case m.SameFile:
		why = "同じファイル"
	}
	return filingResult{PageID: pageID, Outcome: "discarded", TargetID: dupID,
		Message: "「" + pageTitleOf(dupID) + "」（/" + dupID + "）と" + why + "なので取り込みませんでした" +
			"（解析で作ったページはごみ箱へ" + noted + "）"}
}

// addTagToDrawing は本文の index 番目の図面ブロックの最初の可変タグの並びへ、タグの組 pair を足します。
func addTagToDrawing(body string, index int, pair string) (string, bool) {
	i, out, done := 0, body, false
	eachSection(body, func(sec string, open, _ int) {
		if done || !isDrawingSection(sec) {
			return
		}
		if i == index {
			if at := cms.EndOfFirstTagList(sec); at >= 0 {
				out = body[:open+at] + pair + body[open+at:]
				done = true
			}
		}
		i++
	})
	return out, done
}

// ── 🤖 中身を比べる ──────────────────────────────────────────────────────

// compareDrawingsMaxBytes は2つのPDFを合わせた上限です（Gemini へそのまま載せる大きさ）。
const compareDrawingsMaxBytes = 18 << 20

// compareDrawingsPrompt は2つの図面PDFを見比べさせる指示です（番号は呼ぶときに埋める）。
const compareDrawingsPrompt = `2つのPDFを渡しました。どちらも機械部品の図面です（1つのPDFに複数の図面が入っていることがあります）。
1つ目のPDFの中の図面番号「%A%」の図面と、2つ目のPDFの中の図面番号「%B%」の図面を見比べてください
（番号が空のときは、そのPDFの最初の図面）。

同じ図面か（形・寸法・公差・注記・材質・表面処理・改訂記号と改訂の欄がすべて同じか）を判定し、
違いがあれば具体的に挙げてください（例: 「寸法 120 → 125」「改訂記号 A → B」「穴が1つ増えた」）。
⚠ PDFの作り方の違い（線の太さ・色・解像度・余白・ファイルの作成日時・紙の大きさ）は違いに数えないでください。

JSONだけを返してください:
{"same": true または false, "differences": ["違い1", "違い2"], "summary": "一言で"}`

// compareVerdict は見比べた結果です。
type compareVerdict struct {
	Same        bool     `json:"same"`
	Differences []string `json:"differences"`
	Summary     string   `json:"summary"`
}

// CompareDrawingsAPIHandler は POST /api/compare-drawings です。
// 入力: {"page_id": 整理を待つ仮のページ, "with": 既にある加工製品}。
// 仮のページの図面PDFと、既にある加工製品の同じと見える図面のPDFを見比べます。
// 中身（sha256）が同じなら Gemini を呼ばずに「同じファイル」と答えます。
func CompareDrawingsAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		With   string `json:"with"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	draftID, ok1 := page.NormalizeID(req.PageID)
	withID, ok2 := page.NormalizeID(req.With)
	if !ok1 || !ok2 {
		cms.JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	draft, hashes, ok := draftDrawing(user, draftID)
	if !ok {
		cms.JSONFail(w, http.StatusNotFound, "整理を待つページの図面が見つかりません")
		return
	}
	withInt, err := strconv.Atoi(withID)
	if err != nil || !page.CanView(user, withInt) {
		cms.JSONFail(w, http.StatusNotFound, "比べる相手が見つかりません")
		return
	}
	body, err := cms.ReadPageBody(withID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "比べる相手を読めません")
		return
	}
	secs := drawingSectionsOf(body)
	idx := 0
	if m, ok := matchDrawing(user, draft, hashes, body); ok {
		idx = m.Index
	}
	if idx >= len(secs) {
		cms.JSONFail(w, http.StatusNotFound, "比べる相手に図面がありません")
		return
	}
	other := factsOfBlock(secs[idx])
	a, errA := firstPDF(user, draft.Refs)
	b, errB := firstPDF(user, other.Refs)
	if errA != nil || errB != nil {
		cms.JSONFail(w, http.StatusNotFound, "図面のPDFを読めません: "+errors.Join(errA, errB).Error())
		return
	}
	if sha256.Sum256(a) == sha256.Sum256(b) {
		cms.WriteJSON(w, map[string]any{"success": true, "same": true, "same_file": true,
			"summary": "同じファイルです（中身がバイト単位で同じ）"})
		return
	}
	if len(a)+len(b) > compareDrawingsMaxBytes {
		cms.JSONFail(w, http.StatusRequestEntityTooLarge, "PDFが大きすぎて見比べられません（2つで18MBまで）")
		return
	}
	prompt := strings.NewReplacer("%A%", draft.No, "%B%", other.No).Replace(compareDrawingsPrompt)
	text, err := cms.GeminiGenerateBlobs(prompt,
		genai.Blob{MIMEType: "application/pdf", Data: a}, genai.Blob{MIMEType: "application/pdf", Data: b})
	if err != nil {
		if errors.Is(err, cms.ErrNoGeminiKey) {
			cms.JSONFail(w, http.StatusServiceUnavailable, "サーバーに GEMINI_API_KEY 環境変数が設定されていません。設定してから起動し直してください。")
			return
		}
		cms.JSONFail(w, http.StatusBadGateway, "見比べられませんでした: "+err.Error())
		return
	}
	v, err := parseCompareVerdict(text)
	if err != nil {
		cms.JSONFail(w, http.StatusBadGateway, err.Error())
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "same": v.Same, "same_file": false,
		"differences": v.Differences, "summary": v.Summary})
}

// parseCompareVerdict は Gemini の返答を読みます（Gemini を呼ばずに試せるよう切り出してある）。
func parseCompareVerdict(text string) (compareVerdict, error) {
	var v compareVerdict
	if err := json.Unmarshal([]byte(cms.StripJSONFence(text)), &v); err != nil {
		head := []rune(strings.TrimSpace(cms.StripJSONFence(text)))
		if len(head) > 200 {
			head = append(head[:200], '…')
		}
		return v, errors.New("見比べた結果を読めません: " + err.Error() + "（返ってきたもの: " + string(head) + "）")
	}
	if v.Differences == nil {
		v.Differences = []string{}
	}
	return v, nil
}

// firstPDF は参照のうち最初のPDFの中身を返します。
func firstPDF(user *auth.User, refs []cms.FileRef) ([]byte, error) {
	for _, ref := range refs {
		path, name, ok := refFile(user, ref)
		if !ok || !strings.EqualFold(filepath.Ext(path), ".pdf") {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.New(name + " を読めません")
		}
		return b, nil
	}
	return nil, errors.New("PDFがありません")
}
