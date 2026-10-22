package toho

// ─────────────────────────────────────────────────────────────────────────
// 加工製品ページの整理——機械が提案し、人が直して実行する（2026-09-03）
//
// ユーザー:「各図面の行き場所について、解析から得られた推奨値を提示して、
// ユーザーがそれを書き直して実行ボタンを押す形はどうですか？」
// 「顧客名を書く欄に推奨値を入れてユーザーが修正してはどうでしょう」
//
// **なぜ解析の場で決めないのか**——ユーザー:「人間が見てもなにを言っているのか
// 判断に困る場合も結構多いです。**なぜなら顧客は適当だからです**」。機械にも人にも
// 「いま」決められないなら、決めさせない。解析は加工製品ページを通信記録ページの子として
// 作るところまでで、**通信箱がそのまま「まだ分からないものの置き場」**になります。
// 整理は分かったとき（たいてい後続のメールや電話）に、この操作で行います。
//
// 行き先は **`製造台帳／社名／装置名称／図面名称`** です（2026-10-16 に「加工製品」の箱をなくした。2026-09-29 に段を
// やめた・木の形は product_tree.go が正本）。ワンノートの製造部品ページの形に、
// 決定を足したもの:
//
//   - **顧客名ページは `製造台帳` の下**（2026-09-05・トップ直下をやめた。2026-10-16 の夕方までの名前は `取引先`、夜までは `製造`）
//   - ~~装置名称の上に段（現行・旧型・試作…）~~ → **2026-09-29 に `区分` のタグへ**。
//     利用者:「フォルダで分けるのをやめて、試作のタグをつけるように変更したい」。
//     段のフォルダは1つしか選べず、「試作かつ見積もり」を表せませんでした。
//
// **区分は人が選びます**（段のときと同じ理由）。試作か見積かはメールを読まないと
// 分からないので、**機械には決められません**。何も選ばなければ通常の製品です。
//
// 見積だけ・試作のときは装置名称から新しく作ります。ユーザー:「これは、メールの
// 内容から判断するしかありません」——人が欄を打ち替える前提です。
// なお `【試作】装置名称` という題の付け方（2026-09-03）は、段を経て**区分のタグに
// なったので要りません**——題とタグの両方に「試作」と書くと二重になります。
//
// 移した先に同名の加工製品ページが在れば、その図面は**改定図面**です（ユーザー）。
// 顧客名／装置名称の下では図面名称が一意なので、**ページが在ること自体が改定の合図**。
// （2026-09-20 から、改定か図面追加かは人が選びます。2026-10-01 から、同じ題の別の品物も「新規」で隣に置けます——fileOneDrawing。）
// **旧版は最新版の子ページになります**（2026-09-06 ユーザー:「旧版を最も新しい版の
// 子にしてはどうでしょう。ワンノートではページが子を持てなかったので、出来ません
// でしたが、CMSでは可能では？」）。もとは同じページに積み上げて古いものに赤枠を
// 付ける形でしたが、**それはワンノートの制約を写しただけ**でした。詳しくは
// mergeAsRevision。
//
// **顧客名・装置名称・図面名称は早期に正規化します**（2026-09-06 ユーザー）。
// この3つはそのままページの題になり、題の完全一致が階層の同一性を決めるので、
// 畳まずに入れると `φ３２０　共通台座` が別の装置ページになります
// （`cms.NormalizeNameForIngest`）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// filingRow は1枚の加工製品ページと、その行き先の推奨値です。
type filingRow struct {
	PageID      string `json:"page_id"`
	Title       string `json:"title"`
	DrawingNo   string `json:"drawing_no"`
	DrawingName string `json:"drawing_name"`
	Customer    string `json:"customer"`     // 推奨値（客先）。人が直す
	MachineName string `json:"machine_name"` // 推奨値（装置名称）。人が直す
	// Kinds はそのページにいま付いている区分（試作・見積…）です（2026-09-29）。
	// 画面の印の初期値で、**人が直します**。
	Kinds []string `json:"kinds"`
}

// FilingProposalAPIHandler は GET /api/filing-proposal?page_id=X です。
// 通信記録ページ X から生まれた加工製品ページの一覧と、行き先の推奨値を返します。
func FilingProposalAPIHandler(w http.ResponseWriter, r *http.Request) {
	_, idInt, user, ok := cms.GateJSONPageRead(w, r, r.URL.Query().Get("page_id"))
	if !ok {
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
		cms.JSONFail(w, http.StatusInternalServerError, "案件の一覧を作れません: "+err.Error())
		return
	}
	// 区分の選択肢も返します——**選べる値は設定（語彙 `区分`）が持つ**ので、画面に
	// 書き写しません（語が2箇所にあると必ず片方が古くなる）。
	//
	// **既にある取引先の名前も返します**（2026-09-06）。初回の実データで
	// 「南北スポーツ機械」（アドレス帳が作った）と「株式会社南北スポーツ
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
	// **記録の添付も返します**（2026-10-08・filing_files.go——図面以外のファイルも、行き先を探して足せるように）。
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "rows": rows, "orders": orders,
		"kinds": ProductKinds(), "partners": partnerNames(user),
		"machines": machineNames(user), "files": mailFilesOf(page.FormatID(idInt))})
}

// FilingTargetAPIHandler は GET /api/filing-target です。
// 整理の欄に打たれた行き先（顧客／装置名称／図面名称）に、**既にページがあるか**を返します。
//
// ユーザー:「図面名称と装置名称を手がかりに、改定図面や追加図面を認識するはずですが、
// 問題はそれらは**編集者が微妙に書き換える**ことです。整理画面で編集者が書き換える
// たびに、既存のページがあるか**検索しなおす**必要があります」（2026-09-20）。
//
// ⚠ **先に配っておけない値**です。装置名称の候補は顧客ごとの一覧を先に配って JS で
// 絞っていますが（`machines`）、図面名称は顧客×装置の数だけあるので配れません。
// だから**打ち替えのたびに聞きます**。
//
// ⚠ **調べるだけで、1枚も作りません。** 無ければ作る関数（当時の `ensureChildPage`・2026-10-19 に消した・
// いまの `ensureProductPath`）は使わず、フォルダだけを辿る `findFolderChild` で探します——**問い合わせただけで
// 空の顧客ページが増える**のはいちばん質の悪い副作用です。
//
// ⚠ **読めないページは「無い」と答えます**（見せ分け・C案）。存在を漏らさないためで、
// 匿名に「読めない」と「存在しない」を区別させない方針と同じです。
func FilingTargetAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	q := r.URL.Query()
	customer := cms.NormalizeNameForIngest(q.Get("customer"))
	machine := cms.NormalizeNameForIngest(q.Get("machine"))
	name := cms.NormalizeNameForIngest(q.Get("name"))

	out := map[string]any{"success": true, "exists": false}
	// **既にある加工製品の候補**（2026-09-30）——同じ取引先の中で、図面番号（版の印は除く）か題が同じページ。
	// 利用者:「既存の加工製品に図面を追加する場合、既存の加工製品の図面が修正され、新しい図面に変更する場合、
	// 全くの新規図面で加工製品ページを新設する場合がある」。装置名称が読めていない図面（表題欄に無い）は、
	// 行き先の題が一致せず「無い」になり、改定・追加の相手を人が探す手掛かりがありませんでした。
	// ⚠ **候補までです**——押すと欄に入り、決めるのは人（図面番号でページを自動的に束ねない）。
	cands := productCandidates(user, customer, q.Get("drawing_no"), name)
	// **同じ図面が既にあるか**（2026-09-30 利用者:「同じものがあるということを提示して欲しい」）——整理を待つ
	// ページを渡されたら、候補に「同じ図面番号」「同じファイル」の印を付けます（filing_duplicate.go）。
	if draftID, ok := page.NormalizeID(q.Get("page_id")); ok {
		markDuplicateCandidates(user, draftID, cands)
	}
	out["candidates"] = cands
	// 取引先の決まり（2026-10-01・partner_rules.go）——画面の「図面番号を品番に」「図面名称を品名に」の印の初期値。
	rule := partnerRuleFor(customer)
	out["rule"] = map[string]any{"part_no": rule.PartNoIsDrawingNo, "item_name": rule.ItemNameIsDrawingName}
	// 装置名称は空でもよい（2026-10-16 利用者:「装置名は新仕様とし…」——装置名の分からない品物は社名の直下）。
	if customer == "" || name == "" {
		json.NewEncoder(w).Encode(out) // まだ埋まっていない——「無い」と同じ扱い
		return
	}
	boxID, ok := CustomerBoxPageID()
	if !ok {
		json.NewEncoder(w).Encode(out)
		return
	}
	id := boxID
	// 木は `製造台帳／社名／（装置名称）`（2026-10-16 に「加工製品」の箱をなくした）。フォルダだけを辿る（findFolderChild）。
	path := []string{customer}
	if machine != "" {
		path = append(path, machine)
	}
	for _, title := range path {
		next, found := findFolderChild(id, title)
		if !found {
			json.NewEncoder(w).Encode(out)
			return
		}
		id = next
	}
	// 行き先の題のページは**何枚もありうる**（2026-10-01 利用者:「実際には同じ品名があります」——「新規」で隣に置ける）。
	// 読めるものだけを古い順に返し、図面追加・図面改定の相手は人が選ぶ（`same`）。
	type samePage struct {
		PageID     string   `json:"page_id"`
		DrawingNos []string `json:"drawing_nos"`
	}
	var same []samePage
	for _, sid := range childrenByTitle(id, name, "") {
		idInt, err := strconv.Atoi(sid)
		if err != nil || !page.CanView(user, idInt) {
			continue // 読めないものは「無い」
		}
		sp := samePage{PageID: sid}
		// **既に載っている図面番号**を添えます——人が「改定か、別の図面か」を決める
		// ときの手掛かりです（同じ番号なら改定、違う番号なら別図面のことが多い。
		// ⚠ **決めるのは人**で、機械はここでも候補までです）。
		if body, err := cms.ReadPageBody(sid); err == nil {
			sp.DrawingNos = drawingNosOf(body)
		}
		same = append(same, sp)
	}
	if len(same) == 0 {
		json.NewEncoder(w).Encode(out)
		return
	}
	out["exists"] = true
	out["page_id"] = same[0].PageID
	out["title"] = name
	out["drawing_nos"] = same[0].DrawingNos
	out["same"] = same
	json.NewEncoder(w).Encode(out)
}

// productCandidate は整理の欄に出す「既にある加工製品」の候補です。
type productCandidate struct {
	PageID     string `json:"page_id"`
	Title      string `json:"title"`
	Machine    string `json:"machine"`
	DrawingNos string `json:"drawing_nos"`
	// SameNo・SameFile は「届いた図面と同じと見える」印です（2026-09-30・filing_duplicate.go）——版の印まで
	// 同じ図面番号／図面ブロックが指すファイルの中身が同じ。整理を待つページ（`page_id`）を渡されたときだけ付きます。
	SameNo   bool `json:"same_no"`
	SameFile bool `json:"same_file"`
}

// drawingChildrenOf は、そのページの子のうち**図面ブロックを持つもの**を集めます。
// 推奨値は索引から読みます（解析が入れた値がそのまま初期値になる）。
func drawingChildrenOf(user *auth.User, parentIDInt int) ([]filingRow, error) {
	// **先に読み切ってから絞ります**（`cms.ChildPages`）。
	children, err := cms.ChildPages(database.DB, parentIDInt)
	if err != nil {
		return nil, err
	}
	out := []filingRow{}
	for _, c := range children {
		if !page.CanView(user, c.ID) {
			continue // 見せ分け（C案）——読めないものは黙って落ちる
		}
		// ⚠ **可変タグから読みます**（2026-09-18 に業務ブロックから移した）。
		// 同じ名前のタグは繰り返せますが、この4つは**1ページに1つ**です
		// （加工製品ページ＝1つの部品。改定で来た旧版は子ページへ移る）。
		tags, err := cms.TagsOfPage(database.DB, c.ID)
		if err != nil || cms.FirstTag(tags, DrawingNoTag) == "" &&
			cms.FirstTag(tags, DrawingNameTag) == "" {
			continue // 加工製品ページではない（受注ページなど）
		}
		client := cms.FirstTag(tags, ClientNameTag)
		machine := cms.FirstTag(tags, MachineNameTag)
		out = append(out, filingRow{
			PageID:      formatID(c.ID),
			Title:       c.Title,
			DrawingNo:   cms.FirstTag(tags, DrawingNoTag),
			DrawingName: cms.FirstTag(tags, DrawingNameTag),
			Customer:    suggestCustomer(user, c.ID, client),
			MachineName: machine,
			Kinds:       append([]string{}, tags[ProductKindTag]...),
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
	// Kinds は加工製品の区分です（試作・見積・旧型…・2026-09-29）。**人が選びます**。
	// 空なら通常の製品。選択肢は設定の語彙 `区分`。
	Kinds []string `json:"kinds"`
	// ConfirmRevision は「図面番号が同じでも改定として合流してよい」の確認です。
	// 既定は false——**偽の改定を黙って作らない**ため（2026-09-03 ユーザー:
	// 「同じ図面名称を2回整理すると改定になるのはちょっとマズいと思います」）。
	ConfirmRevision bool `json:"confirm_revision"`
	// ConfirmNoMachine は「その会社に装置名のフォルダがあるが、装置名称を空のまま社名の直下に入れてよい」の確認です
	// （2026-10-16 利用者:「装置名のフォルダがあるのに、装置名を空にしている場合は、警告して保存の可否を問い合わせてください」）。
	// ⚠ **ConfirmRevision とは別の印です**——1つの印を使い回すと、こちらで「はい」と答えた行が、図面改定の確かめまで飛ばします。
	ConfirmNoMachine bool `json:"confirm_no_machine"`
	// ConfirmSameCode は「同じ客先に同じ品番の加工製品ページが既にあるが、別の品物として新しく作る」の確認です
	// （2026-10-19・filing_same_code.go）。⚠ ConfirmRevision・ConfirmNoMachine とは別の印です。
	ConfirmSameCode bool `json:"confirm_same_code"`
	// Merge は**行き先に同じ題のページがあったとき、どうするか**です（2026-09-20）。
	//
	//	""・"new"   … 行き先に無ければ新規の加工製品ページ。**在れば動かしません**（人が決めるまで通信箱に置いたまま）
	//	"revision"  … 図面改定（いまの図面は旧版として子ページへ）。⚠ 行き先が無ければ作らずに断る（2026-09-30）
	//	"drawing"   … 図面追加（同じページに並べる・部品図と溶接図）。⚠ 同上
	//	"folder"    … 装置のページへ（装置名称のページに足す・組立図など——2026-10-08・filing_folder.go）。図面名称は見ない
	//	"duplicate" … 重複（取り込まない）。同じ図面が DuplicateOf に既にある（2026-09-30・filing_duplicate.go）
	//
	// ⚠ **既定を「改定」にしません。** 機械には区別できない（どちらも「同じ品物・
	// 違う図面番号」）ので、既定を置くと**見ないまま押した人がその既定に従います**。
	// 溶接図が黙って旧版になるのが、それまでの振る舞いでした。
	// 空欄は「まだ決められない」の意思表示、という整理の作法に揃えています。
	Merge string `json:"merge"`
	// DuplicateOf は「重複（取り込まない）」のときの、既にある加工製品のページIDです（2026-09-30）。
	DuplicateOf string `json:"duplicate_of"`
	// Target は図面追加・図面改定の行き先のページIDです（2026-10-01）——行き先に同じ題の加工製品が何枚もあるとき、
	// 人が候補を押して決めたもの。空なら題で引く（1枚だけならそれ）。
	Target string `json:"target"`
	// PartNoFromDrawing・ItemNameFromDrawing は「図面番号を品番に」「図面名称を品名に」の印です（2026-10-01 利用者:
	// 「図面名称を品名、図面番号を品番にするチェックボックスがあっても良いのかもしれません」）。新規のときだけ効く。
	// 無ければ（道具など印を送らない口）取引先のページの決まり（partner_rules.go）に従う。
	PartNoFromDrawing   *bool `json:"part_no_from_drawing"`
	ItemNameFromDrawing *bool `json:"item_name_from_drawing"`
}

// filingResult は1行の結果です。何が起きたかを人へ返します
// （黙って動かすのではなく、**どこへ入ったか・改定になったか**を必ず見せる）。
type filingResult struct {
	PageID string `json:"page_id"`
	// "moved" / "revision" / "added" / "discarded"（重複・取り込まない） / "skipped" / "needs_confirm"（人の確認待ち） / "needs_choice"
	Outcome  string `json:"outcome"`
	Message  string `json:"message"`
	TargetID string `json:"target_id,omitempty"` // 改定のときは合流先
	// Confirm は needs_confirm のとき、何の確かめか（"no_machine"＝装置名称が空・2026-10-16／"same_code"＝同じ客先に同じ品番・2026-10-19）。空なら図面の確かめ
	// （画面は「承知のうえで進める」〔confirm_revision〕の印を付けて送り直す）。
	Confirm string `json:"confirm,omitempty"`
}

// FileDrawingsAPIHandler は POST /api/file-drawings です。
// 入力: {rows: [{page_id, customer, machine_name, drawing_name, kinds, merge, …}, ...], orders: [{page_id, client}, ...]}
// （rows の全部の欄は `filingRequest`、orders は `orderRequest`〔filing_order.go〕）。
//
// 関門は行ごとです（`filingSourceGate`）。2026-10-12 からブラウザは編集権を取らないので、
// 「編集中なら飛ばす」は道具（`/api/lock`）が握っているときだけ効きます。人の保存とは、
// 本文を書く口（`cms.RewriteBody` など）の書き込みの錠で順に並びます。
func FileDrawingsAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Rows []filingRequest `json:"rows"`
		// Orders は受注ページの行です。⚠ **もとはIDの文字列だけでした**
		// （2026-09-20 に変えた）——`発注元` が直せるようになったので値を運びます。
		// 行き先は発注日から決まるので、そこは送りません。
		Orders []orderRequest `json:"orders"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}

	results := make([]filingResult, 0, len(req.Rows)+len(req.Orders))
	for _, row := range req.Rows {
		// ⚠ **ここでは受注の行を結びません**（2026-10-01 に外した）。加工製品ページが後から置かれたときの
		// 受注の `弊社品番` は、**受注フォルダを開いたとき**に埋まります（backlog.go の `linkBacklogRows`・
		// 利用者:「単純化しましょう」）。ここで結んでいた頃は、二つ目の図面（結果 "added"）を条件の
		// "drawing" で取りこぼしていた。
		results = append(results, fileOneDrawing(user, row))
	}
	for _, o := range req.Orders {
		results = append(results, fileOneOrder(user, o))
	}
	cms.WriteJSON(w, map[string]any{"success": true, "results": results})
}

// filingSourceGate は、整理で動かすページ（解析が作ったページ）を user が書けて、誰も開いていないかを見ます。
// 通せないときは飛ばす行を返します（ok=false）。
//
// **開いている人が居たら、その行は飛ばします**（2026-09-14）。整理は本文を読んで・変えて・書くので、誰かがエディタを
// 開いていると**オートセーブと上書きし合います**。関門をハンドラではなく行ごとに置くのは、整理が**複数のページへ書く**
// ためです（加工製品ページ・合流先）——1枚が編集中でも、残りは片付けられるほうがよい。
//
// （2026-10-12 から）ブラウザは編集権を取らないので、`EditorOpen` で飛ばすのは道具（`/api/lock` → `/api/save`）が
// 一瞬握っているときだけです。人がページを開いていても通り、書き込みは `cms.RewriteBody` の錠で人の保存と順に並びます。
func filingSourceGate(user *auth.User, pageID string) (idInt int, refused filingResult, ok bool) {
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !canWritePage(user, idInt) {
		return 0, filingResult{PageID: pageID, Outcome: "skipped", Message: "このページを動かす権限がありません"}, false
	}
	if holder, open := editlock.Locks.EditorOpen(idInt); open {
		return 0, filingResult{PageID: pageID, Outcome: "skipped",
			Message: "このページは編集中です（" + holder + "）。閉じてからもう一度お試しください"}, false
	}
	return idInt, filingResult{}, true
}

// fileOneDrawing は1枚の加工製品ページを行き先へ収めます。
func fileOneDrawing(user *auth.User, row filingRequest) filingResult {
	pageID, ok := page.NormalizeID(row.PageID)
	if !ok {
		return filingResult{PageID: row.PageID, Outcome: "skipped", Message: "ページIDが不正です"}
	}
	// **重複（取り込まない）は行き先の欄を見ません**（2026-09-30）——装置名称が読めていない図面でも片付けられるよう、
	// 空欄の確かめより先に分けます。権限と編集中の確かめは向こうで（仮のページと既にある加工製品の両方）。
	if row.Merge == "duplicate" {
		return discardDuplicate(user, pageID, row.DuplicateOf)
	}
	// **装置のページへ**（2026-10-08・filing_folder.go）——加工製品ではなく装置名称のページに図面を足す。図面名称の欄は見ない。
	if row.Merge == "folder" {
		return fileToMachineFolder(user, pageID, row)
	}
	in, refused, ok := readFilingInput(user, pageID, row)
	if !ok {
		return refused
	}
	customer, machine, name := in.customer, in.machine, in.name

	// **人が直した値を、図面ブロックにも書き戻します**（2026-09-11 ユーザー:「整理で
	// 治した客先名は図面ブロックの索引にも反映させます。検索するときに困るからです」）。
	//
	// 解析が書いた `客先`・`装置名称` は**読み違いを含みます**（実データで
	// 『株式会社南北ス**ス**ポーツマシーン』が出た）。木の階層だけ直しても
	// **索引は誤読を持ったまま**なので、`客先` で探しても出てきません
	// ——索引が見るのは見出しの表示文字＝本文の値だからです（D-1）。
	//
	// 移す前に直すのは、**移動に失敗しても値は正しくなっている**ほうが害が小さい
	// ためです（値が正しくて場所が古いのは探せば見つかる。逆は見つからない）。
	//
	// ⚠ **「二つ目の図面として追加」のときは図面名称を書き戻しません**（2026-09-20）。
	// そのときの欄の値は**行き先のページを決めるため**に打たれたもの（部品図と同じ題）で、
	// 運ぶブロックは溶接図です。書き戻すと**2枚目の名前が1枚目の名前に潰れます**。
	// 客先と装置名称は品物の属性なので、どちらでも揃えます。
	//
	// ⚠ **「図面改定」のときも書き戻しません**（2026-10-01）——利用者:「改定図面にする場合、品名を変えてくる
	// 場合があるようです。弊社としては、どちらの品名でも検索できる必要が出てきました」。改定の行き先は題で
	// 決まるので、欄には**既にあるページの題（古い名前）**が入ります。それを書き戻すと、改定図面に書いてある
	// **新しい名前が消えて**、新しい名前では探せなくなっていました。古い名前は題と旧版の子ページに残ります。
	writeName := name
	if row.Merge == "drawing" || row.Merge == "revision" {
		writeName = ""
	}
	if err := syncDrawingFields(user, pageID, customer, machine, writeName); err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "図面ブロックの値を直せません: " + err.Error()}
	}

	// **顧客名ページは「製造台帳」の下**です（2026-09-05 ユーザー決定。2026-10-16 の夕方までの名前は「取引先」、夜までは「製造」）。アドレス帳が
	// 作る相手ページと**同じ場所・同じ1枚**——連絡先を見るページと部品を見るページを
	// 分けないため（2026-09-05 の決定）。⚠ 2026-09-16 にこの決定を覆し、連絡先は別の木（`連絡帳`）へ分けました
	// ——同じ会社のページが2枚になります（customer_box.go の冒頭が正本）。
	//
	// **2つの木を参照タグで結びます**（2026-09-16）。`製造台帳／社名` のページから
	// `連絡帳／組織` を指す `連絡帳` のタグ（2026-09-17 までの名前は `相手`）を1つ書きます（`ensureProductPath` の中の
	// `linkPartner`）。⚠ **題だけで結んでいると、改名した日に切れます**——参照は
	// ページIDなので切れません。**人が選んだ社名で引きます**（機械が推した組織ではなく）
	// ——整理の画面は「機械が出して人が直す」場所なので、**打ち替えた結果が正**です。
	//
	// **装置は社名の直下**です（2026-10-16 に「加工製品」の箱をなくした・product_tree.go）。
	//
	// ⚠ **ここでは辿るだけで、作りません**（2026-10-22 の見直し）。作るのは、確かめを済ませて新しく置くとき（fileAsNewProduct）——
	// 確かめで「やめる」を選ぶと、空の社名・装置のフォルダが残っていた。行き先が無ければ、同じ題のページも無い。
	machineID, customerID, found := findProductPath(customer, machine)
	where := customer + "／" + machine + "／" + name
	if machine == "" {
		where = customer + "／" + name // 装置名の分からない品物は社名の直下
	}

	// **行き先に同じ題のページがあるとき**——改定か、二つ目の図面か（2026-09-20）。
	//
	// ⚠ **それまでは黙って改定にしていました。** 図面番号が違えば改定、としていた
	// のですが、**部品図と溶接図も「図面番号が違う」**ので、片方が旧版として子ページへ
	// 押し込まれます（ユーザー:「品物としては一つです」）。機械には区別できないので、
	// **人が選びます**。画面は打ち替えのたびに `/api/filing-target` へ聞いて、
	// ページがあれば選択肢を出します。
	//
	// ⚠ **同じ題の別の品物もあります**（2026-10-01 利用者:「品名がかぶると加工製品ページが追加できないようですが、
	// 実際には同じ品名があります」）——「新規」を選べば、同じ題のページが在っても隣に置きます。そのため行き先に同じ題が
	// **何枚も**ありえるので、図面追加・図面改定の相手は人が押した候補（`Target`）で決めます（1枚だけなら題で引く）。
	var sameTitle []string
	if found {
		sameTitle = childrenByTitle(machineID, name, pageID)
	}
	// 読めないページは候補にしない（番号を「選んでください」に並べ、図面番号を確かめの文に書いていた・2026-10-22 の見直し）。
	visible := sameTitle[:0]
	for _, sid := range sameTitle {
		if n, err := strconv.Atoi(sid); err == nil && page.CanView(user, n) {
			visible = append(visible, sid)
		}
	}
	sameTitle = visible
	if len(sameTitle) > 0 && row.Merge != "new" {
		linkPartner(user, customerID, customer) // 2つの木を結ぶ（ensureProductPath と同じ・行き先はもうある）
		return fileIntoSameTitle(user, row, in, sameTitle, where)
	}
	return fileAsNewProduct(user, row, in, machine, where, sameTitle)
}

// filingInput は整理の1行を正規化して確かめた値です（readFilingInput が組む・2026-10-20 に fileOneDrawing から分けた）。
type filingInput struct {
	pageID                  string
	idInt                   int
	customer, machine, name string
	kinds                   []string
}

// readFilingInput は整理の1行の値を正規化して確かめます（空欄・区分・権限と編集中・装置名称の空の問い合わせ）。
// 通らなければ、そのまま返す結果と false。
func readFilingInput(user *auth.User, pageID string, row filingRequest) (filingInput, filingResult, bool) {
	// **人が打った値もここで正規化します**（2026-09-06 ユーザー:「顧客名、装置名称、
	// 図面名称の値を早期に正規化したいです」）。この3つはそのままページの題になり、
	// **題の完全一致が階層の同一性**なので、揃えないと同じ装置のページが2枚できます。
	// 「人が打った値は見た目のまま」（normalize_text.go）の例外はここだけ——
	// 本文の中身ではなく、木の鍵だからです。
	customer := cms.NormalizeNameForIngest(row.Customer)
	machine := cms.NormalizeNameForIngest(row.MachineName)
	name := cms.NormalizeNameForIngest(row.DrawingName)

	// **顧客名・製品名称の空欄は「まだ決められない」の意思表示**——移さずに置いたままにします
	// （通信箱が保留の置き場。空の顧客ページを増やさない）。
	// ⚠ **装置名称は空でもよい**（2026-10-16 利用者:「装置名は新仕様とし、文書も修正して下さい」——【考察】全体の設計 §4.4
	// 「その下にオプションで装置名フォルダ」「装置名が分からないメーカーもあるので」）。空なら社名の直下に置きます。
	// それまでは装置名称の空も保留の意思表示でした。
	if customer == "" || name == "" {
		return filingInput{}, filingResult{PageID: pageID, Outcome: "skipped",
			Message: "顧客名・製品名称のどちらかが空なので、そのままにしました"}, false
	}
	// **区分は表引きで閉じます**——「試作」と「試作品」が混ざると、絞るときに
	// 静かに取りこぼします（設定の語彙 `区分` が正本）。
	kinds, bad := cleanProductKinds(row.Kinds)
	if bad != "" {
		return filingInput{}, filingResult{PageID: pageID, Outcome: "skipped",
			Message: "区分「" + bad + "」は選べません（" + strings.Join(ProductKinds(), "・") + "）"}, false
	}
	idInt, refused, ok := filingSourceGate(user, pageID)
	if !ok {
		return filingInput{}, refused, false
	}
	// **装置名称が空でも、その会社に装置名のフォルダがあれば、先に聞きます**（2026-10-16 利用者:「装置名のフォルダが
	// あるのに、装置名を空にしている場合は、警告して保存の可否を問い合わせてください」）。何も書く前に聞きます。
	if machine == "" && !row.ConfirmNoMachine {
		if names := machineFoldersOf(user, customer); len(names) > 0 {
			return filingInput{}, filingResult{PageID: pageID, Outcome: "needs_confirm", Confirm: "no_machine",
				Message: "「" + customer + "」には装置名のフォルダが " + strconv.Itoa(len(names)) + " つあります（" + strings.Join(firstN(names, 5), "・") +
					"）。装置名称が空のままです。装置名称を入れずに、社名の直下に入れますか？"}, false
		}
	}
	return filingInput{pageID: pageID, idInt: idInt, customer: customer, machine: machine, name: name, kinds: kinds}, filingResult{}, true
}

// filingKindsNote は区分を書いた結果を知らせる一言です（書けなくても整理は済んでいる）。
func filingKindsNote(user *auth.User, target string, kinds []string, replace bool) string {
	if err := setProductKinds(user, target, kinds, replace); err != nil {
		return "／⚠ 区分を書けませんでした: " + err.Error()
	}
	if len(kinds) > 0 {
		return "（区分: " + strings.Join(kinds, "・") + "）"
	}
	return ""
}

// fileIntoSameTitle は、行き先に同じ題のページがあるときの整理です（図面追加・図面改定——2026-10-20 に fileOneDrawing から分けた）。
// row.Merge が未選択なら、人に選ばせます。
func fileIntoSameTitle(user *auth.User, row filingRequest, in filingInput, sameTitle []string, where string) filingResult {
	pageID, name := in.pageID, in.name
	kindsNote := func(target string, replace bool) string { return filingKindsNote(user, target, in.kinds, replace) }
	if row.Merge != "drawing" && row.Merge != "revision" {
		// **未選択なら動かしません。** ⚠ どちらかを既定にすると、見ないまま
		// 押した人がその既定に従います——溶接図が黙って旧版になるのが、
		// それまでの振る舞いでした。
		return filingResult{PageID: pageID, Outcome: "needs_choice", TargetID: sameTitle[0],
			// 言葉は画面の選択肢と同じ（2026-09-30 利用者の案「図面改定」「図面追加」）。
			Message: "「" + name + "」は既にあります。「図面改定」か「図面追加」か、同じ名前の別の品物なら「新規」を選んでください"}
	}
	existing, why := pickMergeTarget(sameTitle, row.Target, name)
	if why != "" {
		return filingResult{PageID: pageID, Outcome: "needs_choice", Message: why}
	}
	// ⚠ **同じ添付・同じ図面番号は「疑わしい」**——止めずに人へ確認します
	// （2026-09-20 ユーザー:「実は、同じ添付同じPDFの中に同じ図面番号で別図面が
	// 入っているものがありました」）。**機械には重複を判定できません**ので、
	// 最後の砦は「人が確認の文を読む」ことだけになりました。
	suspicious, err := suspiciousSameSource(pageID, existing)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "合流先を調べられません: " + err.Error()}
	}
	if suspicious && !row.ConfirmRevision {
		return filingResult{PageID: pageID, Outcome: "needs_confirm", TargetID: existing,
			Message: "「" + name + "」に、同じ添付から作られた同じ図面番号の図面が既にあります。" +
				"⚠ 本来、同じ図番で別の図面はあってはならないので、" +
				"二重に整理した可能性が高いです。" +
				"先方の誤りで**実際に別の図面**なら、「承知のうえで進める」にチェックして実行してください"}
	}
	if suspicious {
		// ⚠ **通したことを残します。** 本来あってはならない形（同じ図番で別図面）を
		// 人の判断で受け入れた、という事実は、あとから「なぜ同じ番号が2枚あるのか」を
		// 調べる人にとって唯一の手掛かりです。
		auth.Audit(user.Username, "file-drawing.same-number-accepted", pageID+" -> "+existing)
	}
	// **図面追加で同じ図面が在る・図面改定で同じ図面が無い**ときは、人に確かめます（2026-10-03 利用者:「図面追加で同じ図面が
	// 在ったら警告、図面改定で同じ図面が無ければ警告ということになります。警告ダイアログが出て、追加するかやめるか選んでは
	// どうでしょうか」）。「同じ図面」は版の印を除いた図面番号が同じ図面（`sameDrawingKey`）。図面番号が読めていなければ
	// 判断しない。画面は確認の文をダイアログに出し、進めるなら `confirm_revision` を付けて送り直す。
	if !row.ConfirmRevision && (row.Merge == "drawing" || row.Merge == "revision") {
		if msg := mergeMismatch(pageID, existing, row.Merge, name); msg != "" {
			return filingResult{PageID: pageID, Outcome: "needs_confirm", TargetID: existing, Message: msg}
		}
	}

	switch row.Merge {
	case "drawing":
		// ⚠ **図面名称は書き戻しません**（上の `syncDrawingFields` は
		// `mergeDrawing` のとき名称を飛ばしています）。欄の値は**行き先を決める
		// ため**のもので、運ぶブロックの中身（溶接図自身の名前と番号）はそのまま。
		if err := mergeAsDrawing(user, pageID, existing); err != nil {
			return filingResult{PageID: pageID, Outcome: "skipped",
				Message: "図面追加できません: " + err.Error()}
		}
		auth.Audit(user.Username, "file-drawing.add", pageID+" -> "+existing)
		return filingResult{PageID: pageID, Outcome: "added", TargetID: existing,
			Message: where + " へ二つ目の図面として並べました" + kindsNote(existing, false)}

	case "revision":
		// **偽の改定を作らない**——図面番号が同じものは人に確認する。
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
			Message: where + " の改定図面として合流しました" + kindsNote(existing, false)}
	}
	return filingResult{PageID: pageID, Outcome: "needs_choice", Message: "図面改定か図面追加を選んでください"} // 上の switch のどれかで戻る
}

// fileAsNewProduct は、新しい加工製品として行き先へ置く整理です（2026-10-20 に fileOneDrawing から分けた）。
func fileAsNewProduct(user *auth.User, row filingRequest, in filingInput, machine, where string, sameTitle []string) filingResult {
	pageID, idInt, customer, name := in.pageID, in.idInt, in.customer, in.name
	kindsNote := func(target string, replace bool) string { return filingKindsNote(user, target, in.kinds, replace) }
	// ⚠ **改定・追加を選んだのに行き先が無いときは、作りません**（2026-09-30）——それまでは黙って新しい加工製品
	// ページになっていました（改定のつもりが新規・画面の選択肢がいつも出ていた間は実際に起こり得た）。相手は候補を
	// 押すか、装置名称・図面名称を既にあるページに合わせて決めます。
	if row.Merge == "revision" || row.Merge == "drawing" {
		return filingResult{PageID: pageID, Outcome: "needs_choice",
			Message: "「" + where + "」はまだありません。図面改定・図面追加は、既にある加工製品を行き先にしてください" +
				"（候補を押すと欄に入ります）。新しく作るなら「新規」を選んでください"}
	}
	// **同じ客先に同じ品番の加工製品ページが既にあれば、作る前に確かめます**（2026-10-19・filing_same_code.go）。
	// 2026-10-01 利用者:「とすると、二つの加工製品ページが同じ品番を持つのはマズいので、加工製品ページを作る前にチェックし、
	// 編集者に選択させる必要があります」。
	if hits, codes := sameCodeProducts(database.DB, user, idInt, customer); len(hits) > 0 {
		if !row.ConfirmSameCode {
			return filingResult{PageID: pageID, Outcome: "needs_confirm", Confirm: "same_code",
				TargetID: page.FormatID(hits[0]), Message: sameCodeMessage(customer, hits, codes)}
		}
		var ids []string
		for _, h := range hits {
			ids = append(ids, page.FormatID(h))
		}
		auth.Audit(user.Username, "file-drawing.same-code-accepted", pageID+" beside "+strings.Join(ids, ","))
	}
	// 確かめを済ませてから、行き先のフォルダを用意します（無ければ作る）。
	machineID, err := ensureProductPath(user, customer, machine)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: err.Error()}
	}
	if err := movePage(user, pageID, machineID, name); err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "移動できません: " + err.Error()}
	}
	auth.Audit(user.Username, "file-drawing.move", pageID+" -> "+machineID)
	// 弊社品番と、取引先の決まり（品番＝図面番号・品名＝図面名称）を書きます（2026-10-01・partner_rules.go）。
	ruleNote := applyPartnerRules(user, pageID, idInt, customer, name, row.PartNoFromDrawing, row.ItemNameFromDrawing)
	// 同じ題の隣に「新規」で置いたことは残し、知らせます（2026-10-01）——あとで「なぜ同じ名前が2枚あるのか」を
	// 調べる人の手掛かり。⚠ 同じ品物を二重に作っていないかは、品番・図面番号で人が見る（題は判断の基準にしない）。
	sameNote := ""
	if len(sameTitle) > 0 {
		auth.Audit(user.Username, "file-drawing.same-name", pageID+" beside "+strings.Join(sameTitle, ","))
		sameNote = "（同じ名前の加工製品が既に " + strconv.Itoa(len(sameTitle)) + " 枚あります——別の品物として隣に置きました）"
	}
	// 区分は**足すだけ**です（2026-10-08 に「揃える」からやめた）——整理の欄から区分の印を外したので、画面は区分を送らない
	// （利用者:「試作や見積もりによるフォルダ分けが無くなったので、整理ブロックで入力する必要はなくなりました」）。揃えると、
	// 送らなかった＝空で置き換えて、運んできたページに付いていた区分を消してしまう。区分を付けるのは加工製品ページで。
	return filingResult{PageID: pageID, Outcome: "moved",
		Message: where + " へ収めました" + sameNote + kindsNote(pageID, false) + ruleNote}
}

// applyPartnerRules は整理で新しく置いた加工製品ページに、弊社品番と取引先の決まりのタグを書き、知らせる一言を返します
// （書けなくても整理は済んでいる）。partNo・itemName は画面の印——あればそちらが決まりより先。
func applyPartnerRules(user *auth.User, pageID string, idInt int, customer, name string, partNo, itemName *bool) string {
	rule := partnerRuleFor(customer)
	if partNo != nil {
		rule.PartNoIsDrawingNo = *partNo
	}
	if itemName != nil {
		rule.ItemNameIsDrawingName = *itemName
	}
	if partNo != nil && itemName != nil {
		rule.Unknown = nil // 人が両方を決めたので、取引先の決まりの読めない値は効いていない
	}
	drawingNo := cms.PageTagValue(database.DB, idInt, DrawingNoTag)
	if err := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		return withProductRules(cur, pageID, drawingNo, name, rule)
	}); err != nil {
		return "／⚠ 弊社品番・品番・品名を書けませんでした: " + err.Error()
	}
	if len(rule.Unknown) > 0 {
		return "／⚠ 取引先の決まりの「" + strings.Join(rule.Unknown, "・") + "」は分かりません（書けるのは「" +
			PartNoRuleTag + "：" + ruleDrawingNo + "」「" + ItemNameRuleTag + "：" + ruleDrawingName + "」）"
	}
	return ""
}

// childrenByTitle は親の子のうち、題が完全一致するページを古い順に返します（except は除く）。
// 同じ題の別の品物があるので（2026-10-01）、1枚とは限りません。
func childrenByTitle(parentID, title, except string) []string {
	parentInt, err := strconv.Atoi(parentID)
	if err != nil {
		return nil
	}
	kids, err := cms.ChildPages(database.DB, parentInt)
	if err != nil {
		return nil
	}
	var out []string
	for _, k := range kids {
		if id := formatID(k.ID); k.Title == title && id != except {
			out = append(out, id)
		}
	}
	return out
}

// pickMergeTarget は図面追加・図面改定の行き先を、同じ題のページ（sameTitle）から決めます。
// 人が候補を押していれば（target）それ、1枚だけならそれ。決められなければ理由を返します。
func pickMergeTarget(sameTitle []string, target, name string) (string, string) {
	if target != "" {
		if id, ok := page.NormalizeID(target); ok {
			for _, s := range sameTitle {
				if s == id {
					return id, ""
				}
			}
		}
		return "", "選んだ行き先（/" + target + "）は「" + name + "」ではありません。候補を押し直してください"
	}
	if len(sameTitle) == 1 {
		return sameTitle[0], ""
	}
	return "", "「" + name + "」が " + strconv.Itoa(len(sameTitle)) + " 枚あります（/" + strings.Join(sameTitle, "・/") +
		"）。図面追加・図面改定の行き先は、下の候補から押して決めてください"
}

// linkPartner は加工製品の階層の社名ページから、連絡帳の組織ページへの参照タグを書きます。
//
// **何度呼んでも増えません**（既に在れば何もしない）。引けないときも黙って戻ります
// ——連絡帳にまだ居ない相手は普通にいるので、**結べないことは異常ではありません**。
//
// ⚠ **編集中のページには書きません**（`editlock.RefuseWhileEditing` と同じ理由ですが、
// ここは画面へ返す `http.ResponseWriter` を持たないので、**開いていたら黙って諦めます**
// ——整理そのものは続けたいからです。次に整理を押せば書かれます）。
// （2026-10-12 から）ブラウザは編集権を取らないので、諦めるのは道具（`/api/lock`）が握っているときだけです。
// 書き込みは `cms.RewriteBody` の錠の中なので、人の保存とは順に並びます。
func linkPartner(user *auth.User, customerID, title string) {
	partnerID, ok := contacts.PartnerByTitle(user, title)
	if !ok {
		return // まだ連絡帳に居ない（新しい顧客の1通目。異常ではない）
	}
	body, err := cms.ReadPageBody(customerID)
	if err != nil {
		return
	}
	// 既に結ばれていれば何もしない（同じタグを2つ並べない）。
	if hasTagPair(body, contacts.ContactsRefTag, partnerID) {
		return
	}
	// 道具が握っていれば書かない（次の整理で書けばよい）。
	_ = cms.RewriteBodyUnlessHeld(customerID, user.Username, func(current string) string {
		if hasTagPair(current, contacts.ContactsRefTag, partnerID) {
			return current
		}
		return cms.AddPageTags(current, "<dt>"+contacts.ContactsRefTag+"</dt><dd>"+partnerID+"</dd>")
	})
}
