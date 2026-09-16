package cms

// ─────────────────────────────────────────────────────────────────────────
// 拡張が要る置き場——管理画面のボタンで作る（2026-09-16）
//
// ユーザー:「**拡張プラグインが必要とするフォルダなどは、管理画面でボタンを押して
// 作成する仕組みにしてはどうでしょう？**」
//
// ── なぜ要るのか（実際に行き止まりになった）──
//
// w-cms には**名前で機能が決まるトップ直下のページ**がいくつかあります（通信箱・
// 取引先・受注・テンプレート置き場）。ところが**誰が作るかがばらばら**でした:
//
//	テンプレート … 誰も作らない（人が手で）
//	通信箱       … 誰も作らない（人が手で。「意図して置く」という決定）
//	取引先       … 連絡先の登録の口と整理だけ
//	受注         … 整理のときだけ
//
// 2026-09-16 にデータを一掃したところ、**取引先が行き止まり**になりました——
// 取引先ページを作るのは登録の口だけなのに、**登録の画面（未登録の連絡先）は
// 取引先ページの上に載っている**ためです。登録しないと作業面が出ず、作業面が
// 無いと登録できない。手で1枚作って抜けました。
//
// ── なぜ「起動時に自動で作る」ではなくボタンなのか ──
//
// **人が押したことが、そのページを置く意図です。** 通信箱は「そこへ落とすと
// 取り込みが走る」機能の入口なので、機械が勝手に置いてよいものではありません
// （2026-09-05 の決定）。ボタンなら**人が意図して置く**という線を保ったまま、
// 「どこに何を作ればいいのか分からない」だけを解けます。起動のたびに黙って
// ページが増える形は、無人生成の歯止め（§3 人間ゲート型）にも反します。
//
// ── 約束 ──
//
//   - **冪等**。既にあるものは触りません（何度押しても増えません）。
//   - **本文は作業面込み**。空の見出しだけのページを作ると、また行き止まりに
//     なります（2026-09-11 に実際にそうなった——「機械が作るページは行き止まりに
//     しない」）。だから本文は宣言した拡張が持ちます。
//   - **admin だけ**。トップ直下にページを作る操作なので、既存の規律どおり。
//   - **名前が機能、は変えません。** 人が題を変えれば機械は見つけられなくなります
//     ——受け入れ済みのリスクで、この仕組みはそこには触れません（むしろ
//     「作り直す」ボタンとして復旧に使えます）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"html"
	"sort"
	"strings"

	"w-cms/internal/auth"
)

// RequiredPage は「この拡張が要るトップ直下の置き場」1つです。
type RequiredPage struct {
	// Title はトップ直下の題です。**この文字が機能を決めます**（`TopLevelPageByTitle`）。
	Title string
	// Extension は持ち主の拡張ID（名簿と同じ`comm`・`comm/contacts`・`subcon`。
	// コアの持ち物は空）。画面がまとめて見せるために使います。
	Extension string
	// Why は「何のために要るか」の一文です。**画面にそのまま出ます**——
	// 押す人が、押してよいかを自分で判断できるように。
	Why string
	// Body は作るときの本文を返します。**作業面を含めること**——見出しだけの
	// ページを作ると行き止まりになります。nil なら見出しだけ。
	Body func() string
}

// requiredPageRegistry は登録された置き場です。**`init()` の中からだけ**登録します
// （サーバーが動き出す前に揃っている前提なので、ロックを持ちません）。
var requiredPageRegistry = map[string]RequiredPage{}

// RegisterRequiredPage は置き場を1つ登録します（拡張の `init` から呼ぶ）。
//
// **題が鍵**です——同じ題を2つの拡張が要求することはありえます（どちらも
// 「取引先」を使う、など）が、**本文が2通りになると先に押したほうが勝つ**という
// 説明のつかない形になるので、重複はその場で落とします。
func RegisterRequiredPage(p RequiredPage) {
	title := strings.TrimSpace(p.Title)
	if title == "" {
		panic("置き場の題が空です")
	}
	if prev, dup := requiredPageRegistry[title]; dup {
		panic("置き場の題が重複しています: " + title +
			"（" + prev.Extension + " と " + p.Extension + "）")
	}
	p.Title = title
	requiredPageRegistry[title] = p
}

// RequiredPageStatus は1つぶんの現状です（画面へ返す形）。
type RequiredPageStatus struct {
	Title     string `json:"title"`
	Extension string `json:"extension"`
	Why       string `json:"why"`
	PageID    string `json:"page_id"` // 使われるページ（いちばん古いもの。無ければ空）
	Exists    bool   `json:"exists"`
	// Duplicates は**同じ題の余りのページ**です（2枚目以降。普通は空）。
	//
	// **題が機能を決める**ので、同じ題が2枚あると `TopLevelPageByTitle` は
	// 片方しか返さず、**もう片方は誰からも見えないまま残ります**——2026-09-16 に
	// 取引先で実際に起きました（E2Eが作った1枚と手で作った1枚）。
	// 空のうちは害がありませんが、**片方に書き込むと、書いた内容がどこへ行ったのか
	// 分からなくなります**。防げない（題は人が自由に付けられる）ので、**知らせます**。
	Duplicates []string `json:"duplicates,omitempty"`
}

// RequiredPages は登録された置き場の**宣言**を並び順つきで返します。
//
// **DBを引きません**——「何が要るか」は起動時に確定しており、ページが在るかどうか
// （`RequiredPageStatuses`）とは別の問いです。分けてあるのは、宣言だけを見たい場面
// （試験・起動時の確認）でDBを用意させないためです。
//
// **載っている拡張のぶんだけ**出ます——`-tags minimal` の素の w-cms では
// コアの置き場（テンプレート）しか登録されないので、表もそれだけになります。
func RequiredPages() []RequiredPage {
	out := make([]RequiredPage, 0, len(requiredPageRegistry))
	for _, p := range requiredPageRegistry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Extension != out[j].Extension {
			return out[i].Extension < out[j].Extension
		}
		return out[i].Title < out[j].Title
	})
	return out
}

// RequiredPageStatuses は宣言に**いま在るかどうか**を添えて返します（DBを引きます）。
func RequiredPageStatuses() []RequiredPageStatus {
	decls := RequiredPages()
	out := make([]RequiredPageStatus, 0, len(decls))
	for _, p := range decls {
		st := RequiredPageStatus{Title: p.Title, Extension: p.Extension, Why: p.Why}
		if ids := TopLevelPagesByTitle(p.Title); len(ids) > 0 {
			st.PageID, st.Exists = ids[0], true
			if len(ids) > 1 {
				st.Duplicates = ids[1:]
			}
		}
		out = append(out, st)
	}
	return out
}

// CreateMissingRequiredPages は**足りないものだけ**作り、作ったぶんを返します。
//
// 途中で失敗しても、そこまでに作ったものは残します（作れたものを巻き戻すと
// 「押したのに何も起きない」になり、どこまで進んだか分からなくなるため）。
// エラーは題を添えて返すので、画面はどれで止まったかを出せます。
func CreateMissingRequiredPages(owner string) (created []RequiredPageStatus, err error) {
	for _, st := range RequiredPageStatuses() {
		if st.Exists {
			continue
		}
		p := requiredPageRegistry[st.Title]
		body := "<h1>" + html.EscapeString(p.Title) + "</h1>"
		if p.Body != nil {
			body = p.Body()
		}
		id, cerr := CreateChildPage(TopPageID, owner, body)
		if cerr != nil {
			return created, &requiredPageError{Title: p.Title, Err: cerr}
		}
		auth.Audit(owner, "required-page.create", id+" ("+p.Title+")")
		st.PageID, st.Exists = id, true
		created = append(created, st)
	}
	return created, nil
}

// requiredPageError は「どの置き場で失敗したか」を持つエラーです。
type requiredPageError struct {
	Title string
	Err   error
}

func (e *requiredPageError) Error() string {
	return "「" + e.Title + "」を作れません: " + e.Err.Error()
}

func (e *requiredPageError) Unwrap() error { return e.Err }
