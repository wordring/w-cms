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
// **取引の相手は1種類。箱も1枚**です。ユーザー:「顧客であり、仕入れ先である場合も
// あります」——置き場所で分けると、そのとき1枚に保てません。
// ⚠ **役割のタグ（`取引：顧客` / `仕入先` / `自社`）は 2026-09-17 に全廃しました**
// （下の `PersonalOrgTitle` の手前に経緯）。相手はただの相手です。
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
// **コアはアドレス帳を知りません**（2026-09-15 に `internal/cms` から移設・§5b 案B）。
// 移設の本体はファイルを動かすことではなく**依存の向きを裏返すこと**で、
// それが済んだあとの移動は `package` 行と `cms.` の前置きだけでした。
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
	cms.RegisterView(ContactsViewType, contactsViewHTML)
	// 描画は匿名でも通る経路なので `user` は nil——認可は解決の中で見ます。
	cms.RegisterContactResolver(func(addr string) (string, string, bool) {
		return ContactPageForAddress(nil, addr)
	})

	// **連絡帳は管理画面のボタンで作れます**（2026-09-16）。
	//
	// ⚠ **ここが行き止まりでした。** この箱を作るのは `EnsureContactsBox` で、
	// その呼び手は**連絡先の登録の口と整理の2つだけ**。ところが登録の画面
	// （未登録の連絡先）は**この箱のページの上に載っている**ので、
	// **登録しないと作業面が出ず、作業面が無いと登録できない**——2026-09-16 に
	// データを一掃したとき、実際にそうなりました（手で1枚作って抜けた）。
	cms.RegisterRequiredPage(cms.RequiredPage{
		Title:     ContactsBoxTitle,
		Extension: "comm/contacts",
		Why:       "取引の相手（会社・個人）を集める箱です。木は「組織／人」の2段で、メールから拾った「未登録の連絡先」の作業面がこのページに出ます。",
		Body:      contactsBoxBody,
	})
}

// ContactsViewType は「未登録の連絡先」の形式名です。
//
// **3か所（語彙の宣言・描画の登録・取引先ページの本文）が同じ文字で結ばれます**
// ——1つだけ書き換えると、ビューは登録されているのに本文が別の名前を名乗る形になり、
// **枠だけが出て中身が空**になります（通信の `UnhandledViewType` と同じ扱い・2026-09-16）。
const ContactsViewType = "unknown-contacts"

// contactsVocab はアドレス帳が持ち込む語彙です（いまは作業面1つ）。
//
// **`vocab.go` から引き取りました**（2026-09-15）。素の w-cms が `未登録の連絡先`
// を知っている必要はありません——業務側の語です。
var contactsVocab = []cms.VocabDef{{
	// ユーザー:「アドレス帳のようなものを作って、メールから人物や電話番号、
	// メールアドレスを収集しましょう」（2026-09-05）。**集める仕掛けは要りません**
	// ——取り込みが既にアドレスをタグへ書いているので、足りないのは
	// 「まだページになっていないもの」を並べて人が確定する口だけです。
	Type:        ContactsViewType,
	DisplayName: "未登録の連絡先",
	Category:    "ビュー",
	Icon:        "📇",
	Element:     "section",
	View:        true,
}}

// ContactsBoxTitle はアドレス帳の置き場（トップ直下）の名前です。通信箱
// （MailBoxTitle）・テンプレート置き場と同じく **h1（ページ名）が正**。
//
// **2026-09-16 に `取引先` から `連絡帳` へ改めました**（ユーザー決定）。木は
// **組織／人**の2段です:
//
//	連絡帳／南北スポーツ機械／山田 太郎     ← 会社とその窓口
//	連絡帳／個人／山田太郎                        ← 個人のお客様
//
// **個人のお客様にも組織の段を置きます**（ユーザー:「個人という名前の組織ページの
// 下に個人名ページが来るのではないでしょうか」）。木の形が常に「組織／人」に
// 揃うので、読む側が場合分けを持たずに済みます。
//
// ⚠ **`取引先` は下請け業務（`ext/subcon`）の持ち物になりました**——あちらは
// 加工製品の階層の根（`取引先／社名／段／装置名称／図面名称`）です。**別の木**です。
const ContactsBoxTitle = "連絡帳"

// ContactsRefTag は「このページは連絡帳のここにいる」を指す参照タグの名前です
// （`取引先／社名` → `連絡帳／組織`。値は組織ページのID）。
//
// **2026-09-17 に `相手` から改めました**（ユーザー:「取引先／南北スポーツ機械に
// 『相手』というタグがあるのですが、『連絡帳』のほうが分かりやすいのでは？」）。
// `相手` は**2つの仕事を兼ねていました**——通信記録（電話・メモ）の「誰と話したか」
// （`comm.CounterpartTag`・そちらは正しい日本語なのでそのまま）と、この参照です。
// ⚠ **取引先のページ自身が相手**なので、そこに `相手：南北スポーツ機械` と書くと
// 「自分は自分です」になっていました。`連絡帳：…` なら**指す先が名前に出ます**。
//
// ⚠ **`連絡先` は使えません**——あちらは「相手を言い当てる手掛かり」（`メールアドレス`）に
// 予約済みで、実体に付けると「連絡先の無い連絡先」という書けない文になります（§0）。
//
// ⚠ **`ContactsBoxTitle` から導きません。** 箱の題は人が変えられますが、**タグの名前は
// 本文に保存済みのデータ**です。導くと、箱を改名した日に既存のタグが静かに引けなくなります。
const ContactsRefTag = "連絡帳"

// ~~ContactPersonBoxTitle~~ は 2026-09-16 に**やめました**（`連絡帳／組織／人`）。
//
// 2026-09-05 に `担当者` を挟んだ理由は「社名ページの子には装置名称が並ぶので、
// 人を直接ぶら下げると埋もれる」でしたが、**同じ日に「装置名称の上に段」も
// 決まっています**（`取引先／社名／段／装置名称`）。社名ページの子は段3つだけに
// なったので、挟む理由は当時のうちに消えていました——**2つの決定が同じ日に出て、
// 後のほうが前のほうの根拠を消していた**わけです。
//
// 木が分かれたいまは、人の隣に段すら並びません。

// ContactsBoxPageID はトップ直下の連絡帳ページを返します（無ければ ok=false）。
func ContactsBoxPageID() (string, bool) { return cms.TopLevelPageByTitle(ContactsBoxTitle) }

// EnsureContactsBox は取引先ページを返し、**無ければ作ります**。
//
// **通信箱と違って自動で作ります。** 通信箱は「そこへ落とすと取り込みが走る」という
// 機能の入口なので、人が意図して置くものです。取引先はただの置き場——無いからと
// 登録ボタンを行き止まりにする理由がありません。名前を変えられたら次の登録で
// また作られますが、**取り込みのように静かに壊れることはありません**。
//
// 権限は呼ぶ側が見ます（作るときはトップへの書き込み、あるときは箱への書き込み）。
func EnsureContactsBox(user *auth.User) (string, error) {
	if id, ok := ContactsBoxPageID(); ok {
		return id, nil
	}
	return cms.CreateChildPage(cms.TopPageID, user.Username, contactsBoxBody())
}

// contactsBoxBody は取引先ページの初期の本文です。
//
// **「未登録の連絡先」の作業面を最初から載せます**（2026-09-11）。ここを空の見出し
// だけで作っていたために、**アドレス帳の作業面がどこにも存在しませんでした**
// ——誰も一覧を見たことがないまま実メール100通が過ぎ、11ドメインのうち登録済みは
// 1件だけ（しかもそれは整理が作った側で、連絡先を持っていなかった）。
//
// 通信箱は**人が意図して置くページ**なので、作業面も人が入れます。取引先は
// **機械が作る**ので、**行き止まりのページを作らない責任はこちらにあります**。
func contactsBoxBody() string {
	return "<h1>" + stdhtml.EscapeString(ContactsBoxTitle) + "</h1>" +
		"<p>取引の相手（会社・個人）を集めます。木は<strong>組織／人</strong>の2段です" +
		"——会社なら「社名／窓口の人」、個人のお客様なら「個人／お名前」。</p>" +
		"<p>組織のページには<strong>ドメイン</strong>のタグを、人のページには" +
		"<strong>メールアドレス</strong>のタグを付けると、届いたメールから相手を引けます。</p>" +
		`<section data-type="` + ContactsViewType + `"></section>`
}

// EmailTag は連絡先のメールアドレスです。**1ページに何個でも置けます**
// （会社の窓口が複数、同じ人が複数のアドレスを持つ、どちらも起きる）。
const EmailTag = "メールアドレス"

// ⚠ **`取引`（顧客・仕入先・自社）は 2026-09-17 に全廃しました**（ユーザー決定:
// 「自社、顧客、仕入先の区別は全廃します」）。
//
// **読んでいたのは `自社` だけ**で、`顧客` と `仕入先` は誰も読んでいませんでした。
// そのうえ3つは**同じ種類の言葉ではありません**——顧客と仕入先は取引の相手を
// 分類しますが、自社は「**これは取引の相手ではない**」と言っています。分類ではなく、
// この導入環境そのものの性質です。3つを1つのタグに並べていたのが無理でした。
//
// **記録する自由は失われていません**——`取引` は設定の語彙にある言葉1つだったので、
// 人が本文に `取引：仕入先` と書くのは**コードを1行も足さずにいつでもできます**。
// 発注の仕組みを作るときに要るなら、そのとき読む側を書けば足ります。
//
// ⚠ **自社を知らなくなった代償が2つあります**（承知のうえの決定）:
//
//   - 同僚のアドレスが「未登録の連絡先」に並びます（ドメインでまとめて消す道が無い）
//   - 自社が顧客名の推奨値に出ます（通すと `取引先／自社名` のフォルダができる）
//
// 戻すなら、タグではなく**設定に「自社のドメイン」を置く**のが筋です——ページを先に
// 作る必要がなく、`git pull` で全環境へ届き、同僚を1人ずつ登録しなくて済みます。

// PersonalOrgTitle は個人のお客様を置く組織ページの題です（`連絡帳／個人／山田太郎`・
// 2026-09-16 ユーザー:「個人のお客様は、社名ページの代わりに、たとえば個人という名前の
// 組織ページの下に個人名ページが来る」）。組織のコンボボックスには常にこれが並びます
// （2026-09-17）。⚠ **この組織に `ドメイン` は付けません**——ヤフーやジーメールは
// 共有のドメインで、付けるとそのドメインの他人まで「個人」に引き寄せられます。
const PersonalOrgTitle = "個人"

// PersonsOf は組織ページの直下の人（読めるものだけ）を題の順で返します。
// 担当者のコンボボックスの候補です（2026-09-17）。
func PersonsOf(user *auth.User, orgID string) []PartnerRef {
	orgInt, err := strconv.Atoi(orgID)
	if err != nil {
		return nil
	}
	rows, err := database.DB.Query(
		`SELECT id, COALESCE(title, '') FROM pages WHERE parent_id = ? ORDER BY title ASC`, orgInt)
	if err != nil {
		return nil
	}
	type hit struct {
		id    int
		title string
	}
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
		out = append(out, PartnerRef{ID: page.FormatID(h.id), Title: h.title})
	}
	return out
}

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
// ⚠ **自社も返します**（2026-09-17 に `取引：自社` を全廃したため）。同僚のメールから
// 整理すると、顧客名の推奨値に自社の名前が出ます——**通すと `取引先／自社名` の
// フォルダができます**。人が見て打ち替えてください。
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
		return page.FormatID(h.id), cms.PageTitleByID(h.id), true
	}
	return "", "", false
}

// DomainTag は**組織の連絡先**です（`メールアドレス` が個人の連絡先なのに対して）。
//
// 2026-09-16 ユーザー決定:「**アドレス帳の社名ページには、ドメインのタグが必要**という
// ことになります。タグがあればDBに入るので、メールが届いたときに検索すれば社名ページが
// わかり、すなわち社名もわかるからです」。
//
// **2026-09-12 の「ドメインを独立したタグにしない」を覆します。** 当時の心配は
// 「タグが2種類になり、どちらが正かを人が意識する」でしたが、**2種類ではなく別のもの**
// でした——`メールアドレス` は**その人**を、`ドメイン` は**その組織**を言い当てます。
//
// **暗黙の切り出しをやめたのが本体です。** それまでは登録済みアドレスから機械が
// ドメインを切り出して一致を見ていたので、`@yahoo.co.jp` の個人客を1人登録すると
// **以後その全員がその人になりました**（実データに yahoo のアドレスがあります）。
// タグなら、yahoo に付けないのは編集者の判断です。
//
// **1つの組織が複数持てます**（自社が `example-works.co.jp` と `itohocorp.onmicrosoft.com` の
// 2つを持つ実例）。タグが複数になるだけです。
//
// ⚠ **同じドメインを複数の組織が持つと、1つに決まりません。** そのときは
// `ResolvePartner` が ok=false を返し、**候補を人に選ばせます**
// （`PartnerCandidates`。2026-09-16 ユーザー:「コンボボックスで選択できる候補が
// 複数あるということでどうでしょう」）。
const DomainTag = "ドメイン"

// PartnerRef は既にある相手1枚（画面の選択肢・候補に使います）。は下にあります。

// ResolvePartner はアドレスから**組織のページ**を引きます（§5.2）。
//
// 順序は **①`メールアドレス` の完全一致 → ②`ドメイン` タグの一致**。完全一致が先なのは
// 「自社の工場長だけ別プロバイダ」という実例があるからで、**ドメインは手掛かりであって
// 決定ではない**という位置づけは変わりません。
//
// **1つに決まらなければ ok=false** です——同じドメインを2つの組織が持っていたら、
// 機械には選べません（`PartnerCandidates` が候補を返し、人が選びます）。
//
// 載っているのが人のページでも、**答えるのは組織**です（`PartnerOfPage` で丸める）
// ——整理が欲しいのは会社の名前だからです。
func ResolvePartner(user *auth.User, addr string) (pageID string, title string, ok bool) {
	exact, domain := partnerHits(user, addr)
	if len(exact) == 1 {
		return page.FormatID(exact[0].id), exact[0].title, true
	}
	if len(exact) > 1 {
		return "", "", false // 同じアドレスが2つの組織に載っている（人が直す）
	}
	if len(domain) == 1 {
		return page.FormatID(domain[0].id), domain[0].title, true
	}
	return "", "", false
}

// PartnerCandidates はアドレスから引ける**組織の候補**を返します（0件も普通）。
//
// `limit` を超えたら打ち切り、`truncated` を立てます。⚠ **件数制限は必須です**
// （2026-09-16 ユーザー:「ヤフーのようなドメインでは、候補が1万件ということも
// あり得ます…あまりに多い場合、コンボボックスにはすべての候補は出せませんから、
// 自分で入力するしかないのです」）。**選べない長さの一覧は、選択肢ではありません。**
func PartnerCandidates(user *auth.User, addr string, limit int) (refs []PartnerRef, truncated bool) {
	exact, domain := partnerHits(user, addr)
	all := append(exact, domain...)
	seen := map[int]bool{}
	for _, h := range all {
		if seen[h.id] {
			continue
		}
		seen[h.id] = true
		if limit > 0 && len(refs) >= limit {
			return refs, true
		}
		refs = append(refs, PartnerRef{ID: page.FormatID(h.id), Title: h.title})
	}
	return refs, false
}

// partnerMatch は引き当てた組織1件です。
type partnerMatch struct {
	id    int
	title string
}

// partnerHits はアドレスから、**完全一致で引けた組織**と**ドメインで引けた組織**を
// 別々に返します（どちらも重複なし・ページIDの順）。
//
func partnerHits(user *auth.User, addr string) (exact, byDomain []partnerMatch) {
	mail := normalizeEmail(addr)
	if mail == "" {
		return nil, nil
	}
	domain := domainOf(mail)

	// 個人の連絡先（`メールアドレス`）と組織の連絡先（`ドメイン`）を1度に読みます。
	// **先に読み切ってから絞ります**（行を読みながら別のクエリを投げない）。
	rows, err := database.DB.Query(
		`SELECT page_id, name, value FROM page_tags WHERE name IN (?, ?)`,
		EmailTag, DomainTag)
	if err != nil {
		return nil, nil
	}
	type hit struct {
		id          int
		name, value string
	}
	var found []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.name, &h.value); err != nil {
			rows.Close()
			return nil, nil
		}
		found = append(found, h)
	}
	rows.Close()

	add := func(dst *[]partnerMatch, id int, title string) {
		for _, m := range *dst {
			if m.id == id {
				return
			}
		}
		*dst = append(*dst, partnerMatch{id: id, title: title})
	}
	for _, h := range found {
		if user != nil && !page.CanView(user, h.id) {
			continue
		}
		companyID, title, ok := PartnerOfPage(h.id)
		if !ok || title == "" {
			continue // 連絡帳の外のページに書かれた値は連絡先ではない
		}
		switch h.name {
		case EmailTag:
			if normalizeEmail(h.value) == mail {
				add(&exact, companyID, title)
			}
		case DomainTag:
			// **完全一致だけ**（サブドメインは拾いません・2026-09-16 ユーザー決定）。
			// 要るなら編集者がタグを足します。
			if normalizeDomain(h.value) == domain {
				add(&byDomain, companyID, title)
			}
		}
	}
	return exact, byDomain
}

// normalizeDomain は `ドメイン` タグの値を畳みます（小文字・前後の空白・先頭の `@`）。
// 値の形は `example-sports.co.jp`（`@` なし）ですが、**`@` 付きで書かれても拾います**
// ——人が書く欄なので、書き方の揺れで静かに引けなくなるほうが困ります。
func normalizeDomain(v string) string {
	d := strings.ToLower(strings.TrimSpace(v))
	d = strings.TrimPrefix(d, "@")
	if i := strings.LastIndex(d, "@"); i >= 0 { // アドレスを貼られても拾う
		d = d[i+1:]
	}
	return d
}

// PartnerTitleForAddress はアドレスから組織の**題**を返します（`ResolvePartner` の薄い皮）。
//
// 整理（`ext/subcon`）と参照リンクの描画が呼びます。**ページが欲しいなら
// `ResolvePartner`** を使ってください——題で引き直すのは完全一致の二度手間です。
func PartnerTitleForAddress(user *auth.User, addr string) (string, bool) {
	_, title, ok := ResolvePartner(user, addr)
	return title, ok
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
	return addContactTags(pageID, author, EmailTag, addrs)
}

// AddContactDomains は組織のページへ `ドメイン` のタグを足します（2026-09-16）。
//
// **組織の連絡先**です（`AddContactAddresses` が個人の連絡先なのに対して）。
// **1つの組織が複数持てます**——自社が `example-works.co.jp` と `itohocorp.onmicrosoft.com` の
// 2つを持つのが実データの例で、タグが2つ並ぶだけです。
//
// ⚠ **フリーメールのドメインを足さないのは編集者の判断です**（2026-09-16 ユーザー:
// 「これは編集者がなんとかする問題だと思います」）。足しても壊れはしません——
// 同じドメインを別の組織も持てば `ResolvePartner` が1つに決めず、候補を出します。
func AddContactDomains(pageID, author string, domains []string) (int, error) {
	norm := make([]string, 0, len(domains))
	for _, d := range domains {
		if n := normalizeDomain(d); n != "" {
			norm = append(norm, n)
		}
	}
	return addContactTags(pageID, author, DomainTag, norm)
}

// addContactTags は連絡先のタグ（`メールアドレス`／`ドメイン`）を足す共通の本体です。
//
// 既に載っている値は足しません——二度押しでタグが並ぶのを防ぐだけの判定なので、
// `MarkHandled` と同じく**文字列で見ます**（取りこぼしても害は同じタグが2つ）。
func addContactTags(pageID, author, tagName string, values []string) (int, error) {
	added := 0
	err := cms.RewriteBody(pageID, author, func(current string) string {
		added = 0 // 呼び直されても数が増えないように
		var pairs strings.Builder
		dt := `<dt>` + stdhtml.EscapeString(tagName) + `</dt>`
		for _, a := range values {
			dd := `<dd>` + stdhtml.EscapeString(a) + `</dd>`
			// ⚠ **同じ組で並んでいるかを見ます**——値だけで探すと、`メールアドレス` の
			// 値と `ドメイン` の値がたまたま同じ文字のときに取りこぼします。
			if strings.Contains(current, dt+dd) {
				continue
			}
			pairs.WriteString(dt + dd)
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

// isPartnerPage はそのページが「取引先」の直下にあるかを返します。
//
// **足す先を箱の中に限ります。** 画面から来たIDをそのまま信じると、通信記録や
// 図面ページに `メールアドレス` のタグが付き、ドメインの逆引きが別物を拾います。
func isPartnerPage(pageIDInt int) bool {
	boxID, ok := ContactsBoxPageID()
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

// PartnerByTitle は、連絡帳の直下から**題の一致する組織**を1枚返します（2026-09-16）。
//
// **加工製品の階層（`取引先`）と連絡帳を結ぶための口**です。2つの木は 2026-09-16 に
// 分かれたので、`取引先／社名` のページから「この会社は誰か」を辿る道が要ります。
// 整理が参照タグ（`相手`）を書くときに、ここで行き先を引きます。
//
// **題で引きます**——人が整理の画面で選んだ社名がそのまま行き先になるためです。
// 機械が推した組織で結ぶと、**人が打ち替えた社名と食い違ったまま結んで**しまいます。
//
// ⚠ **2枚あったら引きません**（ok=false）。同じ題の組織が2つあると、どちらを指すか
// 決められません——黙って片方を選ぶと、**参照が静かに間違った先を向きます**。
func PartnerByTitle(user *auth.User, title string) (pageID string, ok bool) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", false
	}
	boxID, found := ContactsBoxPageID()
	if !found {
		return "", false
	}
	boxInt, err := strconv.Atoi(boxID)
	if err != nil {
		return "", false
	}
	rows, err := database.DB.Query(
		`SELECT id FROM pages WHERE parent_id = ? AND title = ? ORDER BY id`, boxInt, title)
	if err != nil {
		return "", false
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", false
		}
		ids = append(ids, id)
	}
	rows.Close()

	var hits []int
	for _, id := range ids {
		if user == nil || page.CanView(user, id) {
			hits = append(hits, id)
		}
	}
	if len(hits) != 1 {
		return "", false // 0枚（まだ登録していない）か、2枚（どちらか決められない）
	}
	return page.FormatID(hits[0]), true
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
	boxID, found := ContactsBoxPageID()
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
	// **組織の直下に人**（2026-09-16）。`担当者` の箱は挟みません——上の
	// ContactsBoxTitle の説明に、挟んでいた理由と、それが消えた経緯があります。
	return ensureChildByTitle(user, companyID, name)
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
		return page.FormatID(id), nil
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
	boxID, ok := ContactsBoxPageID()
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
		out = append(out, PartnerRef{ID: page.FormatID(h.id), Title: h.title})
	}
	return out
}
