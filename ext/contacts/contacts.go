package contacts

// ─────────────────────────────────────────────────────────────────────────
// アドレス帳——メールから相手を拾い、ページにする（2026-09-05）
//
// ユーザー:「アドレス帳のようなものを作って、メールから人物や電話番号、
// メールアドレスを収集しましょう」。
//
// **材料はもう索引に入っています。** 取り込みが `差出人アドレス`・`宛先アドレス`・
// `CCアドレス`・`返信先アドレス` を書いているので、新しく集める仕掛けは要りません
// ——足りないのは「**まだページになっていないアドレス**」を並べて、人が確定する口だけです。
// 未処理の受信とまったく同じ形（見つかっているものを並べ、決めるのは人）。
//
// ── 実データで分かった3つのこと（60通・11アドレス）──
//
//  1. **アドレスは揺れない。** 11アドレス中、表示名が2通りになったものはゼロ。
//     突き合わせの鍵はアドレスで足り、名寄せは要らない（機械が発行した文字列だから）。
//  2. **表示名は「人」と「会社」が混ざる**（`山田 太郎` / `株式会社緑川製作所` /
//     `レーザマックス大阪支店`）。メールは「誰か」を1つしか教えないので、
//     人と会社を分けるのは人の仕事。
//  3. **会社はドメインでまとまる——が、例外がある。** `example-sports.co.jp` に
//     4人。しかし自社の工場長だけ別プロバイダのアドレスを使っていた。
//     **ドメインは手掛かりであって決定ではない**（今日から何度も出てきた形）。
//
// ── 決めたこと ──
//
// **取引の相手は1種類。役割はタグで表します**（`取引：顧客` / `仕入先` / `自社`）。
// ユーザー:「顧客であり、仕入れ先である場合もあります」——置き場所で分けると、
// そのとき1枚に保てません。同じページに両方書けます（タグは同じ名前を何度でも置ける）。
//
// **相手ページは「取引先」の下**です（2026-09-05 ユーザー:「トップページの直接の子が
// 個人名や社名はちょっと具合が悪い」）。もとは 2026-09-03 の決定でトップ直下でしたが、
// 相手が増えるほどトップが名簿になってしまうため、**箱を1枚かませました**。
//
// **箱は1枚だけ**です。`顧客` `仕入先` `自社` で箱を分けないのは、ユーザーの
// 「顧客であり、仕入れ先である場合もあります」がそのまま効くため——置き場所で
// 分けると、その相手を**どちらか一方にしか置けません**。役割はタグの仕事です。
//
// **顧客名／装置名称／図面名称の「顧客名」も同じページ**です（同日ユーザー決定）。
// 会社ページを別に作ると「株式会社南北スポーツ機械」が2枚になり、
// 連絡先を見るページと部品を見るページが分かれてしまいます。
//
// **個人のお客様なら、そのページが本人**です。人物ページを別に作りません
// ——本人の名前のページを2枚重ねる意味がないためです。
//
// **窓口の人は `社名／担当者／名前`**（2026-09-05 ユーザー決定）。社名ページの子には
// 既に**装置名称**が並ぶので、人を直接ぶら下げると**人と装置が兄弟になり**、装置が
// 増えるほど人が埋もれます。`担当者` を1枚かませて分けます:
//
//	取引先／株式会社南北スポーツ機械／担当者／山田 太郎
//	取引先／株式会社南北スポーツ機械／標準2輪／取付ベース
//
// **人のページは要るときだけ**です。連絡先そのもの（メールアドレス・電話番号）は
// 社名ページにタグとして何個でも置けるので、**その人に添付や記録を紐づけたく
// なったとき**に初めてページにします。**作る操作は未実装**。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"fmt"
	stdhtml "html"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// init はアドレス帳を**フック経由で**コアへ差し込みます。
//
// **まだ `internal/cms` の中に居ますが、外から差せる形にしました**（2026-09-15）。
// 行き先は `ext/contacts` と決まっており（§5b 案B）、移設の本体はファイルを動かす
// ことではなく**依存の向きを裏返すこと**だからです。ここが済んでいれば、移動は
// `package` 行と `cms.` の前置きだけになります。
//
// 裏返したのは2本です:
//
//   - **計算ビュー**——`view_render.go` の表に `unknown-contacts` が名指しで
//     書かれていました。`cms.RegisterView` で自分から名乗ります。
//   - **アドレス→ページの解決**——`ref_render.go` が `ContactPageForAddress` を
//     直接呼んでいました。`cms.RegisterContactResolver` で解決係を預けます。
//
// 語彙（`unknown-contacts` の宣言）も `vocab.go` の表から引き取ります。
func init() {
	cms.RegisterVocab(contactsVocab...)
	cms.RegisterView("unknown-contacts", contactsViewHTML)
	// 描画は匿名でも通る経路なので `user` は nil——認可は解決の中で見ます。
	cms.RegisterContactResolver(func(addr string) (string, string, bool) {
		return ContactPageForAddress(nil, addr)
	})
}

// contactsVocab はアドレス帳が持ち込む語彙です（いまは作業面1つ）。
//
// **`vocab.go` から引き取りました**（2026-09-15）。素の w-cms が `未登録の連絡先`
// を知っている必要はありません——業務側の語です。
var contactsVocab = []cms.VocabDef{{
	// ユーザー:「アドレス帳のようなものを作って、メールから人物や電話番号、
	// メールアドレスを収集しましょう」（2026-09-05）。**集める仕掛けは要りません**
	// ——取り込みが既にアドレスをタグへ書いているので、足りないのは
	// 「まだページになっていないもの」を並べて人が確定する口だけです。
	Type:        "unknown-contacts",
	DisplayName: "未登録の連絡先",
	Category:    "ビュー",
	Icon:        "📇",
	Element:     "section",
	View:        true,
}}

// ContactPersonBoxTitle は社名ページの下の、窓口の人を集める箱の名前です
// （`取引先／社名／担当者／名前`）。**装置名称と人を兄弟にしない**ための1枚。
// 語を1箇所に閉じておくのは「担当」と「担当者」が混ざるのを防ぐためです。
const ContactPersonBoxTitle = "担当者"

// PartnerBoxTitle は相手ページの置き場（トップ直下）の名前です。通信箱
// （MailBoxTitle）・テンプレート置き場と同じく **h1（ページ名）が正**。
const PartnerBoxTitle = "取引先"

// PartnerBoxPageID はトップ直下の取引先ページを返します（無ければ ok=false）。
func PartnerBoxPageID() (string, bool) { return cms.TopLevelPageByTitle(PartnerBoxTitle) }

// EnsurePartnerBox は取引先ページを返し、**無ければ作ります**。
//
// **通信箱と違って自動で作ります。** 通信箱は「そこへ落とすと取り込みが走る」という
// 機能の入口なので、人が意図して置くものです。取引先はただの置き場——無いからと
// 登録ボタンを行き止まりにする理由がありません。名前を変えられたら次の登録で
// また作られますが、**取り込みのように静かに壊れることはありません**。
//
// 権限は呼ぶ側が見ます（作るときはトップへの書き込み、あるときは箱への書き込み）。
func EnsurePartnerBox(user *auth.User) (string, error) {
	if id, ok := PartnerBoxPageID(); ok {
		return id, nil
	}
	return cms.CreateChildPage(cms.TopPageID, user.Username, partnerBoxBody())
}

// partnerBoxBody は取引先ページの初期の本文です。
//
// **「未登録の連絡先」の作業面を最初から載せます**（2026-09-11）。ここを空の見出し
// だけで作っていたために、**アドレス帳の作業面がどこにも存在しませんでした**
// ——誰も一覧を見たことがないまま実メール100通が過ぎ、11ドメインのうち登録済みは
// 1件だけ（しかもそれは整理が作った側で、連絡先を持っていなかった）。
//
// 通信箱は**人が意図して置くページ**なので、作業面も人が入れます。取引先は
// **機械が作る**ので、**行き止まりのページを作らない責任はこちらにあります**。
func partnerBoxBody() string {
	return "<h1>" + stdhtml.EscapeString(PartnerBoxTitle) + "</h1>" +
		"<p>取引の相手（会社・個人）を集めます。製造部品の階層もこの下です" +
		"（社名／段／装置名称／図面名称）。</p>" +
		`<section data-type="unknown-contacts"></section>`
}

// EmailTag は連絡先のメールアドレスです。**1ページに何個でも置けます**
// （会社の窓口が複数、同じ人が複数のアドレスを持つ、どちらも起きる）。
const EmailTag = "メールアドレス"

// RelationTag は取引の役割です（値は RelationCustomer / RelationSupplier / RelationSelf）。
// **同じページに複数書けます**——顧客でも仕入先でもある相手が実際にいるためです。
const RelationTag = "取引"

// 取引の値。**表引きで閉じます**——`仕入先` と `仕入れ先` が混ざると絞り込みが
// 静かに取りこぼします。
const (
	RelationCustomer = "顧客"
	RelationSupplier = "仕入先"
	RelationSelf     = "自社"
)

// Relations は選択肢を並び順つきで返します（画面が使います）。
func Relations() []string { return []string{RelationCustomer, RelationSupplier, RelationSelf} }

// PartnerTitleForAddress は差出人アドレスから、取引先ページの**題**を引きます。
//
// **社名の揺れを消すための鍵**です（2026-09-06 ユーザー:「社名の揺れは、エイリアスの
// 表かAIでなくせませんか？」）。実データの初回で「南北スポーツ機械」と
// 「株式会社南北スポーツ機械」が同じ会社で2枚になりました——機械が読んだ名前を
// 人が打ち写したためです。**推測をやめて、既にあるページを名指しで引きます。**
//
// 引き方は2段:
//
//  1. **アドレスの完全一致**。ドメインより先に見ます——実データに「自社の工場長だけ
//     別プロバイダのアドレス」という例があり、ドメインを先に見ると取り違えます。
//  2. **ドメインの一致**。会社の窓口は増えるので、新しい人からのメールでも当たります。
//
// **`取引：自社` のページは返しません。** 顧客名の推奨値として自社が出ることは
// ありえず、出ると人がそのまま押してしまいます。
//
// 見つからなければ ok=false——**呼ぶ側は読めた名前へ戻ります**。新しい顧客の1通目は
// ここに無いのが正常で、そのときは人が打ちます。
// ContactPageForAddress は、そのアドレスが載っている**連絡先ページ**を返します。
//
// 返すのは**アドレスが書いてあるページそのもの**（担当者ページがあればその人、
// 無ければ社名ページ）です。`PartnerTitleForAddress` が「会社の名前」を答えるのに対し、
// こちらは「この人のページ」を答えます——2026-09-13 ユーザー:「表示するときに
// メールアドレスからアドレス帳のページへリンクがあると良いと思います」。
//
// **完全一致だけ**です。ドメインが同じでも別人なので、飛び先は決められません。
//
// **`user` が nil なら認可を見ません**——描画から呼ぶときの形です。
// リンクを作るだけで中身は見せないので、`pageExists`（参照リンクの存在確認）と
// 同じ規律にしています: 踏んだ先で通常の関門が判定します。
// 存在の秘匿は匿名にだけ意味があり、そこは本文の公開設定が受け持ちます。
func ContactPageForAddress(user *auth.User, addr string) (pageID, title string, ok bool) {
	mail := normalizeEmail(addr)
	if mail == "" {
		return "", "", false
	}
	rows, err := database.DB.Query(
		`SELECT page_id, value FROM page_tags WHERE name = ?`, EmailTag)
	if err != nil {
		return "", "", false
	}
	type hit struct {
		id    int
		value string
	}
	// **先に読み切ってから絞ります**（行を読みながら別のクエリを投げない）。
	var found []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.value); err != nil {
			rows.Close()
			return "", "", false
		}
		found = append(found, h)
	}
	rows.Close()

	for _, h := range found {
		if normalizeEmail(h.value) != mail {
			continue
		}
		if user != nil && !page.CanView(user, h.id) {
			continue
		}
		if _, _, inPartner := PartnerOfPage(h.id); !inPartner {
			continue // 取引先の外に書かれたアドレスは連絡先ではない
		}
		return fmt.Sprintf("%06d", h.id), cms.PageTitleByID(h.id), true
	}
	return "", "", false
}

func PartnerTitleForAddress(user *auth.User, addr string) (string, bool) {
	mail := normalizeEmail(addr)
	if mail == "" {
		return "", false
	}
	domain := mail[strings.LastIndex(mail, "@"):] // "@example.co.jp"

	// **取引先の木ぜんたいから引きます**（2026-09-13）。連絡先を人ごとのページへ
	// 分けたので、アドジスが載っているのは社名ページとは限りません
	// （`取引先／社名／担当者／氏名`）。見つけた先を `PartnerOfPage` で**会社へ丸めて**
	// から返します——整理が欲しいのは会社の名前だからです。
	rows, err := database.DB.Query(
		`SELECT v.page_id, v.value FROM page_tags v WHERE v.name = ?`, EmailTag)
	if err != nil {
		return "", false
	}
	type hit struct {
		id    int
		value string
	}
	// **先に読み切ってから絞ります**（行を読みながら別のクエリを投げない）。
	var found []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.value); err != nil {
			rows.Close()
			return "", false
		}
		found = append(found, h)
	}
	rows.Close()

	byDomain := ""
	for _, h := range found {
		v := normalizeEmail(h.value)
		if v == "" || !page.CanView(user, h.id) {
			continue
		}
		// 載っているのが担当者ページでも、**答えるのは会社の名前**です。
		companyID, title, ok := PartnerOfPage(h.id)
		if !ok || title == "" {
			continue // 取引先の外のページに書かれたアドレスは相手ではない
		}
		// `取引：自社` は推奨値にしません（社名ページに付きます）。
		if isSelfPartner(companyID) {
			continue
		}
		if v == mail {
			return title, true // 完全一致が最優先
		}
		if byDomain == "" && strings.HasSuffix(v, domain) {
			byDomain = title
		}
	}
	if byDomain != "" {
		return byDomain, true
	}
	return "", false
}

// isSelfPartner はそのページが `取引：自社` かを返します。
func isSelfPartner(pageIDInt int) bool {
	var n int
	database.DB.QueryRow(
		`SELECT COUNT(*) FROM page_tags WHERE page_id = ? AND name = ? AND value = ?`,
		pageIDInt, RelationTag, RelationSelf).Scan(&n)
	return n > 0
}

// AddContactAddresses は既存の相手ページへ `メールアドレス` のタグを足します。
//
// **2つ目のドメインのため**です（2026-09-06）。同じ会社が別のドメインからも
// メールを送ってくると、アドレス帳には新しい行として現れます。押すたびに新しい
// ページができると、**会社ページが2枚**になり、社名の揺れを消した意味が無くなります。
//
// 既に載っているアドレスは足しません——二度押しでタグが並ぶのを防ぐだけの判定なので、
// `MarkHandled` と同じく**文字列で見ます**（取りこぼしても害は同じタグが2つ）。
func AddContactAddresses(pageID, author string, addrs []string) (int, error) {
	added := 0
	err := cms.RewriteBody(pageID, author, func(current string) string {
		added = 0 // 呼び直されても数が増えないように
		var pairs strings.Builder
		for _, a := range addrs {
			dd := `<dd>` + stdhtml.EscapeString(a) + `</dd>`
			if strings.Contains(current, dd) {
				continue
			}
			pairs.WriteString(`<dt>` + stdhtml.EscapeString(EmailTag) + `</dt>` + dd)
			added++
		}
		if pairs.Len() == 0 {
			return current
		}
		if at := cms.EndOfFirstTagList(current); at >= 0 {
			return current[:at] + pairs.String() + current[at:]
		}
		return cms.InsertAfterH1(current, `<dl data-type="tags">`+pairs.String()+`</dl>`)
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

// partnerTitleOf は索引からページの題を引きます（引けなければ空）。
func partnerTitleOf(pageIDInt int) string {
	var t string
	database.DB.QueryRow(`SELECT COALESCE(title, '') FROM pages WHERE id = ?`, pageIDInt).Scan(&t)
	return t
}

// isPartnerPage はそのページが「取引先」の直下にあるかを返します。
//
// **足す先を箱の中に限ります。** 画面から来たIDをそのまま信じると、通信記録や
// 図面ページに `メールアドレス` のタグが付き、ドメインの逆引きが別物を拾います。
func isPartnerPage(pageIDInt int) bool {
	boxID, ok := PartnerBoxPageID()
	if !ok {
		return false
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return false
	}
	var parent int
	if err := database.DB.QueryRow(
		`SELECT COALESCE(parent_id, 0) FROM pages WHERE id = ?`, pageIDInt).Scan(&parent); err != nil {
		return false
	}
	return parent == boxInt
}

// PartnerOfPage は、そのページが属する**相手（社名ページ）**を返します。
//
// 担当者ページ（`取引先／社名／担当者／氏名`）や、その下のページから
// **会社へ戻る**ための道です。連絡先を人ごとのページに分けた結果、
// 「このアドレスは誰の会社のものか」を答えるのに祖先を辿る必要が出ました
// （2026-09-13 ユーザー:「連絡先にはメールアドレス、電話番号、名前など様々なタグが
// 必要なので、ページに分割する必要があります」）。
//
// 社名ページ自身を渡せばそれ自身が返ります。取引先の外なら ok=false。
// 壊れたデータで無限に辿らないよう回数に上限を置きます（`isDescendantOf` と同じ用心）。
func PartnerOfPage(pageIDInt int) (id int, title string, ok bool) {
	boxID, found := PartnerBoxPageID()
	if !found {
		return 0, "", false
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return 0, "", false
	}
	cur := pageIDInt
	for i := 0; i < 100; i++ {
		var parent int
		var t string
		if err := database.DB.QueryRow(
			`SELECT COALESCE(parent_id, 0), COALESCE(title, '') FROM pages WHERE id = ?`,
			cur).Scan(&parent, &t); err != nil {
			return 0, "", false
		}
		if parent == boxInt {
			return cur, t, true
		}
		if parent == 0 {
			return 0, "", false
		}
		cur = parent
	}
	return 0, "", false
}

// EnsureContactPerson は `取引先／社名／担当者／氏名` のページを返し、無ければ作ります。
//
// **なぜページに分けるのか**（2026-09-13 ユーザー決定）。連絡先は
// メールアドレス・電話番号・氏名・役職…と**複数のタグが1人にぶら下がります**。
// 社名ページにタグを平らに積むと、6つ並んだ `メールアドレス` が**誰のものか
// 分からなくなり**（実データでそうなりました）、電話番号を足すこともできません。
// **まとめる器が要る**——w-cms の器はページです。
//
// **会社の口は社名ページのまま**です（`order@…`・`info@…` のように人ではないもの）。
// 人だと分かったものだけを分けます——分からないものを人のページにすると、
// 存在しない担当者が名簿に並びます。
func EnsureContactPerson(user *auth.User, companyID, name string) (string, error) {
	name = cms.NormalizeNameForIngest(name)
	if name == "" {
		return "", errors.New("担当者の名前が空です")
	}
	boxID, err := ensureChildByTitle(user, companyID, ContactPersonBoxTitle)
	if err != nil {
		return "", err
	}
	return ensureChildByTitle(user, boxID, name)
}

// ensureChildByTitle は題の一致する子を返し、無ければ作ります。
//
// **完全一致だけ**です（`findChildByTitle` と同じ規律）——揺れを機械が吸収すると
// 別人が1人に潰れます。
func ensureChildByTitle(user *auth.User, parentID, title string) (string, error) {
	parentInt, err := strconv.Atoi(parentID)
	if err != nil {
		return "", err
	}
	var id int
	err = database.DB.QueryRow(
		`SELECT id FROM pages WHERE parent_id = ? AND title = ? ORDER BY id ASC LIMIT 1`,
		parentInt, title).Scan(&id)
	if err == nil {
		return fmt.Sprintf("%06d", id), nil
	}
	if !page.GetPerms(parentInt).CanWrite(user) {
		return "", errors.New("親ページへ書き込む権限がありません")
	}
	return cms.CreateChildPage(parentID, user.Username,
		"<h1>"+stdhtml.EscapeString(title)+"</h1><p><br/></p>")
}

// PartnerRef は既にある相手ページ1枚（画面の選択肢に使います）。
type PartnerRef struct {
	ID    string
	Title string
}

// existingPartners は「取引先」の下にある相手ページを題の順で並べます（読めるものだけ）。
func existingPartners(user *auth.User) []PartnerRef {
	boxID, ok := PartnerBoxPageID()
	if !ok {
		return nil
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return nil
	}
	rows, err := database.DB.Query(
		`SELECT id, COALESCE(title, '') FROM pages WHERE parent_id = ? ORDER BY title ASC`, boxInt)
	if err != nil {
		return nil
	}
	type hit struct {
		id    int
		title string
	}
	// **先に読み切ってから絞ります**（page.CanView が別のクエリを投げるため）。
	var found []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.title); err != nil {
			rows.Close()
			return nil
		}
		found = append(found, h)
	}
	rows.Close()

	var out []PartnerRef
	for _, h := range found {
		if h.title == "" || !page.CanView(user, h.id) {
			continue
		}
		out = append(out, PartnerRef{ID: fmt.Sprintf("%0*d", page.IDLength, h.id), Title: h.title})
	}
	return out
}
