package subcon

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// 発注書を送る・発注済みの印（2026-09-22）
//
// ユーザー:「**発注書の表の一品ずつに発注済みの印**をつけます。…**メールの送信など
// 自明な時は、自動で印をつければ良い**し、発注書を印刷して手渡しするような場合は、
// **人間が発注済みの印をつける**と思います。大事なことは、**発注の取り消しもある**
// ということです」。

// orderPaper は発注明細1枚ぶんの本文を組みます（行は `状態` つき）。
func orderPaper(statuses ...string) string {
	var b strings.Builder
	b.WriteString(`<h1>発注 みなと商店</h1>`)
	b.WriteString(`<table data-type="` + ourOrderItemsType + `"><tbody>`)
	b.WriteString(`<tr><th>弊社品番</th><th>寸法</th><th>数量</th><th>状態</th></tr>`)
	for i, st := range statuses {
		b.WriteString(`<tr><td>000031</td><td>t` + string(rune('1'+i)) +
			`</td><td>2</td><td>` + st + `</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// TestSetOrderLineStatusOneRow は、⚠ **1行だけ**書き換わることを固定します。
func TestSetOrderLineStatusOneRow(t *testing.T) {
	body := orderPaper(OrderLineUnsent, OrderLineUnsent)
	out, n := setOrderLineStatus(body, 2, OrderLineCancelled)
	if n != 1 {
		t.Fatalf("書き換えた行が %d です（1を期待）:\n%s", n, out)
	}
	if strings.Count(out, "<td>"+OrderLineUnsent+"</td>") != 1 {
		t.Errorf("⚠ 1行目まで巻き込んでいます:\n%s", out)
	}
	if !strings.Contains(out, "<td>"+OrderLineCancelled+"</td>") {
		t.Errorf("2行目が取消になっていません:\n%s", out)
	}
}

// TestSetOrderLineStatusAllSkipsCancelled は、⚠ **まとめ書きが取消を起こさない**ことを
// 固定します。
//
// ⚠ **これは実害のある取り違えです。** 巻き込むと、**取り消したはずの材料が
// 「手配済み」に戻り**、未手配の一覧から静かに消えます——そして誰も買いません。
func TestSetOrderLineStatusAllSkipsCancelled(t *testing.T) {
	body := orderPaper(OrderLineUnsent, OrderLineCancelled, OrderLineUnsent)
	out, n := setOrderLineStatus(body, 0, OrderLineSent)
	if n != 2 {
		t.Fatalf("書き換えた行が %d です（2を期待）:\n%s", n, out)
	}
	if !strings.Contains(out, "<td>"+OrderLineCancelled+"</td>") {
		t.Errorf("⚠ 取消の行まで発注済みにしています:\n%s", out)
	}
	if strings.Count(out, "<td>"+OrderLineSent+"</td>") != 2 {
		t.Errorf("発注済みが2行になっていません:\n%s", out)
	}
}

// TestSetOrderLineStatusSaysNothingChanged は、⚠ **同じ値なら書かない**ことを
// 固定します（版と更新日時を無駄に進めないため）。
func TestSetOrderLineStatusSaysNothingChanged(t *testing.T) {
	body := orderPaper(OrderLineSent)
	out, n := setOrderLineStatus(body, 0, OrderLineSent)
	if n != 0 || out != body {
		t.Errorf("同じ値で書き換えています: n=%d\n%s", n, out)
	}
}

// TestCancelledOrderStaysConsumed は、⚠ **取り消しても必要部材表へ戻らない**ことを
// 固定します（2026-09-23 ユーザー:「状態を取り消しにすることの意味が、**その部材は
// もう発注しない**ということになりました。**発注書ページで消費して発注しなくなります**」）。
//
// ⚠ **09-22 は逆でした**（取り消すと自動で戻る）。この番人はその名残を捕まえます
// ——戻る側に書き戻すと、**もう発注しないと決めた部材が必要部材表に再び並び**、
// 同じものをもう一度発注書に入れてしまいます。戻したいときは「必要部材表へ戻す」。
func TestCancelledOrderStaysConsumed(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	viewer := &auth.User{Username: "root", IsAdmin: true}

	// 下ごしらえの発注書（32）は 板 t3.2 を6本手配していて、必要数と同じです
	// ——だから一覧には FB t4.5 の1件しか出ません（`unordered_test.go` の番人）。
	before, err := UnorderedItems(viewer)
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("下ごしらえが違います（未手配 %d 件）: %#v", len(before), before)
	}

	// その発注書を取り消します。
	syncBody(t, 32, `<h1>発注 みなと商店</h1>`+
		`<dl data-type="tags"><dt>`+SupplierTag+`</dt><dd>みなと商店</dd>`+
		`<dt>`+OrderedAtTag+`</dt><dd>2026-09-21</dd></dl>`+
		`<table data-type="`+ourOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単価</th><th>状態</th></tr>`+
		`<tr><td>000031</td><td>鉄</td><td>板</td><td>t3.2*100*200</td><td>6</td><td>800</td>`+
		`<td>`+OrderLineCancelled+`</td></tr>`+
		`</tbody></table>`)

	after, err := UnorderedItems(viewer)
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	// ⚠ **件数だけでなく中身も見ます**——下ごしらえと同じ1件で、それが t3.2 では
	//    ないこと（取り消した材料が戻っていないこと）まで確かめます。
	if len(after) != 1 {
		t.Fatalf("⚠ 取り消した材料が必要部材表に戻っています（%d 件）: %#v", len(after), after)
	}
	for _, u := range after {
		if strings.Contains(u.Size, "t3.2") {
			t.Errorf("⚠ 取り消した材料が一覧にあります: %#v", u)
		}
	}
}

// TestUnsentPriceIsNotABoughtPrice は、⚠ **出していない紙の単価を「最新単価」に
// 出さない**ことを固定します。
//
// `未発注` は**こちらが書いただけ**で、仕入先はまだ何も承諾していません——
// それを相場として出すと、**自分で書いた希望額で見積もって受注**することになります。
func TestUnsentPriceIsNotABoughtPrice(t *testing.T) {
	setupMaterialsPermsTest(t)
	rows := `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>2</td></tr>`
	seedMaterialAndOrder(t, rows, orderSeed{
		ID: 20, Owner: "root", Mode: "302", Public: true,
		Date: "2026-08-19", Supplier: "みなと商店", Status: OrderLineUnsent,
		Rows: `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>800</td></tr>`,
	})

	got := showMaterials(t, &auth.User{Username: "root", IsAdmin: true}, materialsBody(rows))
	if strings.Contains(got, "800円") {
		t.Errorf("⚠ まだ出していない発注書の単価が「最新単価」に出ています:\n%s", got)
	}
	// ⚠ **黙りません。** 「引けなかった」と「そもそも鏡が走っていない」を
	//    見分けられる形で言います。
	if !strings.Contains(got, "買った記録がありません") {
		t.Errorf("⚠ 引けなかったことを黙っています:\n%s", got)
	}
}

// TestOrderSendState は、⚠ **合っているときも黙らない**ことを固定します。
//
// 黙ると「出した」と「そもそも数えていない」が見分けられません（検算と同じ規律）。
func TestOrderSendState(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    OrderSendCounts
		want string
	}{
		{"未発注", OrderSendCounts{Total: 3}, "まだ発注していません"},
		{"一部", OrderSendCounts{Total: 3, Sent: 1}, "2行がまだ発注済みになっていません"},
		{"全部", OrderSendCounts{Total: 3, Sent: 3}, "✓ 発注済み"},
		{"全部取消", OrderSendCounts{Cancelled: 2}, "全部取り消しました"},
		{"明細なし", OrderSendCounts{}, "明細がありません"},
	} {
		if got := orderSendStateHTML(tc.c); !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q に %q がありません", tc.name, got, tc.want)
		}
	}
	// ⚠ **0行を「発注済み」と言わないこと**——沈黙を成功と読む形です。
	if (OrderSendCounts{}).AllSent() {
		t.Error("⚠ 明細の無い紙が「発注済み」になっています")
	}
}

// TestOrderLinkHasNoBakedState は、⚠ **「まだ発注していません」を本文に焼き込まない**
// ことを固定します。
//
// ⚠ **出したら消えなければならない文**なので、本文に書くと**発注書を出したあとも
// 古いまま**残ります——そして直す人は現れません。
func TestOrderLinkHasNoBakedState(t *testing.T) {
	got := orderLinkHTML("000145", "みなと商店", 5)
	if strings.Contains(got, "まだ発注していません") {
		t.Errorf("⚠ 進み具合が本文に焼き込まれています:\n%s", got)
	}
	for _, want := range []string{`data-ref="000145"`, "みなと商店", "5行", `href="/000145"`} {
		if !strings.Contains(got, want) {
			t.Errorf("本文に %q がありません:\n%s", want, got)
		}
	}
}

// TestOrderLinkMirrorReadsTheOrderPage は、⚠ **リンクが発注書ページから読み直す**
// ことを固定します。
//
// ⚠ **「⚠ が出ないこと」だけを見ません**——鏡が一度も走っていなくても通るためです。
// **出していないときは「まだ」、出したあとは「✓」**の**両方**を見ます。
func TestOrderLinkMirrorReadsTheOrderPage(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 40, 0, "発注", "root", "302", true)
	addPage(t, 41, 0, "発注 みなと商店", "root", "302", true)

	body := orderLinkHTML("000041", "みなと商店", 2)
	viewer := &auth.User{Username: "root", IsAdmin: true}

	syncBody(t, 41, orderPaper(OrderLineUnsent, OrderLineUnsent))
	if got := showPage(t, viewer, 40, body); !strings.Contains(got, "まだ発注していません") {
		t.Fatalf("⚠ 出していないのに「まだ」が出ません（鏡が走っていない？）:\n%s", got)
	}

	syncBody(t, 41, orderPaper(OrderLineSent, OrderLineSent))
	got := showPage(t, viewer, 40, body)
	if strings.Contains(got, "まだ発注していません") {
		t.Errorf("⚠ 出したのに「まだ発注していません」が残っています:\n%s", got)
	}
	if !strings.Contains(got, "✓ 発注済み") {
		t.Errorf("⚠ 出したことを黙っています:\n%s", got)
	}
}

// TestOurOrderMirrorPutsTheSendForm は、発注明細の足元に送信欄が出ることと、
// ⚠ **それが本文に残らないクローム**であることを固定します。
//
// ⚠ **`vocab-chrome` が無いと、人が画面の表をコピーして貼ったとき本当の列として
// 保存されます**（`class` はサニタイズで落ちるので、貼られた時点では見分けが
// 付かなくなる）。
func TestOurOrderMirrorPutsTheSendForm(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 41, 0, "発注 みなと商店", "root", "302", true)

	body := orderPaper(OrderLineUnsent)
	syncBody(t, 41, body)
	got := showPage(t, &auth.User{Username: "root", IsAdmin: true}, 41, body)

	for _, want := range []string{
		"FAXで送った", "手渡した", "メールで送る",
		`data-order-send="1"`,                       // メールの送信ボタン
		`data-order-status="` + OrderLineSent + `"`, // 行ごとの「発注済」
		`class="vocab-chrome order-row-act"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ 鏡に %q がありません:\n%s", want, got)
		}
	}
}

// TestCancelIsReachableFromEveryStage は、⚠ **どの段からも取り消せる**ことを
// 固定します。
//
// ユーザー（2026-09-22）:「**発注した後でも取り消す場合があり得ます**。この場合、
// 電話などで材料屋に取り消しを依頼して、OKが出たら取り消しボタンを押します。
// 発注前なら単純に取り消します。**発注後に材料屋から取り扱いが無いと連絡が来て
// キャンセルになることもあります**」。
//
// ⚠ **`納品済` からも押せること**を見ます——「2手かければ戻せる」では、
// 現物が届いたあとの取消が**在ることに気づけません**。
func TestCancelIsReachableFromEveryStage(t *testing.T) {
	for _, st := range []string{OrderLineUnsent, OrderLineSent, OrderLineDelivered,
		orderLineLegacySent, ""} {
		got := orderRowButtonsHTML("000041", 1, st)
		if !strings.Contains(got, `data-order-status="`+OrderLineCancelled+`"`) {
			t.Errorf("⚠ 状態 %q から取り消せません:\n%s", st, got)
		}
	}
	// ⚠ **取消からは「取消」を出しません**——押しても何も変わらないボタンは、
	//    押した人に「効かなかった」と読まれます。
	if got := orderRowButtonsHTML("000041", 1, OrderLineCancelled); strings.Contains(
		got, `data-order-status="`+OrderLineCancelled+`"`) {
		t.Errorf("⚠ 取消の行にもう一度「取消」が出ています:\n%s", got)
	}
}

// TestCancelAsksOnlyAfterThePaperWentOut は、⚠ **確認を出す／出さないの線引き**を
// 固定します。
//
// ユーザー:「**発注前なら単純に取り消します**」／発注後は「電話などで材料屋に
// 取り消しを依頼して、**OKが出たら**取り消しボタンを押します」。
//
// ⚠ **紙が外へ出たあとの取消は、押しただけでは終わりません。** 確かめずに押せる
// 形にすると「押したから片付いた」と読まれ、**材料屋には注文が残ったまま**に
// なります。⚠ **逆に、発注前にまで確認を出すと、毎回読まれない問いが増えます**
// ——そして読まれない確認は、**本当に要るときにも読まれません**。
func TestCancelAsksOnlyAfterThePaperWentOut(t *testing.T) {
	for _, tc := range []struct {
		status string
		asks   bool
	}{
		{OrderLineUnsent, false},
		{"", false},
		{OrderLineSent, true},
		{orderLineLegacySent, true},
		{OrderLineDelivered, true},
	} {
		got := orderRowButtonsHTML("000041", 1, tc.status)
		if asks := strings.Contains(got, "data-order-confirm="); asks != tc.asks {
			t.Errorf("状態 %q の確認が %v です（%v を期待）:\n%s",
				tc.status, asks, tc.asks, got)
		}
		// ⚠ **取り消したらどうなるかを書くこと**——「必要部材表へは戻りません」が
		//    無いと、09-22 までの意味（戻る）で読まれ、**誰も買わないまま納期が来ます**。
		if tc.asks && !strings.Contains(got, "必要部材表へは戻りません") {
			t.Errorf("状態 %q の確認に、戻り先が書かれていません:\n%s", tc.status, got)
		}
	}
}

// TestOrderPDFSkipsCancelledLines は、⚠ **取り消した行を紙に刷らない**ことを
// 固定します。
//
// ⚠ **刷ると合計金額にも入ります**（`buildOrderPDF` が数量×単価を足すため）
// ——**取り消したものの代金を請求される紙**を自分で作ることになります。
func TestOrderPDFSkipsCancelledLines(t *testing.T) {
	withPDFFont(t, systemJPFont(t))

	body := pdfOrderBody(
		`<tr><td></td><td></td><td></td><td>鉄FB</td><td>FB</td><td>t4.5</td><td></td>` +
			`<td>2</td><td>本</td><td>500</td><td></td><td>` + OrderLineSent + `</td></tr>` +
			`<tr><td></td><td></td><td>やめた部品</td><td></td><td></td><td></td><td></td>` +
			`<td>3</td><td>個</td><td>7000</td><td></td><td>` + OrderLineCancelled + `</td></tr>`)
	pdf, err := buildOrderPDF(body, nil)
	if err != nil {
		t.Fatalf("PDFを作れません: %v", err)
	}
	got := pdfTextOf(t, pdf)
	if strings.Contains(got, "やめた部品") {
		t.Errorf("⚠ 取り消した行が紙に刷られています。読み返した中身:\n%s", got)
	}
	// ⚠ **合計まで見ます。** 行が消えても合計に残っていたら、紙の上では
	//    **取り消したものの代金を請求されます**。
	if !strings.Contains(got, "1,000") {
		t.Errorf("⚠ 合計が 1,000 円になっていません（取り消した21,000円が残っている？）:\n%s", got)
	}
	if strings.Contains(got, "21,000") || strings.Contains(got, "22,000") {
		t.Errorf("⚠ 取り消した行の金額が合計に入っています:\n%s", got)
	}
}

// TestOrderPDFSaysWhenEverythingIsCancelled は、⚠ **全部取り消したときに黙らない**
// ことを固定します。
//
// 「中身のある行がありません」だと、**書き忘れたのか、取り消したのか**が
// 読む人に分かりません。
func TestOrderPDFSaysWhenEverythingIsCancelled(t *testing.T) {
	body := pdfOrderBody(
		`<tr><td></td><td></td><td>やめた部品</td><td></td><td></td><td></td><td></td>` +
			`<td>3</td><td>個</td><td>7000</td><td></td><td>` + OrderLineCancelled + `</td></tr>`)
	_, _, _, err := readOrderDoc(body)
	if err == nil {
		t.Fatal("⚠ 全部取り消した発注書からPDFを作れてしまいます")
	}
	if !strings.Contains(err.Error(), "取り消され") {
		t.Errorf("理由が「取り消した」と言っていません: %v", err)
	}
}

// TestOrderPDFIsShownOnThePage は、⚠ **作ったPDFがページの上で開ける**ことを
// 固定します（2026-09-22 ユーザー報告:「発注書のページにPDFが表示されていません」）。
//
// ⚠ **原因は「作る道が画面に無かった」ことでした。** 口は 09-21 から在ったのに
// 呼ぶボタンがどこにも無く、**API を直に叩いて確かめただけ**でした——
// **試した経路と、人が使う経路が違っていた**わけです。
func TestOrderPDFIsShownOnThePage(t *testing.T) {
	body := orderPaper(OrderLineUnsent)
	out, ok := placeOrderPDFView(body, "000041-a1b2")
	if !ok {
		t.Fatalf("マーカーを置けません:\n%s", out)
	}
	if !strings.Contains(out, `data-type="`+cms.FileViewType+`"`) ||
		!strings.Contains(out, `data-ref="000041-a1b2"`) {
		t.Fatalf("⚠ ファイル表示のマーカーがありません:\n%s", out)
	}
	// ⚠ **表の「後」であること**（ユーザーの流れ:「新たな表の下に発注書を埋め込み
	//    表示し」）。前に置くと、明細より先にPDFが出ます。
	if strings.Index(out, "</table>") > strings.Index(out, `data-ref="000041-a1b2"`) {
		t.Errorf("⚠ マーカーが発注明細の表より前にあります:\n%s", out)
	}
	// ⚠ **サニタイズを通しても残ること。** ここが落ちると、**本文を保存した瞬間に
	//    配線が消え**、PDFは黙って出なくなります（`class` が落ちるのと同じ形）。
	if clean := cms.Sanitize(out); !strings.Contains(clean, `data-ref="000041-a1b2"`) {
		t.Errorf("⚠ サニタイズで配線が落ちています:\n%s", clean)
	}
}

// TestOrderPDFViewIsReplacedNotStacked は、⚠ **作り直しても増えない**ことを
// 固定します。
//
// ページの上に3枚並んでいても、**どれが最新か分かりません**。古いPDFは添付として
// 残り、実際に送ったものは通信箱の控えが持っています。
func TestOrderPDFViewIsReplacedNotStacked(t *testing.T) {
	body := orderPaper(OrderLineUnsent)
	first, _ := placeOrderPDFView(body, "000041-aaaa")
	second, ok := placeOrderPDFView(first, "000041-bbbb")
	if !ok {
		t.Fatalf("2枚目で置き換えられません:\n%s", second)
	}
	if n := strings.Count(second, `data-type="`+cms.FileViewType+`"`); n != 1 {
		t.Fatalf("⚠ マーカーが %d 個あります（1個を期待）:\n%s", n, second)
	}
	if strings.Contains(second, "000041-aaaa") || !strings.Contains(second, "000041-bbbb") {
		t.Errorf("⚠ 新しいPDFを指していません:\n%s", second)
	}
	// ⚠ **同じものなら書きません**（版と更新日時を無駄に進めない）。
	if again, ok := placeOrderPDFView(second, "000041-bbbb"); ok || again != second {
		t.Errorf("⚠ 同じ参照で書き換えています:\n%s", again)
	}
}

// TestOrderPDFViewNeedsTheTable は、⚠ **置き場所が無いときに黙らない**ことを
// 固定します。
func TestOrderPDFViewNeedsTheTable(t *testing.T) {
	if out, ok := placeOrderPDFView(`<h1>ただのページ</h1><p>本文</p>`, "000041-aaaa"); ok {
		t.Errorf("⚠ 発注明細の無いページにマーカーを置いています:\n%s", out)
	}
}

// showPage はページを鏡ごしに描きます。
func showPage(t *testing.T, viewer *auth.User, pageID int, body string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	if viewer != nil {
		req = auth.WithUser(req, viewer)
	}
	return cms.RenderComputedViews(req, pageID, body)
}
