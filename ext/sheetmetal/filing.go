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
// 作るところまでで、**通信箱がそのまま「まだ分からないものの置き場」**になります。
// 整理は分かったとき（たいてい後続のメールや電話）に、この操作で行います。
//
// 行き先は **`取引先／社名／段／装置名称／図面名称`** です。ワンノートの製造部品
// ページの形に、2026-09-05 の2つの決定を足したもの:
//
//   - **顧客名ページは `取引先` の下**（トップ直下をやめた。cms.EnsurePartnerBox）
//   - **装置名称の上に段**（現行・旧型・試作…）。ユーザー:「装置名の上の段として、
//     旧型、現行、試作などがあったほうが探しやすいです」。段の名前は設定が持ちます
//     （`machine_stages`）——「など」と付いたので増える前提です。
//
// **段は人が毎回選びます**（同日ユーザー決定）。機械が入れるのは初期値だけで、
// 既にその装置が在ればその段、無ければ一覧の先頭（現行）。試作か現行かは
// メールを読まないと分からないので、**機械には決められない**ためです。
//
// 見積だけ・試作のときは装置名称から新しく作ります。ユーザー:「これは、メールの
// 内容から判断するしかありません」——人が欄を打ち替える前提です。
// なお `【試作】装置名称` という題の付け方（2026-09-03）は、**段ができたので
// 要らなくなりました**——題と段の両方に「試作」と書くと二重になります。
//
// 移した先に同名の部品ページが在れば、その図面は**改定図面**です（ユーザー）。
// 顧客名／装置名称の下では図面名称が一意なので、**ページが在ること自体が改定の合図**。
// **旧版は最新版の子ページになります**（2026-09-06 ユーザー:「旧版を最も新しい版の
// 子にしてはどうでしょう。ワンノートではページが子を持てなかったので、出来ません
// でしたが、CMSでは可能では？」）。もとは同じページに積み上げて古いものに赤枠を
// 付ける形でしたが、**それはワンノートの制約を写しただけ**でした。詳しくは
// mergeAsRevision。
//
// **顧客名・装置名称・図面名称は早期に正規化します**（2026-09-06 ユーザー）。
// この3つはそのままページの題になり、題の完全一致が階層の同一性を決めるので、
// 畳まずに入れると `φ３２０　三輪共通` が別の装置ページになります
// （`cms.NormalizeNameForIngest`）。
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
	"time"

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
	// Stage は装置の段（現行・旧型・試作…）の推奨値です。**既にその装置が在れば
	// その段**、無ければ一覧の先頭。ユーザー決定は「人が毎回選ぶ」なので、
	// これは初期値であって決定ではありません（2026-09-05）。
	Stage string `json:"stage"`
}

// suggestStage はその装置がいま居る段を探します（無ければ一覧の先頭）。
//
// **人が毎回選ぶ**のが決定ですが、既にある装置を別の段へ入れてしまう事故は
// 初期値で防げます——`取引先／社名／段／装置名称` を段ごとに当たります。
func suggestStage(customer, machine string) string {
	stages := cms.MachineStages()
	fallback := ""
	if len(stages) > 0 {
		fallback = stages[0]
	}
	if customer == "" || machine == "" {
		return fallback
	}
	boxID, ok := cms.PartnerBoxPageID()
	if !ok {
		return fallback
	}
	custID, ok := findChildByTitle(boxID, customer)
	if !ok {
		return fallback
	}
	for _, st := range stages {
		stageID, ok := findChildByTitle(custID, st)
		if !ok {
			continue
		}
		if _, ok := findChildByTitle(stageID, machine); ok {
			return st
		}
	}
	return fallback
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
	// **受注ページも同じ画面で扱います**（2026-09-06）。行き先の決め方は違いますが
	// （部品は人が打ち、受注は発注日で決まる）、人がやることは同じ「解析で生まれた
	// ものを片付ける」なので、ボタンを2つに分ける理由がありません。
	orders, err := orderChildrenOf(user, idInt)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "受注の一覧を作れません: "+err.Error())
		return
	}
	// 段の一覧も返します——**選べる値は設定が持つ**ので、画面に書き写しません
	// （語が2箇所にあると必ず片方が古くなる）。
	//
	// **既にある取引先の名前も返します**（2026-09-06）。初回の実データで
	// 「トーアスポーツマシーン」（アドレス帳が作った）と「株式会社トーアスポーツ
	// マシーン」（整理で人が打った）が**同じ会社で2枚**になりました。題の一致は
	// 完全一致のまま（名寄せを機械がやると別の顧客が1つに潰れる）で、
	// **人の目の前に既にある名前を出す**ことで解きます。
	// **装置名称の候補も返します**（2026-09-11 ユーザー:「装置名称の候補表示は
	// あると良いと思います」）。顧客名を `partners` で解いたのと同じ形で、
	// **一段下に同じ問題が残っていました**——実データでは1通のメールの5枚が
	// `φ410 2輪` / `2輪シュート改良` / `φ410-2輪` / `2軸シュート改良`（輪→軸の
	// 誤読）に割れ、そのまま流せば1台の装置が4フォルダに散ります。
	//
	// **顧客ごとに分けて返します**——装置名称は顧客の中でしか意味を持たないので、
	// 全部混ぜると他社の装置名が候補に出ます。
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "rows": rows, "orders": orders,
		"stages": cms.MachineStages(), "partners": partnerNames(user),
		"machines": machineNames(user)})
}

// suggestCustomer は顧客名の推奨値を返します。
//
// **読んだ名前ではなく、既にある取引先ページの題を第一にします**（2026-09-06
// ユーザー:「社名の揺れは、エイリアスの表かAIでなくせませんか？」）。揺れは
// 「機械が読んだ名前を人が打ち写す」ところで生まれるので、**打ち写す元を変えます**:
//
//	部品ページ → `受信元` タグ → 通信記録 → `差出人アドレス` → 取引先ページ → その題
//
// この鎖は**全部が完全一致**で、推測が1つも入りません。エイリアスの表もAIも
// 要らないのは、**同一性を名前で決めていない**からです。
//
// 引けなければ読めた名前（Geminiが図面から読んだ客先）へ戻ります——新しい顧客の
// 1通目はまだ取引先に居ないのが正常で、そのときは人が打ちます。
func suggestCustomer(user *auth.User, partPageID int, read string) string {
	if addr := senderAddressOf(partPageID); addr != "" {
		if title, ok := cms.PartnerTitleForAddress(user, addr); ok {
			return title
		}
	}
	return read
}

// senderAddressOf は部品ページの由来（`受信元`）をたどり、通信記録の差出人アドレスを返します。
//
// **由来のタグを見ます**（親ではなく）——部品ページは整理で動きますが、`受信元` は
// 動きません。値は「ページID-添付ID」なので、ハイフンの前だけ使います。
func senderAddressOf(partPageID int) string {
	var ref string
	database.DB.QueryRow(
		`SELECT value FROM vocab_index WHERE page_id = ? AND field = '受信元' LIMIT 1`,
		partPageID).Scan(&ref)
	ref = strings.TrimSpace(ref)
	if i := strings.Index(ref, "-"); i > 0 {
		ref = ref[:i]
	}
	srcID, err := strconv.Atoi(ref)
	if err != nil {
		return ""
	}
	var addr string
	database.DB.QueryRow(
		`SELECT value FROM vocab_index WHERE page_id = ? AND field = '差出人アドレス' LIMIT 1`,
		srcID).Scan(&addr)
	return strings.TrimSpace(addr)
}

// partnerNames は「取引先」の下にある相手ページの題を並べます（読めるものだけ）。
//
// 整理の画面の入力補助です。**選ばせるのではなく、候補として見せる**だけ——
// 新しい顧客の1枚目はここに無いので、打てなくしてはいけません。
func partnerNames(user *auth.User) []string {
	boxID, ok := cms.PartnerBoxPageID()
	if !ok {
		return []string{}
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return []string{}
	}
	dbRows, err := database.DB.Query(
		`SELECT id, COALESCE(title, '') FROM pages WHERE parent_id = ? ORDER BY title ASC`, boxInt)
	if err != nil {
		return []string{}
	}
	type row struct {
		id    int
		title string
	}
	var found []row
	for dbRows.Next() {
		var r row
		if err := dbRows.Scan(&r.id, &r.title); err != nil {
			dbRows.Close()
			return []string{}
		}
		found = append(found, r)
	}
	dbRows.Close()

	out := []string{}
	for _, r := range found {
		if r.title != "" && page.CanView(user, r.id) {
			out = append(out, r.title)
		}
	}
	return out
}

// machineNames は、既にある装置名称を**顧客ごと**に返します（画面の候補用）。
//
// 木は `取引先／社名／段／装置名称` なので、装置は顧客の**孫**です。
// **段はまたいで集めます**——人が知りたいのは「この装置はもう在るか」で、
// それがどの段に在るかは `suggestStage` が別に答えるためです（現行に在る装置を
// 試作へ入れ直すこともあり、段で絞ると既存が見えなくなります）。
//
// **候補を出すだけで、合わせるのは人**です。完全一致でしか階層は繋がらないので
// （findChildByTitle）、揺れを機械が吸収すると別の装置が1つに潰れます。
func machineNames(user *auth.User) map[string][]string {
	out := map[string][]string{}
	boxID, ok := cms.PartnerBoxPageID()
	if !ok {
		return out
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return out
	}
	// **3世代を1回のクエリで取ります**。行を読みながら別のクエリを投げると
	// `:memory:` DBでカーソルが接続を握ったままになり、**絞り込みが静かに全部落ちます**
	// （2026-09-03 に本番コードで踏んだ罠）。
	rows, err := database.DB.Query(`
		SELECT cust.id, COALESCE(cust.title, ''), mach.id, COALESCE(mach.title, '')
		  FROM pages cust
		  JOIN pages stage ON stage.parent_id = cust.id
		  JOIN pages mach  ON mach.parent_id  = stage.id
		 WHERE cust.parent_id = ?
		 ORDER BY cust.title ASC, mach.title ASC`, boxInt)
	if err != nil {
		return out
	}
	type hit struct {
		custID int
		cust   string
		machID int
		mach   string
	}
	var found []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.custID, &h.cust, &h.machID, &h.mach); err != nil {
			rows.Close()
			return out
		}
		found = append(found, h)
	}
	rows.Close()

	seen := map[string]map[string]bool{}
	for _, h := range found {
		if h.cust == "" || h.mach == "" {
			continue
		}
		// **読めないページの装置は数えません**（見せ分けC案——黙って落ちる）。
		if !page.CanView(user, h.custID) || !page.CanView(user, h.machID) {
			continue
		}
		if seen[h.cust] == nil {
			seen[h.cust] = map[string]bool{}
		}
		if seen[h.cust][h.mach] {
			continue // 同じ装置名が複数の段に在ることがある
		}
		seen[h.cust][h.mach] = true
		out[h.cust] = append(out[h.cust], h.mach)
	}
	return out
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
			Customer:    suggestCustomer(user, c.id, v["client-name"]),
			MachineName: v["machine-name"],
			Stage:       suggestStage(v["client-name"], v["machine-name"]),
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
	// Stage は装置の段です（現行・旧型・試作…）。**人が選びます**。
	Stage string `json:"stage"`
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
		// Orders は受注ページのIDだけ。直す欄が無いので、送るのは「押した」という
		// 事実だけです（行き先は発注日から決まる）。
		Orders []string `json:"orders"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}

	results := make([]filingResult, 0, len(req.Rows)+len(req.Orders))
	for _, row := range req.Rows {
		results = append(results, fileOneDrawing(user, row))
	}
	for _, id := range req.Orders {
		results = append(results, fileOneOrder(user, id))
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "results": results})
}

// fileOneDrawing は1枚の部品ページを行き先へ収めます。
func fileOneDrawing(user *auth.User, row filingRequest) filingResult {
	pageID, ok := page.NormalizeID(row.PageID)
	if !ok {
		return filingResult{PageID: row.PageID, Outcome: "skipped", Message: "ページIDが不正です"}
	}
	// **人が打った値もここで正規化します**（2026-09-06 ユーザー:「顧客名、装置名称、
	// 図面名称の値を早期に正規化したいです」）。この3つはそのままページの題になり、
	// **題の完全一致が階層の同一性**なので、揃えないと同じ装置のページが2枚できます。
	// 「人が打った値は見た目のまま」（normalize_text.go）の例外はここだけ——
	// 本文の中身ではなく、木の鍵だからです。
	customer := cms.NormalizeNameForIngest(row.Customer)
	machine := cms.NormalizeNameForIngest(row.MachineName)
	name := cms.NormalizeNameForIngest(row.DrawingName)
	stage := strings.TrimSpace(row.Stage)

	// **空欄は「まだ決められない」の意思表示**——移さずに置いたままにします
	// （通信箱が保留の置き場。空の顧客ページを増やさない）。
	if customer == "" || machine == "" || name == "" {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "顧客名・装置名称・図面名称のどれかが空なので、そのままにしました"}
	}
	// **段は表引きで閉じます**——「現行」と「現行品」が混ざると、探すときに
	// 静かに取りこぼします（設定 machine_stages が正本）。
	if !cms.ValidMachineStage(stage) {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "段（" + strings.Join(cms.MachineStages(), "・") + "）を選んでください"}
	}

	idInt, err := strconv.Atoi(pageID)
	if err != nil || !canWritePage(user, idInt) {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "このページを動かす権限がありません"}
	}

	// **人が直した値を、図面ブロックにも書き戻します**（2026-09-11 ユーザー:「整理で
	// 治した客先名は図面ブロックの索引にも反映させます。検索するときに困るからです」）。
	//
	// 解析が書いた `客先`・`装置名称` は**読み違いを含みます**（実データで
	// 『株式会社トーアス**ス**ポーツマシーン』が出た）。木の階層だけ直しても
	// **索引は誤読を持ったまま**なので、`客先` で探しても出てきません
	// ——索引が見るのは見出しの表示文字＝本文の値だからです（D-1）。
	//
	// 移す前に直すのは、**移動に失敗しても値は正しくなっている**ほうが害が小さい
	// ためです（値が正しくて場所が古いのは探せば見つかる。逆は見つからない）。
	if err := syncDrawingFields(user, pageID, customer, machine, name); err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "図面ブロックの値を直せません: " + err.Error()}
	}

	// **顧客名ページは「取引先」の下**です（2026-09-05 ユーザー決定）。アドレス帳が
	// 作る相手ページと**同じ場所・同じ1枚**——連絡先を見るページと部品を見るページを
	// 分けないため（cms.EnsurePartnerBox の説明が正本）。
	boxID, err := cms.EnsurePartnerBox(user)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "「" + cms.PartnerBoxTitle + "」ページを用意できません: " + err.Error()}
	}
	customerID, err := ensureChildPage(user, boxID, customer)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "顧客名ページを用意できません: " + err.Error()}
	}
	// **装置の上に段を1枚**（2026-09-05 ユーザー:「装置名の上の段として、旧型、現行、
	// 試作などがあったほうが探しやすいです」）。装置が別の段へ移るときは、
	// この段ページのあいだで付け替えるだけ——配下の図面もついていきます。
	stageID, err := ensureChildPage(user, customerID, stage)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "段のページを用意できません: " + err.Error()}
	}
	machineID, err := ensureChildPage(user, stageID, machine)
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
		Message: customer + "／" + stage + "／" + machine + "／" + name + " へ収めました"}
}

// syncDrawingFields は図面ブロックの `客先`・`装置名称`・`図面名称` を、整理の画面で
// 人が決めた値に揃えます。
//
// **索引のためです。** ③計算も将来の検索も `vocab_index` の `field`＝見出しの表示文字で
// 引くので（D-1）、本文が誤読のままだと階層をいくら直しても引けません。
//
// **書き換えるのは最初の図面ブロックだけ**です。改定で合流した古い図面は子ページへ
// 移っており（mergeAsRevision）、このページに残る図面は最新の1つ——`装置名称` と
// `客先` はそこにしか現れません。
//
// **値に印が付いていたら触りません**（`[^<]*` に当たらない＝人がリンクや強調を
// 書いた）。機械が人の手入れを踏み潰さないための線引きで、そのときは黙って
// そのままにします——階層は直るので、探せなくなるのは1件だけです。
func syncDrawingFields(user *auth.User, pageID, customer, machine, name string) error {
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return err
	}
	fixed := body
	for _, f := range []struct{ field, value string }{
		{"客先", customer},
		{"装置名称", machine},
		{"図面名称", name},
	} {
		fixed = setDrawingField(fixed, f.field, f.value)
	}
	if fixed == body {
		return nil // **変わらないなら書きません**——版と更新日時を無駄に進めない
	}
	return cms.RewriteBody(pageID, user.Username, func(string) string { return fixed })
}

// setDrawingField は図面ブロックの項目を書き換えます。**無ければ足します**。
//
// 足すのは、解析が**読めなかった項目を書かない**ためです——実データでは7枚のうち
// 3枚に `客先` の行がありませんでした。値を直すだけだと、その3枚は今後も
// `客先` で引けません（無い行は索引にも無い）。`客先` は図面ブロックの正式な列
// （vocab.go の drawing）なので、埋めるのは形を壊しません。
//
// 足す位置は**ヘッダの `dl` の末尾**です。`客先` は列の並びでも最後なので、
// たいてい定義どおりの順になります（`装置名称` が欠けていた場合だけ後ろへ付きますが、
// 並びは見た目だけの話で、索引も③計算も見出しの文字で引きます）。
func setDrawingField(body, field, value string) string {
	if fixed := replaceFirstFieldValue(body, field, value); fixed != body {
		return fixed
	}
	if strings.Contains(body, "<dt>"+field+"</dt>") {
		return body // 在るが差し替えなかった（同じ値・または人が印を書いている）
	}
	// 図面ブロックのヘッダの `dl` を探します。**属性の無い `<dl>`** がヘッダで、
	// `<dl data-type="tags">` は可変タグ（`受信元` などが入る別のもの）です。
	h2 := strings.Index(body, "<h2>図面</h2>")
	if h2 < 0 {
		return body
	}
	open := strings.Index(body[h2:], "<dl>")
	if open < 0 {
		return body
	}
	open += h2
	close := strings.Index(body[open:], "</dl>")
	if close < 0 {
		return body
	}
	close += open
	return body[:close] + "<dt>" + field + "</dt><dd>" + htmlEscape(value) + "</dd>" + body[close:]
}

// replaceFirstFieldValue は最初の `<dt>名前</dt><dd>値</dd>` の値を差し替えます。
//
// この形の正規表現はこのファイルの既定の作法です（`drawingNoRe`・`sourceRefRe`）
// ——図面ブロックは機械が書くので形が決まっており、DOMへ通して組み直すと
// **本文の他の場所まで書式が変わります**（正本なので、触っていない所は1バイトも
// 変えたくない）。
func replaceFirstFieldValue(body, field, value string) string {
	// **空欄は `<br/>` です**（サニタイズが空の `dd` に入れる）。実データの7枚のうち
	// 3枚の `客先` がこの形で、`[^<]*` だけで見ていたときは**素通りしていました**
	// ——行は在るのに値が無く、索引にも入らないので `客先` で永久に引けません。
	re := regexp.MustCompile(`<dt>` + regexp.QuoteMeta(field) + `</dt><dd>(<br\s*/?>|[^<]*)</dd>`)
	loc := re.FindStringSubmatchIndex(body)
	if loc == nil {
		return body
	}
	return body[:loc[2]] + htmlEscape(value) + body[loc[3]:]
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
// **旧版は最新版の子ページになります**（2026-09-06 ユーザー:「図面が改定された場合、
// 旧版を最も新しい版の子にしてはどうでしょう。ワンノートではページが子を持てなかった
// ので、出来ませんでしたが、CMSでは可能では？」）。もとは同じページに図面ブロックを
// 積み上げ、古いものに赤枠を付けて見分ける形でした——**ワンノートの制約を写した形**で、
// ページが子を持てるいまは写す理由がありません。
//
// 変わったこと:
//
//   - 部品ページに載るのは**最新の図面1つだけ**。積み上がらないので赤枠も要らない。
//   - 旧版は `旧版 <図面番号> <図面名称>` という子ページへ、**ブロックごと**移る
//     （由来の `受信元` はブロックの中にあるので、出所も一緒に付いて行く）。
//   - 改訂履歴は最新版に載ったまま。その版の行の図面番号が**旧版ページへのリンク**
//     になります（本文の `a[href]` は制限が無いので、参照タグの文法は要りません）。
//
// 仮のページを消すのは**物理削除ではなくゴミ箱への移動**です。
//
// **順序に意味があります**——旧版ページを先に作り、次に部品ページを書き換え、
// 最後に仮のページを片付けます。途中で失敗しても、図面がどこにも無い状態は生まれません。
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
	dstBody, err := cms.ReadPageBody(dstPageID)
	if err != nil {
		return err
	}

	// 1. いま載っている図面ブロックを外し、旧版ページへ移す。
	oldBlocks, rest := extractDrawingSections(dstBody)
	oldPageID, oldNo := "", ""
	if len(oldBlocks) > 0 {
		oldNo = drawingNoOf(oldBlocks[0])
		oldPageID, err = createOldVersionPage(user, dstPageID, oldNo, oldBlocks)
		if err != nil {
			return err
		}
		dstBody = rest
	}

	// 2. 新しい図面ブロックを見出しの直後へ。
	//
	// **ブロックIDが衝突しないようにする**——`ページID-ブロックID` はその改定の
	// 社内コードなので、1つのページの中で重複したら指し先が定まりません
	// （4桁 base36 なので確率は低いが、低いことと起きないことは違う）。
	block = reassignBlockIDIfTaken(block, dstBody)
	newNo := drawingNoOf(block)
	if err := cms.RewriteBody(dstPageID, user.Username, func(string) string {
		body := insertAfterH1String(dstBody, block)
		// **改訂履歴に1行足す**——社内コードの指し先はこの行です（vocab.go の
		// drawing-revisions）。図面ブロックは人が消せる決まりなので、消せるものを
		// 指し先にすると紙に出たコードが宙ぶらりんになります。
		body = InsertRevisionRow(body, newNo)
		if oldPageID != "" {
			body = linkRevisionRow(body, oldNo, oldPageID)
		}
		return body
	}); err != nil {
		return err
	}

	// 3. 合流し終えてから仮のページを片付ける。
	if _, err := cms.DeletePageToTrash(srcPageID); err != nil {
		return err
	}
	return nil
}

// createOldVersionPage は旧版の子ページを作り、そのIDを返します。
//
// 題は `旧版 <図面番号> <図面名称>`（2026-09-06 ユーザー決定）。同じ題の兄弟が
// 既に居れば受領日を添えます——**図面番号が変わらない改定**が実際にあるためです。
func createOldVersionPage(user *auth.User, dstPageID, oldNo string, blocks []string) (string, error) {
	name := pageTitleOf(dstPageID)
	title := strings.TrimSpace("旧版 " + strings.TrimSpace(oldNo) + " " + strings.TrimSpace(name))
	if _, taken := findChildByTitle(dstPageID, title); taken {
		title += "（" + time.Now().In(time.Local).Format("2006-01-02") + "）"
	}
	body := "<h1>" + htmlEscape(title) + "</h1>" + strings.Join(blocks, "")
	return cms.CreateChildPage(dstPageID, user.Username, body)
}

// extractDrawingSections は本文から図面ブロックを取り出し、残りの本文と一緒に返します。
//
// **`section` が入れ子にならない前提**です——業務ブロックは機能見出し形で平らに並ぶ
// （入れ子にする書き方が無い）。見出しの文字で見分けるのは、機能見出し形そのもの
// ——機械キーを本文へ書く属性はありません。
func extractDrawingSections(body string) (blocks []string, rest string) {
	var out strings.Builder
	i := 0
	for {
		open := strings.Index(body[i:], "<section")
		if open < 0 {
			break
		}
		open += i
		close := strings.Index(body[open:], "</section>")
		if close < 0 {
			break
		}
		end := open + close + len("</section>")
		sec := body[open:end]
		if strings.Contains(sec, "<h2>図面</h2>") {
			blocks = append(blocks, sec)
			out.WriteString(body[i:open]) // ブロックは落とし、間の本文は残す
		} else {
			out.WriteString(body[i:end])
		}
		i = end
	}
	out.WriteString(body[i:])
	return blocks, out.String()
}

// insertAfterH1String は h1 の直後へ差し込みます（文字列版）。
//
// コアの `InsertAfterH1` はページを読み書きしますが、ここでは**1回の書き換えで
// 全部やる**必要があります——外して・足して・履歴を直すのを別々に保存すると、
// 途中で失敗したときに図面の無いページが残ります。
func insertAfterH1String(body, fragment string) string {
	if i := strings.Index(body, "</h1>"); i >= 0 {
		at := i + len("</h1>")
		return body[:at] + fragment + body[at:]
	}
	return fragment + body
}

// linkRevisionRow は改訂履歴の中で図面番号が no の行を、旧版ページへのリンクにします。
//
// **見つからなければ何もしません**（手で消した履歴・古い形のページ）。旧版へは
// 子ページの一覧からも行けるので、ここが効かなくても行き止まりにはなりません。
func linkRevisionRow(body, no, oldPageID string) string {
	no = strings.TrimSpace(no)
	if no == "" || oldPageID == "" {
		return body
	}
	at := strings.Index(body, `<table data-type="drawing-revision-items">`)
	if at < 0 {
		return body
	}
	cell := "<td>" + htmlEscape(no) + "</td>"
	rel := strings.Index(body[at:], cell)
	if rel < 0 {
		return body
	}
	i := at + rel
	linked := `<td><a href="/` + htmlEscape(oldPageID) + `">` + htmlEscape(no) + `</a></td>`
	return body[:i] + linked + body[i+len(cell):]
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
