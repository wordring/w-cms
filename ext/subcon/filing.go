package subcon

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
// 行き先は **`取引先／社名／段／装置名称／図面名称`** です。ワンノートの製造部品
// ページの形に、2026-09-05 の2つの決定を足したもの:
//
//   - **顧客名ページは `取引先` の下**（トップ直下をやめた。cms.EnsureContactsBox）
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
// 移した先に同名の加工製品ページが在れば、その図面は**改定図面**です（ユーザー）。
// 顧客名／装置名称の下では図面名称が一意なので、**ページが在ること自体が改定の合図**。
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
	"errors"
	stdhtml "html"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"w-cms/ext/comm"
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
	stages := MachineStages()
	fallback := ""
	if len(stages) > 0 {
		fallback = stages[0]
	}
	if customer == "" || machine == "" {
		return fallback
	}
	boxID, ok := CustomerBoxPageID()
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
		cms.JSONFail(w, http.StatusInternalServerError, "受注の一覧を作れません: "+err.Error())
		return
	}
	// 段の一覧も返します——**選べる値は設定が持つ**ので、画面に書き写しません
	// （語が2箇所にあると必ず片方が古くなる）。
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
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "rows": rows, "orders": orders,
		"stages": MachineStages(), "partners": partnerNames(user),
		"machines": machineNames(user)})
}

// FilingTargetAPIHandler は GET /api/filing-target です。
// 整理の欄に打たれた行き先（顧客／段／装置名称／図面名称）に、**既にページがあるか**を返します。
//
// ユーザー:「図面名称と装置名称を手がかりに、改定図面や追加図面を認識するはずですが、
// 問題はそれらは**編集者が微妙に書き換える**ことです。整理画面で編集者が書き換える
// たびに、既存のページがあるか**検索しなおす**必要があります」（2026-09-20）。
//
// ⚠ **先に配っておけない値**です。装置名称の候補は顧客ごとの一覧を先に配って JS で
// 絞っていますが（`machines`）、図面名称は顧客×段×装置の数だけあるので配れません。
// だから**打ち替えのたびに聞きます**。
//
// ⚠ **調べるだけで、1枚も作りません。** `ensureChildPage` は無ければ作るので使わず、
// `findChildByTitle` だけで辿ります——**問い合わせただけで空の顧客ページが増える**のは
// いちばん質の悪い副作用です。
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
	stage := strings.TrimSpace(q.Get("stage"))
	machine := cms.NormalizeNameForIngest(q.Get("machine"))
	name := cms.NormalizeNameForIngest(q.Get("name"))

	out := map[string]any{"success": true, "exists": false}
	if customer == "" || machine == "" || name == "" {
		json.NewEncoder(w).Encode(out) // まだ埋まっていない——「無い」と同じ扱い
		return
	}
	boxID, ok := CustomerBoxPageID()
	if !ok {
		json.NewEncoder(w).Encode(out)
		return
	}
	id := boxID
	for _, title := range []string{customer, stage, machine, name} {
		if title == "" {
			json.NewEncoder(w).Encode(out)
			return
		}
		next, found := findChildByTitle(id, title)
		if !found {
			json.NewEncoder(w).Encode(out)
			return
		}
		id = next
	}
	idInt, err := strconv.Atoi(id)
	if err != nil || !page.CanView(user, idInt) {
		json.NewEncoder(w).Encode(out) // 読めないものは「無い」
		return
	}
	out["exists"] = true
	out["page_id"] = id
	out["title"] = name
	// **既に載っている図面番号**を添えます——人が「改定か、別の図面か」を決める
	// ときの手掛かりです（同じ番号なら改定、違う番号なら別図面のことが多い。
	// ⚠ **決めるのは人**で、機械はここでも候補までです）。
	if body, err := cms.ReadPageBody(id); err == nil {
		out["drawing_nos"] = drawingNosOf(body)
	}
	json.NewEncoder(w).Encode(out)
}

// drawingNosOf は本文に載っている図面番号を並べます（図面ブロックごとに1つ）。
func drawingNosOf(body string) []string {
	out := []string{}
	for _, sec := range drawingSectionsOf(body) {
		if no := strings.TrimSpace(drawingNoOf(sec)); no != "" {
			out = append(out, no)
		}
	}
	return out
}

// suggestCustomer は顧客名の推奨値を返します。
//
// **読んだ名前ではなく、既にある取引先ページの題を第一にします**（2026-09-06
// ユーザー:「社名の揺れは、エイリアスの表かAIでなくせませんか？」）。揺れは
// 「機械が読んだ名前を人が打ち写す」ところで生まれるので、**打ち写す元を変えます**:
//
//	加工製品ページ → `受信元` タグ → 通信記録 → `差出人アドレス` → 取引先ページ → その題
//
// この鎖は**全部が完全一致**で、推測が1つも入りません。エイリアスの表もAIも
// 要らないのは、**同一性を名前で決めていない**からです。
//
// 引けなければ読めた名前（Geminiが図面から読んだ客先）へ戻ります——新しい顧客の
// 1通目はまだ取引先に居ないのが正常で、そのときは人が打ちます。
func suggestCustomer(user *auth.User, productPageID int, read string) string {
	if addr := senderAddressOf(productPageID); addr != "" {
		if title, ok := contacts.PartnerTitleForAddress(user, addr); ok {
			return title
		}
	}
	// **鎖が切れたら、名前で連絡帳を当たります**（2026-09-20 ユーザー:「株式会社や
	// 有限会社、（株）などを無くした社名が連絡帳にあれば、それを提案するような形で、
	// 編集者の承認を得てはどうでしょう？」）。
	//
	// 鎖が切れるのは珍しくありません——新しい客先の1通目・社内からの転送・
	// FAXだけの相手。そこで生の `株式会社南北…` へ落ちると、連絡帳に
	// `南北…` が居るのに**2枚目の会社ページができます**。
	//
	// ⚠ **これは提案で、決めるのは人**です。欄は編集できるコンボボックスで、
	// 読んだ名前も候補の一覧に並びます（`partners`）。候補が2つ以上に割れたときは
	// `OrgNameForPage` が黙って読んだ名前を返します——`株式会社あさひ` と
	// `有限会社あさひ` は**実在しうる別会社**なので、機械が選んではいけません。
	// 連絡帳にまだ居なければ**法人格を落とした形**を提案します（2026-09-21）。
	return contacts.OrgNameForPage(user, read)
}

// senderAddressOf は加工製品ページの由来（`受信元`）をたどり、通信記録の差出人アドレスを返します。
//
// **由来のタグを見ます**（親ではなく）——加工製品ページは整理で動きますが、`受信元` は
// 動きません。値は「ページID-添付ID」なので、ハイフンの前だけ使います。
func senderAddressOf(productPageID int) string {
	ref := strings.TrimSpace(cms.PageTagValue(database.DB, productPageID, SourceRefTag))
	if i := strings.Index(ref, "-"); i > 0 {
		ref = ref[:i]
	}
	srcID, err := strconv.Atoi(ref)
	if err != nil {
		return ""
	}
	var addr string
	database.DB.QueryRow(
		// **畳んだ値がアドレス**（生の値は `名前 <アドレス>`）。2026-09-13 に1人1タグへ。
		// 名前は**書き手の定数**を通します（2026-09-16）——下請けは通信記録の
		// 中身を直接読んでいるので、通信側で欄の名前が変わると**顧客名の推奨が
		// 黙って空になります**（エラーにはならない）。
		`SELECT COALESCE(norm_value, value) FROM page_tags
		  WHERE page_id = ? AND name = ? LIMIT 1`,
		srcID, comm.FromTag).Scan(&addr)
	return strings.TrimSpace(addr)
}

// partnerNames は「取引先」の下にある相手ページの題を並べます（読めるものだけ）。
//
// 整理の画面の入力補助です。**選ばせるのではなく、候補として見せる**だけ——
// 新しい顧客の1枚目はここに無いので、打てなくしてはいけません。
func partnerNames(user *auth.User) []string {
	out := []string{}
	for _, c := range visibleCustomerChildren(user) {
		out = append(out, c.Title)
	}
	sort.Strings(out)
	return out
}

// visibleCustomerChildren は `取引先` の直下（社名ページ）のうち、読めて題のあるものを
// ID順で返します（整理の候補と、連絡帳と未接続の相手の一覧が共有します）。
func visibleCustomerChildren(user *auth.User) []cms.ChildPage {
	boxID, ok := CustomerBoxPageID()
	if !ok {
		return nil
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return nil
	}
	kids, err := cms.ChildPages(database.DB, boxInt)
	if err != nil {
		return nil
	}
	var out []cms.ChildPage
	for _, k := range kids {
		if k.Title != "" && page.CanView(user, k.ID) {
			out = append(out, k)
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
// ⚠ **ただし「顧客の孫」だけでは広すぎます。** 社名ページの子は段だけではなく、
// `担当者`（窓口の人を集める箱）も並びます——そこの孫は**人の名前**なので、
// 絞らないと `小澤 美智子` が装置の候補に出ます（2026-09-14 に実データで発見）。
// **真ん中の世代は段の一覧（設定の `machine_stages`）に限ります**——段は閉じた
// 集合なので、表引きで断てます。`担当者` を名指しで除くやり方は採りません
// （箱が増えるたびに除外が増え、いつか漏れます）。
//
// **候補を出すだけで、合わせるのは人**です。完全一致でしか階層は繋がらないので
// （findChildByTitle）、揺れを機械が吸収すると別の装置が1つに潰れます。
func machineNames(user *auth.User) map[string][]string {
	out := map[string][]string{}
	boxID, ok := CustomerBoxPageID()
	if !ok {
		return out
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return out
	}
	// 段が1つも設定されていなければ、装置の置き場そのものが決まりません。
	stages := MachineStages()
	if len(stages) == 0 {
		return out
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(stages)), ",")
	args := []any{boxInt}
	for _, st := range stages {
		args = append(args, st)
	}
	// **3世代を1回のクエリで取ります**。行を読みながら別のクエリを投げると
	// `:memory:` DBでカーソルが接続を握ったままになり、**絞り込みが静かに全部落ちます**
	// （2026-09-03 に本番コードで踏んだ罠）。
	rows, err := database.DB.Query(`
		SELECT cust.id, COALESCE(cust.title, ''), mach.id, COALESCE(mach.title, '')
		  FROM pages cust
		  JOIN pages stage ON stage.parent_id = cust.id
		  JOIN pages mach  ON mach.parent_id  = stage.id
		 WHERE cust.parent_id = ? AND stage.title IN (`+ph+`)
		 ORDER BY cust.title ASC, mach.title ASC`, args...)
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
			Stage:       suggestStage(client, machine),
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
	// Merge は**行き先に同じ題のページがあったとき、どうするか**です（2026-09-20）。
	//
	//	""          … 未選択。**動かしません**（人が決めるまで通信箱に置いたまま）
	//	"revision"  … 改定として合流（いまの図面は旧版として子ページへ）
	//	"drawing"   … 二つ目の図面として追加（同じページに並べる・部品図と溶接図）
	//
	// ⚠ **既定を「改定」にしません。** 機械には区別できない（どちらも「同じ品物・
	// 違う図面番号」）ので、既定を置くと**見ないまま押した人がその既定に従います**。
	// 溶接図が黙って旧版になるのが、それまでの振る舞いでした。
	// 空欄は「まだ決められない」の意思表示、という整理の作法に揃えています。
	Merge string `json:"merge"`
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
		res := fileOneDrawing(user, row)
		// ⚠ **ここが「弊社品番を機械的に埋める」引き金です**（2026-09-21 ユーザー:
		// 「注文が入っている以上、近日中に製造製品ページが出来るはずです。**その
		// タイミングで検索して埋める**ことになると思います。できれば機械的に」）。
		//
		// **加工製品ページが置かれた瞬間**——つまり人が整理を押した直後だけに走ります。
		// 裏で回る仕事は作りません。⚠ **合流したときは合流先**を見ます（そのページが
		// 新しい図番を得ているので、そちらが受注行の相手です）。
		// 歯止めと照合の規則は [link_item.go] が正本です。
		if target := res.TargetID; res.Outcome == "moved" || res.Outcome == "revision" ||
			res.Outcome == "drawing" {
			if target == "" {
				target = res.PageID
			}
			if n := LinkOrdersToProduct(user, target); n > 0 {
				res.Message += "／受注の弊社品番を" + strconv.Itoa(n) + "行埋めました"
			}
		}
		results = append(results, res)
	}
	for _, o := range req.Orders {
		results = append(results, fileOneOrder(user, o))
	}
	cms.WriteJSON(w, map[string]any{"success": true, "results": results})
}

// fileOneDrawing は1枚の加工製品ページを行き先へ収めます。
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
	if !ValidMachineStage(stage) {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "段（" + strings.Join(MachineStages(), "・") + "）を選んでください"}
	}
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !canWritePage(user, idInt) {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "このページを動かす権限がありません"}
	}
	// **開いている人が居たら、その行は飛ばします**（2026-09-14）。整理は本文を
	// 読んで・変えて・書くので、誰かがエディタを開いていると**オートセーブと
	// 上書きし合います**。関門をハンドラではなく行ごとに置くのは、整理が
	// **複数のページへ書く**ためです（加工製品ページ・合流先）——1枚が編集中でも、
	// 残りは片付けられるほうがよい。
	if holder, open := editlock.Locks.EditorOpen(idInt); open {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "このページは編集中です（" + holder + "）。閉じてからもう一度お試しください"}
	}

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
	writeName := name
	if row.Merge == "drawing" {
		writeName = ""
	}
	if err := syncDrawingFields(user, pageID, customer, machine, writeName); err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "図面ブロックの値を直せません: " + err.Error()}
	}

	// **顧客名ページは「取引先」の下**です（2026-09-05 ユーザー決定）。アドレス帳が
	// 作る相手ページと**同じ場所・同じ1枚**——連絡先を見るページと部品を見るページを
	// 分けないため（EnsureCustomerBox の説明が正本）。
	boxID, err := EnsureCustomerBox(user)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "「" + CustomerBoxTitle + "」ページを用意できません: " + err.Error()}
	}
	customerID, err := ensureChildPage(user, boxID, customer)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "顧客名ページを用意できません: " + err.Error()}
	}
	// **2つの木を参照タグで結びます**（2026-09-16）。`取引先／社名` のページから
	// `連絡帳／組織` を指す `相手` のタグを1つ書きます。
	//
	// ⚠ **題だけで結んでいると、改名した日に切れます**——加工製品の階層のフォルダ名は
	// 人が直しますし、連絡帳の社名も直ります。**参照はページIDなので切れません**
	// （同じ理由で、加工製品ページの `受信元` もページIDです）。
	//
	// **人が選んだ社名で引きます**（機械が推した組織ではなく）——整理の画面は
	// 「機械が出して人が直す」場所なので、**打ち替えた結果が正**です。
	// 連絡帳にまだ居ない相手は結びません（新しい顧客の1通目がその形で、正常）。
	linkPartner(user, customerID, customer)
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

	// **行き先に同じ題のページがあるとき**——改定か、二つ目の図面か（2026-09-20）。
	//
	// ⚠ **それまでは黙って改定にしていました。** 図面番号が違えば改定、としていた
	// のですが、**部品図と溶接図も「図面番号が違う」**ので、片方が旧版として子ページへ
	// 押し込まれます（ユーザー:「品物としては一つです」）。機械には区別できないので、
	// **人が選びます**。画面は打ち替えのたびに `/api/filing-target` へ聞いて、
	// ページがあれば選択肢を出します。
	if existing, found := findChildByTitle(machineID, name); found && existing != pageID {
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

		switch row.Merge {
		case "drawing":
			// ⚠ **図面名称は書き戻しません**（上の `syncDrawingFields` は
			// `mergeDrawing` のとき名称を飛ばしています）。欄の値は**行き先を決める
			// ため**のもので、運ぶブロックの中身（溶接図自身の名前と番号）はそのまま。
			if err := mergeAsDrawing(user, pageID, existing); err != nil {
				return filingResult{PageID: pageID, Outcome: "skipped",
					Message: "二つ目の図面として追加できません: " + err.Error()}
			}
			auth.Audit(user.Username, "file-drawing.add", pageID+" -> "+existing)
			return filingResult{PageID: pageID, Outcome: "added", TargetID: existing,
				Message: customer + "／" + machine + "／" + name + " へ二つ目の図面として並べました"}

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
				Message: customer + "／" + machine + "／" + name + " の改定図面として合流しました"}

		default:
			// **未選択なら動かしません。** ⚠ どちらかを既定にすると、見ないまま
			// 押した人がその既定に従います——溶接図が黙って旧版になるのが、
			// それまでの振る舞いでした。
			return filingResult{PageID: pageID, Outcome: "needs_choice", TargetID: existing,
				Message: "「" + name + "」は既にあります。" +
					"改定図面か、同じ品物の二つ目の図面（部品図と溶接図など）かを選んでください"}
		}
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
		{ClientNameTag, customer},
		{MachineNameTag, machine},
		{DrawingNameTag, name},
	} {
		// ⚠ **空は「消したい」ではありません**（2026-09-20）。渡さなかった項目は
		// 触らない、という意味です——空で上書きすると、**二つ目の図面として追加する
		// とき、溶接図の図面名称が消えます**（呼ぶ側が名称だけ渡さない形にしている）。
		// 受注の `syncOrderClient` と同じ約束です。
		if strings.TrimSpace(f.value) == "" {
			continue
		}
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
	// 図面ブロックの**可変タグの `dl`** を探します（2026-09-18 に素の `<dl>` から移した
	// ——検索の口が読む表は `page_tags` のほうなので）。
	h2 := strings.Index(body, "<h2>図面</h2>")
	if h2 < 0 {
		return body
	}
	open := strings.Index(body[h2:], `<dl data-type="tags">`)
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
	return cms.FindChildByTitle(parentID, title)
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
	return page.FormatID(idInt)
}

// pageTitleOf は索引から題を引きます（引けなければIDをそのまま返す）。
func pageTitleOf(pageID string) string {
	idInt, err := strconv.Atoi(pageID)
	if err != nil {
		return pageID
	}
	if t := cms.PageTitleByID(idInt); t != "" {
		return t
	}
	return pageID
}

// htmlEscape は本文へ入れる前の逃がしです（サニタイザは安全の網で、
// エスケープの肩代わりはしません——サニタイズ後にHTMLを足す関数と同じ責任）。
func htmlEscape(s string) string { return stdhtml.EscapeString(s) }

// movePage は加工製品ページを行き先の下へ移し、題を図面名称に揃えます。
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

// linkPartner は加工製品の階層の社名ページから、連絡帳の組織ページへの参照タグを書きます。
//
// **何度呼んでも増えません**（既に在れば何もしない）。引けないときも黙って戻ります
// ——連絡帳にまだ居ない相手は普通にいるので、**結べないことは異常ではありません**。
//
// ⚠ **編集中のページには書きません**（`editlock.RefuseWhileEditing` と同じ理由ですが、
// ここは画面へ返す `http.ResponseWriter` を持たないので、**開いていたら黙って諦めます**
// ——整理そのものは続けたいからです。次に整理を押せば書かれます）。
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
	if strings.Contains(body, "<dt>"+contacts.ContactsRefTag+"</dt><dd>"+partnerID+"</dd>") {
		return
	}
	if idInt, err := strconv.Atoi(customerID); err == nil {
		if _, open := editlock.Locks.EditorOpen(idInt); open {
			return // 開いている人が居る。次の整理で書けばよい
		}
	}
	_ = cms.RewriteBody(customerID, user.Username, func(current string) string {
		if strings.Contains(current, "<dt>"+contacts.ContactsRefTag+"</dt><dd>"+partnerID+"</dd>") {
			return current
		}
		pair := "<dt>" + contacts.ContactsRefTag + "</dt><dd>" + partnerID + "</dd>"
		if at := cms.EndOfFirstTagList(current); at >= 0 {
			return current[:at] + pair + current[at:]
		}
		return cms.InsertAfterH1(current, `<dl data-type="tags">`+pair+`</dl>`)
	})
}
