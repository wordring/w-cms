package sheetmetal

// ─────────────────────────────────────────────────────────────────────────
// 部品ページの整理——機械が提案し、人が直して実行する（2026-09-03）
//
// ユーザー:「各図面の行き場所について、解析から得られた推奨値を提示して、
// ユーザーがそれを書き直して実行ボタンを押す形はどうですか？」
// 「顧客名を書く欄に推奨値を入れてユーザーが修正してはどうでしょう」
//
// **なぜ解析の場で決めないのか**——ユーザー:「人間が見てもなにを言っているのか
// 判断に困る場合も結構多いです。**なぜなら顧客は適当だからです**」。機械にも人にも
// 「いま」決められないなら、決めさせない。解析は部品ページを通信記録ページの子として
// 作るところまでで、**受信箱がそのまま「まだ分からないものの置き場」**になります。
// 整理は分かったとき（たいてい後続のメールや電話）に、この操作で行います。
//
// 行き先は「顧客名／装置名称／図面名称」——ワンノートの製造部品ページの形を
// そのまま持ってきたもの。**顧客名ページはトップ直下**（受信箱・テンプレートと同階層）。
//
// 見積だけ・試作のときは装置名称から新しく作ります（`【試作】装置名称` の形）。
// ユーザー:「これは、メールの内容から判断するしかありません」——**機械には
// 決められない**ので、人が欄を打ち替える前提です。
//
// 移した先に同名の部品ページが在れば、その図面は**改定図面**です（ユーザー）。
// 顧客名／装置名称の下では図面名称が一意なので、**ページが在ること自体が改定の合図**。
// 新しい図面ブロックを既存ページの**先頭**へ差し込みます——「新しいものが上」という
// 並びそのものが最新を表すので、どれが古いかを別に持たなくて済みます。
// 古い図面に赤枠を出すのは表示側の仕事（`class` はサニタイズで落ちるため保存しない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// filingRow は1枚の部品ページと、その行き先の推奨値です。
type filingRow struct {
	PageID      string `json:"page_id"`
	Title       string `json:"title"`
	DrawingNo   string `json:"drawing_no"`
	DrawingName string `json:"drawing_name"`
	Customer    string `json:"customer"`     // 推奨値（客先）。人が直す
	MachineName string `json:"machine_name"` // 推奨値（装置名称）。人が直す
}

// FilingProposalAPIHandler は GET /api/filing-proposal?page_id=X です。
// 通信記録ページ X から生まれた部品ページの一覧と、行き先の推奨値を返します。
func FilingProposalAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	pageID, ok := page.NormalizeID(r.URL.Query().Get("page_id"))
	if !ok {
		cms.JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	user := auth.CurrentUser(r)
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !page.CanView(user, idInt) {
		// 読めない相手には「無い」と同じ顔を見せる（匿名の404統一と同じ規律）。
		cms.JSONFail(w, http.StatusNotFound, "ページが見つかりません")
		return
	}

	rows, err := drawingChildrenOf(user, idInt)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "一覧を作れません: "+err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "rows": rows})
}

// drawingChildrenOf は、そのページの子のうち**図面ブロックを持つもの**を集めます。
// 推奨値は索引から読みます（解析が入れた値がそのまま初期値になる）。
func drawingChildrenOf(user *auth.User, parentIDInt int) ([]filingRow, error) {
	dbRows, err := database.DB.Query(
		`SELECT id, title FROM pages WHERE parent_id = ? ORDER BY id ASC`, parentIDInt)
	if err != nil {
		return nil, err
	}
	defer dbRows.Close()

	type child struct {
		id    int
		title string
	}
	var children []child
	for dbRows.Next() {
		var c child
		if err := dbRows.Scan(&c.id, &c.title); err != nil {
			return nil, err
		}
		children = append(children, c)
	}
	if err := dbRows.Err(); err != nil {
		return nil, err
	}

	out := []filingRow{}
	for _, c := range children {
		if !page.CanView(user, c.id) {
			continue // 見せ分け（C案）——読めないものは黙って落ちる
		}
		blocks, err := cms.VocabBlocksOf(database.DB, c.id, "drawing")
		if err != nil || len(blocks) == 0 {
			continue // 図面ページではない（受注ページなど）
		}
		// 図面が複数あるページ（既に改定を重ねたもの）は**先頭が最新**。
		v := blocks[0].Values
		out = append(out, filingRow{
			PageID:      formatID(c.id),
			Title:       c.title,
			DrawingNo:   v["drawing-no"],
			DrawingName: v["drawing-name"],
			Customer:    v["client-name"],
			MachineName: v["machine-name"],
		})
	}
	return out, nil
}

// filingRequest は「実行」で送られてくる1行です。
type filingRequest struct {
	PageID      string `json:"page_id"`
	Customer    string `json:"customer"`
	MachineName string `json:"machine_name"`
	DrawingName string `json:"drawing_name"`
	// ConfirmRevision は「図面番号が同じでも改定として合流してよい」の確認です。
	// 既定は false——**偽の改定を黙って作らない**ため（2026-09-03 ユーザー:
	// 「同じ図面名称を2回整理すると改定になるのはちょっとマズいと思います」）。
	ConfirmRevision bool `json:"confirm_revision"`
}

// filingResult は1行の結果です。何が起きたかを人へ返します
// （黙って動かすのではなく、**どこへ入ったか・改定になったか**を必ず見せる）。
type filingResult struct {
	PageID string `json:"page_id"`
	// "moved" / "revision" / "skipped" / "needs_confirm"（人の確認待ち）
	Outcome  string `json:"outcome"`
	Message  string `json:"message"`
	TargetID string `json:"target_id,omitempty"` // 改定のときは合流先
}

// FileDrawingsAPIHandler は POST /api/file-drawings です。
// 入力: {rows: [{page_id, customer, machine_name, drawing_name}, ...]}
func FileDrawingsAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		Rows []filingRequest `json:"rows"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}

	results := make([]filingResult, 0, len(req.Rows))
	for _, row := range req.Rows {
		results = append(results, fileOneDrawing(user, row))
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "results": results})
}

// fileOneDrawing は1枚の部品ページを行き先へ収めます。
func fileOneDrawing(user *auth.User, row filingRequest) filingResult {
	pageID, ok := page.NormalizeID(row.PageID)
	if !ok {
		return filingResult{PageID: row.PageID, Outcome: "skipped", Message: "ページIDが不正です"}
	}
	customer := strings.TrimSpace(row.Customer)
	machine := strings.TrimSpace(row.MachineName)
	name := strings.TrimSpace(row.DrawingName)

	// **空欄は「まだ決められない」の意思表示**——移さずに置いたままにします
	// （受信箱が保留の置き場。空の顧客ページを増やさない）。
	if customer == "" || machine == "" || name == "" {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "顧客名・装置名称・図面名称のどれかが空なので、そのままにしました"}
	}

	idInt, err := strconv.Atoi(pageID)
	if err != nil || !canWritePage(user, idInt) {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "このページを動かす権限がありません"}
	}

	customerID, err := ensureChildPage(user, cms.TopPageID, customer)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "顧客名ページを用意できません: " + err.Error()}
	}
	machineID, err := ensureChildPage(user, customerID, machine)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "装置名称ページを用意できません: " + err.Error()}
	}

	// **既にあれば改定図面**（ユーザー決定）。顧客名／装置名称の下では図面名称が
	// 一意なので、ページが在ること自体が改定の合図。
	if existing, found := findChildByTitle(machineID, name); found && existing != pageID {
		// **偽の改定を作らない**——同じ添付から作られたものは重複、図面番号が
		// 同じものは人に確認する（duplicateReason）。
		if reason, needsConfirm, err := checkRevision(pageID, existing, row.ConfirmRevision); err != nil {
			return filingResult{PageID: pageID, Outcome: "skipped",
				Message: "合流先を調べられません: " + err.Error()}
		} else if reason != "" {
			outcome := "skipped"
			if needsConfirm {
				outcome = "needs_confirm"
			}
			return filingResult{PageID: pageID, Outcome: outcome, TargetID: existing, Message: reason}
		}
		if err := mergeAsRevision(user, pageID, existing); err != nil {
			return filingResult{PageID: pageID, Outcome: "skipped",
				Message: "改定として合流できません: " + err.Error()}
		}
		auth.Audit(user.Username, "file-drawing.revision", pageID+" -> "+existing)
		return filingResult{PageID: pageID, Outcome: "revision", TargetID: existing,
			Message: customer + "／" + machine + "／" + name + " の改定図面として合流しました"}
	}

	if err := movePage(user, pageID, machineID, name); err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "移動できません: " + err.Error()}
	}
	auth.Audit(user.Username, "file-drawing.move", pageID+" -> "+machineID)
	return filingResult{PageID: pageID, Outcome: "moved",
		Message: customer + "／" + machine + "／" + name + " へ収めました"}
}

// findChildByTitle は親の子から題が完全一致するものを1つ探します。
// **完全一致だけ**にするのは、揺れを機械が吸収すると別の顧客が1つに潰れるため
// ——名寄せは人の仕事です（欄を直せば済む）。
func findChildByTitle(parentID, title string) (string, bool) {
	parentInt, err := strconv.Atoi(parentID)
	if err != nil {
		return "", false
	}
	var id int
	err = database.DB.QueryRow(
		`SELECT id FROM pages WHERE parent_id = ? AND title = ? ORDER BY id ASC LIMIT 1`,
		parentInt, title).Scan(&id)
	if err != nil {
		return "", false
	}
	return formatID(id), true
}

// ensureChildPage は題の一致する子を返し、無ければ作ります。
func ensureChildPage(user *auth.User, parentID, title string) (string, error) {
	if id, found := findChildByTitle(parentID, title); found {
		return id, nil
	}
	parentInt, err := strconv.Atoi(parentID)
	if err != nil {
		return "", err
	}
	if !canWritePage(user, parentInt) {
		return "", errors.New("親ページへ書き込む権限がありません")
	}
	return cms.CreateChildPage(parentID, user.Username, "<h1>"+htmlEscape(title)+"</h1>")
}

// canWritePage は書き込み権限の素の判定です（HTTPの口を通さない版）。
func canWritePage(user *auth.User, pageIDInt int) bool {
	return page.GetPerms(pageIDInt).CanWrite(user)
}

// formatID はページIDをゼロ詰め6桁へ整えます。
func formatID(idInt int) string {
	return fmt.Sprintf("%0*d", page.IDLength, idInt)
}

// htmlEscape は本文へ入れる前の逃がしです（サニタイザは安全の網で、
// エスケープの肩代わりはしません——サニタイズ後にHTMLを足す関数と同じ責任）。
func htmlEscape(s string) string { return stdhtml.EscapeString(s) }

// movePage は部品ページを行き先の下へ移し、題を図面名称に揃えます。
//
// 題を揃えるのは、**題がページ名だから**——整理の画面で図面名称を直したのに
// ページの題が古いままだと、次に同じ部品が来たとき「既にある」の判定
// （＝改定図面の合図）が効きません。
func movePage(user *auth.User, pageID, newParent, title string) error {
	if err := cms.SetPageH1(pageID, user.Username, htmlEscape(title)); err != nil {
		return err
	}
	_, _, err := cms.SetPageParent(user, pageID, newParent)
	return err
}

// mergeAsRevision は改定図面を既存の部品ページへ合流させます。
//
// 新しい図面ブロックを既存ページの**先頭**（見出しの直後）へ差し込み、空になった
// 仮のページをゴミ箱へ移します。「新しいものが上」という並びそのものが最新を表すので、
// どれが古いかを別に持つ必要がありません（古い図面の赤枠は表示側の仕事）。
//
// 仮のページを消すのは**物理削除ではなくゴミ箱への移動**です。図面ブロックは
// 合流先へ運ばれており、由来（受信元タグ）もブロックの中に付いて行くので、
// 消えて困る情報は残りません。
func mergeAsRevision(user *auth.User, srcPageID, dstPageID string) error {
	srcBody, err := cms.ReadPageBody(srcPageID)
	if err != nil {
		return err
	}
	block := cms.FirstBlockHTML(srcBody)
	if strings.TrimSpace(block) == "" {
		return errors.New("移す図面ブロックが見つかりません")
	}
	dstInt, err := strconv.Atoi(dstPageID)
	if err != nil || !canWritePage(user, dstInt) {
		return errors.New("合流先へ書き込む権限がありません")
	}

	// **ブロックIDが合流先と衝突しないようにする**——`ページID-ブロックID` は
	// その改定の社内コードなので、1つのページの中で重複したら指し先が定まりません
	// （4桁 base36 なので確率は低いが、低いことと起きないことは違う）。
	dstBody, err := cms.ReadPageBody(dstPageID)
	if err != nil {
		return err
	}
	block = reassignBlockIDIfTaken(block, dstBody)
	if err := cms.InsertAfterH1(dstPageID, user.Username, block); err != nil {
		return err
	}
	// **改訂履歴に1行足す**——社内コードの指し先はこの行です（vocab.go の
	// drawing-revisions）。図面ブロックは「赤枠で残して人が消す」決まりなので、
	// 消せるものを指し先にすると紙に出たコードが宙ぶらりんになります。
	if err := cms.RewriteBody(dstPageID, user.Username, func(body string) string {
		return InsertRevisionRow(body, drawingNoOf(block))
	}); err != nil {
		return err
	}
	// 合流し終えてから仮のページを片付ける（順序が逆だと、失敗したときに
	// 図面がどこにも無い状態が生まれる）。
	if _, err := cms.DeletePageToTrash(srcPageID); err != nil {
		return err
	}
	return nil
}

// blockIDRe は section の先頭に付いたブロックIDを拾います。
var blockIDRe = regexp.MustCompile(`^<section data-id="([0-9a-z]+)"`)

// reassignBlockIDIfTaken は、運ぶブロックのIDが合流先で既に使われていたら振り直します。
// 使われていなければ**そのまま**——既にどこかで社内コードとして書き留められて
// いるかもしれないので、必要のない振り直しはしません。
func reassignBlockIDIfTaken(block, dstBody string) string {
	m := blockIDRe.FindStringSubmatch(block)
	if m == nil {
		return block
	}
	if !strings.Contains(dstBody, `data-id="`+m[1]+`"`) {
		return block
	}
	return strings.Replace(block,
		`data-id="`+m[1]+`"`, `data-id="`+cms.NewBlockID(dstBody)+`"`, 1)
}

// drawingNoRe は図面ブロックから図面番号を拾います（改訂履歴の行に載せる）。
var drawingNoRe = regexp.MustCompile(`<dt>図面番号</dt><dd>([^<]*)</dd>`)

// drawingNoOf は図面ブロックの図面番号を返します（無ければ空）。
func drawingNoOf(block string) string {
	if m := drawingNoRe.FindStringSubmatch(block); m != nil {
		return m[1]
	}
	return ""
}

// ── 偽の改定を作らないための検査（2026-09-03）──────────────────────────
//
// ユーザー:「同じ図面名称を2回整理すると改定になるのはちょっとマズいと思います」。
// 同じPDFを解析し直して整理に流すと、**中身は同じなのに版が増えます**。
// 履歴が嘘になり、社内コードが増え、赤枠の古い図面が意味も無く積み上がります。
//
// 判定は2段に分けます:
//
//	確実な重複  = 由来（受信元）が既存の図面ブロックと同じ。**同じ添付から作った**
//	              ものなので、改定ではありえない。黙って止める
//	疑わしい    = 図面番号が既存の版と同じ。改定なら普通は番号か改訂記号が変わる。
//	              **機械には決められない**ので人に尋ねる（確認して再実行）

// sourceRefRe は図面ブロックの由来（受信元）を拾います。
var sourceRefRe = regexp.MustCompile(`<dt>受信元</dt><dd>([^<]*)</dd>`)

// revNumberRe は改訂履歴の行から図面番号を拾います。
var revNumberRe = regexp.MustCompile(`<tr data-id="[0-9a-z]+"><td>[0-9]+</td><td>([^<]*)</td>`)

// duplicateReason は合流させてよいかを調べ、止める理由を返します
// （空なら合流してよい）。needsConfirm は「人が確認すれば通してよい」の印です。
func duplicateReason(block, dstBody string, confirmed bool) (reason string, needsConfirm bool) {
	// 確実な重複——同じ添付から作られている。確認しても通しません。
	if m := sourceRefRe.FindStringSubmatch(block); m != nil && strings.TrimSpace(m[1]) != "" {
		if strings.Contains(dstBody, "<dd>"+m[1]+"</dd>") {
			return "同じ添付から作られた図面が既にあります（重複なので合流しません）", false
		}
	}
	if confirmed {
		return "", false
	}
	// 疑わしい——図面番号が既存の版と同じ。改定なら普通は番号が変わります。
	no := strings.TrimSpace(drawingNoOf(block))
	if no == "" {
		return "", false
	}
	for _, m := range revNumberRe.FindAllStringSubmatch(dstBody, -1) {
		if strings.TrimSpace(m[1]) == no {
			return "図面番号「" + no + "」の版が既にあります。" +
				"改定なら普通は図面番号か改訂記号が変わります。" +
				"本当に改定として合流させるなら「改定として合流」にチェックして実行してください", true
		}
	}
	return "", false
}

// checkRevision は合流させてよいかを、運ぶブロックと合流先の本文から調べます。
func checkRevision(srcPageID, dstPageID string, confirmed bool) (reason string, needsConfirm bool, err error) {
	srcBody, err := cms.ReadPageBody(srcPageID)
	if err != nil {
		return "", false, err
	}
	dstBody, err := cms.ReadPageBody(dstPageID)
	if err != nil {
		return "", false, err
	}
	r, c := duplicateReason(cms.FirstBlockHTML(srcBody), dstBody, confirmed)
	return r, c, nil
}
