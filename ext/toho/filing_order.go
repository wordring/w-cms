package toho

// ─────────────────────────────────────────────────────────────────────────
// 受注ページの整理——通信箱から「受注」の年月へ移す（2026-09-06）
//
// ユーザー:「整理の前に、部品ページや受注ページの置き場所を決めましょう」。
// 決まったのは **トップ直下の「受注」／年／月**（加工製品ページのように顧客の下へは
// 置かない）。受注は複数の装置にまたがるので、装置の木には収まりません。
//
// **置き場所を決める理由は、探しやすさだけではありません。** 未処理の一覧は
// 通信箱の**子孫を全部**拾うので（`isDescendantOf` は深さ無制限）、解析で生まれた
// 受注ページはそのまま作業待ちに並びます——メール1通が2行にも3行にもなる。
// 家を与えると、移した時点で一覧から自然に外れます。
//
// **移すのは人の操作**（同日ユーザー決定）。行き先は日付だけで決まるので直す欄は
// ありませんが、**いつ動かすかは人が決めます**——解析の直後に機械が動かすと、
// 判定を間違えた受注ページが黙って通信箱の外へ出てしまい、気づく機会が無くなります。
//
// 年月は、もとは**発注日**で決めていました（受け取った日ではなく）。2026-10-16 から、置き場は `記録／年／月／依頼` で、
// 年月は**その案件ページを作った日**です（`orderFolderDateOf`——作った時刻、読めなければ更新時刻。【考察】全体の設計 §4.2）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strconv"
	"strings"
	"time"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// OrderBoxTitle は受注ページの置き場（トップ直下）の名前です。
//
// **業務の言葉なので拡張が持ちます**——コアが持つのは「トップ直下に題で箱を
// 用意する」仕組みだけ（`cms.EnsureTopLevelBox`）。通信箱・テンプレート置き場と
// 同じく **h1（ページ名）が正**です。
const OrderBoxTitle = "案件"

// PurchaseOrderBoxTitle は**弊社の部材発注書**の置き場（トップ直下）の名前でした。⚠ 置き場は 2026-10-19 になくした
// （作業面はトップ直下の「製造」へ・臨時部材表も——temp_parts.go の purchaseOrderBoxID）。名前は試験と古い本文のために残す。
//
//
// ⚠ **2026-09-21 に決定を覆しました。** それまでは「**材料発注の控えは受注ページ**」
// （同日の決定・見るのは事務が受注ごとだから）でしたが、ユーザーの訂正で崩れました:
//
//	「弊社の発注書は、**受注明細表の単位とは無関係に、納期のグループなどから発行**
//	 されます。したがって、**受注ページに置くことは出来ず**、しかし、受注明細表の
//	 各行に**どの発注書で発注したかを記録しリンク**する必要があります」
//
// ⚠ **発注の単位が受注をまたぐ**なら、受注の下には物理的に置けません
// （1枚の部材発注書が2つの受注ページの子にはなれない）。形は受注と同じ
// **`記録／年／月／製造記録`**（2026-10-16 から。それまでは `部材発注／年／月`）で、⚠ **結びは参照**で作ります。
const PurchaseOrderBoxTitle = "部材発注"

// EstimateBoxTitle は**見積**の置き場（トップ直下）の名前でした（2026-10-01 利用者:「トップ直下に見積フォルダを作りましょう」）。
// ⚠ 置き場は 2026-10-19 になくした（未送付の見積書・見積書を作るのビューは、トップ直下の「製造」へ移した）。
// 置くのは**顧客へ出す見積書**——業者への部材見積依頼はトップ直下の「部材見積依頼」（2026-10-02 利用者:「見積依頼のフォルダはトップ直下の
// 見積依頼に変更します」・それまでは発注のくくりとしていた——利用者:「顧客への見積もりとは、別のくくり」）。
const EstimateBoxTitle = "見積"

// orderRow は整理の画面に出す受注ページ1枚ぶんです。
//
// 行き先（`Destination`）は**日付だけで決まる**ので直せません（2026-10-16 からは作った日・`orderFolderDateOf`）。⚠ **`ClientName`
// だけは直せます**（2026-09-20・3箇所を同じマナーに揃えた最後の1つ）。
type orderRow struct {
	PageID      string `json:"page_id"`
	Title       string `json:"title"`
	OrderNo     string `json:"order_no"`
	ClientName  string `json:"client_name"`
	OrderedAt   string `json:"ordered_at"`  // 発注日（読めたときだけ）
	Destination string `json:"destination"` // 「受注／2024年／09月」
}

// orderChildrenOf は、そのページの子のうち**発注書ブロックを持つもの**を集めます。
func orderChildrenOf(user *auth.User, parentIDInt int) ([]orderRow, error) {
	// **先に読み切ってから解釈します**（`cms.ChildPages`——行を読みながら別のクエリを
	// 投げるとカーソルが接続を握ったままになる。view_unhandled.go で踏んだ罠）。
	children, err := cms.ChildPages(database.DB, parentIDInt)
	if err != nil {
		return nil, err
	}
	out := []orderRow{}
	for _, c := range children {
		if !page.CanView(user, c.ID) {
			continue // 見せ分け（C案）——読めないものは黙って落ちる
		}
		// ⚠ **可変タグから読みます**（2026-09-18 にヘッダから移した）。
		tags, err := cms.TagsOfPage(database.DB, c.ID)
		if err != nil || !isOrderPageTags(tags) {
			continue // 受注ページではない（加工製品ページなど）
		}
		orderedAt := cms.FirstTag(tags, OrderedAtTag)
		when := orderFolderDateOf(c.ID)
		out = append(out, orderRow{
			PageID:      formatID(c.ID),
			Title:       c.Title,
			OrderNo:     cms.FirstTag(tags, OrderNoTag),
			ClientName:  suggestOrderClient(user, cms.FirstTag(tags, OrderClientTag)),
			OrderedAt:   strings.TrimSpace(orderedAt),
			Destination: recordPath(RecordRequestFolder, when),
		})
	}
	return out, nil
}

// suggestOrderClient は発注元の欄の初期値を決めます——**連絡帳に法人格違いの同じ
// 組織が居れば、その実物の題**（2026-09-20 ユーザー:「受注、加工製品、連絡帳を同じ
// マナーで提案の欄を作ってはどうでしょう？」）。
//
// ⚠ **加工製品の `suggestCustomer` とは効き目が違います。** あちらは `linkPartner` が
// **完全一致**で2つの木を結ぶので、揃えないと**黙って結ばれません**。こちらは
// `発注元` を引く読み手がまだ1人も居ないので（`fileOneOrder` が見るのは
// `発注書番号` と `発注日` だけ）、**いま直しても何も起きません**。
//
// それでも入口で揃えるのは、**効くのが横断検索が入ったとき**だからです
// （w-cms を作り始めた動機の1つ・[要件定義書.md] §4.4）。そのとき揃っていない
// データは、遡って直せません。
//
// ⚠ **アドレスの鎖は使いません。** 加工製品は `受信元` から通信記録の差出人まで
// たどれますが（推測ゼロの完全一致）、受注ページにその鎖があるかは未確認です。
// **確かめずに鎖を足すと、間違った相手を推して人がそのまま押します**——名前だけで
// 当たる今の形なら、外れても読んだ名前のまま残ります。
func suggestOrderClient(user *auth.User, read string) string {
	if strings.TrimSpace(read) == "" {
		return "" // **空は埋めません**——読めなかったことを人に見せる
	}
	return contacts.OrgNameForPage(user, read)
}

// orderFolderDateOf は受注ページの年月を決める日付——**受注ページを作った日**を返します（2026-10-16）。
//
// それまでは発注日が第一でした（「受け取った日で並べると、月末に届いた前月ぶんが翌月に混ざります」）。
// 利用者:「その文書を作成した日のフォルダで良いと思います。（つまり、一つのフォルダにたくさん入ると見づらいから
// 分けるだけです）」——月に意味を持たせないので、発注日を読む必要がなくなりました（【考察】全体の設計 §4.2）。
// 作った日が読めなければ更新時刻、それも読めなければ現在時刻（止めない）。
func orderFolderDateOf(pageIDInt int) time.Time {
	var created, updated string
	database.DB.QueryRow(`SELECT COALESCE(created_at, ''), COALESCE(updated_at, '') FROM pages WHERE id = ?`,
		pageIDInt).Scan(&created, &updated)
	for _, v := range []string{created, updated} {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(v)); err == nil {
			return t.In(time.Local)
		}
	}
	return time.Now()
}

// orderRequest は「実行」で送られてくる受注ページ1行です。
//
// ⚠ **もとはIDの文字列だけでした**（2026-09-20 に変えた）。直す欄が無かったので
// 「押した」という事実だけを送っていましたが、`発注元` が直せるようになったので
// 値を運びます。画面（`assets/app.js` の `runFiling`）も同じ形で送ります。
type orderRequest struct {
	PageID string `json:"page_id"`
	// Client は人が確認した発注元です。**空なら本文に触りません**——欄を空にした
	// ことを「消したい」とは読みません（`replaceFirstFieldValue` が値を消してしまう）。
	Client string `json:"client"`
}

// fileOneOrder は受注ページ1枚を「記録／年／月／依頼」へ収めます（2026-10-16 から。それまでは「受注／年／月」）。
//
// **題は変えません**——加工製品ページは図面名称に揃えますが（人が打ち替えた値が正）、
// 受注の題は解析が発注書番号から作ったものです。⚠ **`発注元` だけは書き戻します**
// （2026-09-20）。
func fileOneOrder(user *auth.User, row orderRequest) filingResult {
	pageID, ok := page.NormalizeID(row.PageID)
	if !ok {
		return filingResult{PageID: row.PageID, Outcome: "skipped", Message: "ページIDが不正です"}
	}
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !canWritePage(user, idInt) {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "このページを動かす権限がありません"}
	}
	// **受注ページ以外を動かさない。** 画面から送られた ID をそのまま信じると、
	// 加工製品ページや通信記録まで受注の箱へ入れられます。
	// ⚠ 見るのは `発注書番号` か `発注元` のタグです（2026-09-18 にヘッダから移した・09-30 に `発注元` も——isOrderPageTags）。
	tags, err := cms.TagsOfPage(database.DB, idInt)
	if err != nil || !isOrderPageTags(tags) {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "案件ページではありません"}
	}
	// **開いている人が居たら、その行は飛ばします**——下で本文を読んで・変えて・書くので、
	// エディタが開いているとオートセーブと上書きし合います（`fileOneDrawing` と同じ作法）。
	// （2026-10-12 から、飛ばすのは道具〔`/api/lock`〕が握っているときだけ。人の保存とは `cms.RewriteBody` の錠で順に並ぶ。）
	if holder, open := editlock.Locks.EditorOpen(idInt); open {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "このページは編集中です（" + holder + "）。閉じてからもう一度お試しください"}
	}

	// 置き場は `記録／年／月／依頼`（2026-10-16・record_box.go）。**月は受注ページを作った日**です。
	when := orderFolderDateOf(idInt)
	monthID, err := recordFolderOr("", user.Username, RecordRequestFolder, when)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: err.Error()}
	}
	if _, _, err := cms.SetPageParent(user, pageID, monthID); err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "移動できません: " + err.Error()}
	}
	// **人が確認した発注元を本文へ書き戻します**（2026-09-20）。⚠ **移してから**書きます
	// ——移動は行き先だけで決まるので、書き戻しに失敗しても収まったことは変わりません。
	// 逆にすると、書けなかったときに移すかどうかで迷います。
	if err := syncOrderClient(user, pageID, row.Client); err != nil {
		// **収まったことは事実なので、失敗にしません**（`BumpUpdatedAt` と同じ判断）。
		auth.Audit(user.Username, "file-order.client-failed", pageID+": "+err.Error())
	}
	auth.Audit(user.Username, "file-order.move", pageID+" -> "+monthID)
	msg := recordPath(RecordRequestFolder, when) + " へ収めました"
	// ⚠ **ここでは受注の行を結びません**（2026-10-01 に外した）。結ぶのは受注ページを作ったとき（🤖解析）と、
	// **受注フォルダを開いたとき**（backlog.go の `linkBacklogRows`）の2つだけ——利用者:「単純化しましょう」
	// 「受注フォルダを中心に作業するので問題ないはずです」。収めた受注は、次に受注フォルダを開いたときに結ばれる。
	return filingResult{PageID: pageID, Outcome: "moved", Message: msg}
}

// syncOrderClient は人が確認した発注元を、受注ページの可変タグへ書き戻します。
//
// **索引のためです**——`page_tags` は本文から作られるので、画面で直しても本文が
// 元のままなら、揃えた意味がありません（`syncDrawingFields` と同じ理由）。
//
// ⚠ **足しません。差し替えるだけです。** `発注元` の行は解析が必ず書くので
// （`buildOrderPageHTML`）、無いときは**この形のページではない**——そこへ勝手に
// 行を挿すと、人が消した行を機械が戻すことになります。図面ブロックが足すのは、
// 解析が読めなかった項目を書かない作りだからで、事情が違います。
//
// **値に印が付いていたら触りません**（`replaceFirstFieldValue` が `[^<]*` で見る
// ——人がリンクや強調を書いたら、機械は踏み潰さない）。
func syncOrderClient(user *auth.User, pageID, client string) error {
	client = cms.NormalizeNameForIngest(client)
	if client == "" {
		return nil // **空欄は「消したい」ではありません**（読めなかった・触らなかった）
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return err
	}
	// **変わらないなら書きません**——版と更新日時を無駄に進めない。
	// ⚠ 書くときは、錠の中で読み直した本文から組み直します（2026-10-19 夜・rebuildBody）。
	_, _, err = rebuildBody(pageID, user.Username, body, func(cur string) (string, error) {
		return replaceFirstFieldValue(cur, OrderClientTag, client), nil
	})
	return err
}
