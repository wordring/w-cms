package cms

// ─────────────────────────────────────────────────────────────────────────
// 手で記録を作る口（2026-09-05）
//
// ユーザー:「通信箱に入るページは、メール、FAX、電話、メモです。メールは受信できる
// ようになりましたが、FAX、電話はまだです。そこで、受信箱にメモを作るボタンを付けて、
// チャネルを選択してはどうでしょうか」。
//
// **形は取り込みと同じ**です——通信箱／年／月の下に記録ページが1枚立ち、`チャネル` の
// タグが付く。違うのは**誰が作ったか**だけ。**ファイルはこのページへ落とします**
// （2026-09-05 ユーザー:「通信箱のPDF、DXF取り込みはやめましょう。メモに添付する
// ようにしましょう」）——落ちてきた PDF を機械が `チャネル：FAX` と決めつけていたのを
// 取り止めたためで、経路は届いた本人しか知りません。
//
// **FAXサーバーを繋ぐときは機械専用の口を作ること**（intake.go）。人の手ドロップと
// 機械の投函を同じ口で受けると、また決めつけが戻ります。
//
// **書くのは分かることだけ**です:
//
//   - `チャネル` …… 人が選ぶ（メール／FAX／電話／メモ）
//   - `向き` …… 人が選ぶ（受信／送信）。**電話にもFAXにも両方あります**
//     （2026-09-05 ユーザー指摘）——かかってきた電話と、かけた電話は別の出来事です。
//     **これは「済んだ」の印ではありません**——「かけた電話で受注することもあるので、
//     処理はすんでいません。やはり、人間がチェックするべきでは」。向きは
//     **どちら向きの出来事か**しか語らず、済んだかどうかを決めるのは人（`対応` の印）です。
//   - `受信日時` / `発信日時` …… **向きに応じて**どちらか一方だけ。メモには向きが
//     無いので、どちらも書きません（並びはページの更新日時で代わりが利きます）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"html"
	"net/http"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// PhoneTag は電話番号です。**顧客ページや通信記録に付けておくと、そこから
// 掛けられます**（`tel:` の発信ボタン・app.js）。CTI を入れたときは、着信の番号から
// このタグの逆引き（PagesByTag）で相手を名指しできます——受注明細の品番から部品定義を
// 引くのとまったく同じ仕組みで、新しいものは要りません。
const PhoneTag = "電話番号"

// CounterpartTag は通話の相手です。**ページを指す宣言つきのタグ**なので、
// 6桁の値は描画時にリンクへ合成されます（人の名前を書いてもよく、その場合はただの文字）。
const CounterpartTag = "相手"

// memoChannels は手で作れるチャネルです。**表引きで閉じます**——自由に書けると
// `電話` と `TEL` が混ざり、チャネルで絞る一覧が静かに取りこぼします。
var memoChannels = map[string]bool{
	"メール": true, "FAX": true, "電話": true, "メモ": true,
}

// MemoChannels は選択肢を並び順つきで返します（画面が使います）。
func MemoChannels() []string { return []string{"電話", "FAX", "メール", "メモ"} }

// directionless は向きを持たないチャネルです。**メモは届きも出もしません**
// ——自分のための覚え書きなので、向きを聞くこと自体が意味を持ちません。
var directionless = map[string]bool{"メモ": true}

// MemoDirections は向きの選択肢です（画面が使います）。
func MemoDirections() []string { return []string{DirectionIn, DirectionOut} }

// HasDirection はそのチャネルが向きを持つかを返します（画面が出し分けます）。
func HasDirection(channel string) bool { return !directionless[channel] }

// NewMemoAPIHandler は POST /api/intake/memo です。
// 入力: {"channel": "電話", "direction": "受信", "title": "納期の相談"}——題は省略できます。
func NewMemoAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	var req struct {
		Channel string `json:"channel"`
		Title   string `json:"title"`
		// 以下は発信（CMSから電話をかける）で使います。省略できます。
		Direction   string `json:"direction"`   // 送信 のときだけ意味を持つ
		Phone       string `json:"phone"`       // かけた番号
		Counterpart string `json:"counterpart"` // 相手ページのID（6桁）
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	channel := strings.TrimSpace(req.Channel)
	if !memoChannels[channel] {
		JSONFail(w, http.StatusBadRequest, "チャネルが不正です")
		return
	}
	boxID, ok := MailBoxPageID()
	if !ok {
		JSONFail(w, 0, "通信箱ページがありません（トップ直下に「"+MailBoxTitle+"」という名前のページを作ってください）")
		return
	}
	// **通信箱への write を要求します**（取り込みと同じ関門）。
	if !page.RequirePageWrite(w, r, boxID) {
		return
	}

	now := time.Now()
	title := strings.TrimSpace(req.Title)
	if title == "" {
		// 題が無いときは FAX の取り込みと同じ流儀で作ります（`FAX 2026-09-04 16:40`）。
		title = channel + " " + now.In(time.Local).Format("2006-01-02 15:04")
	}

	// **向きは受信・送信のどちらも受け付けます**（メモには付きません）。
	// **向きは済んだかどうかを決めません**——かけた電話でも受注することがあるので、
	// 印を付けるのは人です（view_unhandled.go）。
	direction := strings.TrimSpace(req.Direction)
	if !HasDirection(channel) || (direction != DirectionIn && direction != DirectionOut) {
		direction = ""
	}
	body := memoBodyHTML(channel, title, direction,
		strings.TrimSpace(req.Phone), strings.TrimSpace(req.Counterpart), now)

	pageID, err := CreateRecordPage(boxID, user.Username, now, body)
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, "記録を作れません: "+err.Error())
		return
	}
	auth.Audit(user.Username, "intake.memo", pageID+" ("+channel+")")
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "page_id": pageID, "title": title,
	})
}

// memoBodyHTML は記録1枚の本文を組み立てます（切り出してあるのはテストのため）。
//
// **書くのは分かることだけ**です——メモは届いていないので受信日時を書かず、
// 発信には向きと発信日時を書く。ここが崩れると、かけた電話が作業待ちに並びます。
func memoBodyHTML(channel, title, direction string, phone, counterpart string, now time.Time) string {
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(`<dl data-type="tags">`)
	WriteTag(&b, DirectionTag, direction) // 空なら書かれない（メモ）
	WriteTag(&b, ChannelTag, channel)
	// **日時は向きに応じて片方だけ。** 両方書くと「どちらが本当か」が生まれます。
	switch direction {
	case DirectionOut:
		WriteTag(&b, "発信日時", now.In(time.Local).Format(time.RFC3339))
	case DirectionIn:
		WriteTag(&b, "受信日時", now.In(time.Local).Format(time.RFC3339))
	}
	WriteTag(&b, PhoneTag, phone)
	// 相手はページ参照（6桁）。**正規化を通すのは、パスに使う前と同じ規律**で、
	// 揺れた表記がそのままタグへ入るのを防ぎます。
	if cp, ok := page.NormalizeID(counterpart); ok {
		WriteTag(&b, CounterpartTag, cp)
	}
	b.WriteString("</dl>")
	// 本文は空の段落を1つ。**開いてすぐ書き始められる**ようにするためで、
	// 何も無いページを開くと「どこへ書くのか」から迷います。
	b.WriteString("<p><br/></p>")
	return b.String()
}
