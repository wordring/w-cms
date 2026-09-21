package subcon

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 材料の参考単価（2026-09-21）。ユーザー:「材料の単価は**検索して最新情報を
// ひくべきもの**で、本来はここにあるべきものではありません」。
//
// ⚠ **鏡は空振りしやすいところです**——引き金が立たなければ、番人は「合っている」
// ではなく**ただの静けさ**を見ます。だからここでは「⚠ が出ないこと」ではなく
// **「引けたときに数字が出ること」**と**「引けないときにそう言うこと」**の両方を見ます。

// 土台は materials_perms_test.go の helper を借ります（同じパッケージ・ファイルDB）。
// ⚠ ファイルDBなのは `page.CanView` を通るためです——`:memory:` は接続ごとに別のDBです。

// seedMaterialAndOrder は、材料ページ（10）と発注書ページを用意します。
func seedMaterialAndOrder(t *testing.T, materialRows string, orders ...orderSeed) {
	t.Helper()
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 10, 0, "加工製品", "root", "302", true)
	syncBody(t, 10, `<h1>加工製品</h1>`+
		`<dl data-type="tags"><dt>部品番号</dt><dd>P-1</dd></dl>`+
		`<table data-type="`+partMaterialsType+`"><tbody>`+
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>`+
		materialRows+`</tbody></table>`)
	for _, o := range orders {
		addPage(t, o.ID, 0, "発注書", o.Owner, o.Mode, o.Public)
		syncBody(t, o.ID, o.Body())
	}
}

type orderSeed struct {
	ID       int
	Owner    string
	Mode     string
	Public   bool
	Date     string
	Supplier string
	Rows     string // <tr><td>材質</td><td>形状</td><td>寸法</td><td>単価</td></tr>
}

func (o orderSeed) Body() string {
	return `<h1>発注書</h1>` +
		`<dl data-type="tags">` +
		`<dt>` + OrderedAtTag + `</dt><dd>` + o.Date + `</dd>` +
		`<dt>` + SupplierTag + `</dt><dd>` + o.Supplier + `</dd></dl>` +
		`<table data-type="` + ourOrderItemsType + `"><tbody>` +
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>単価</th></tr>` +
		o.Rows + `</tbody></table>`
}

// syncBody は本文を索引まで通します（本番と同じ道）。⚠ 名前を `sync` にはできません（標準の `sync` パッケージと衝突します）。
func syncBody(t *testing.T, id int, body string) {
	t.Helper()
	if err := cms.SyncIndex(fmt.Sprintf("%06d", id), body); err != nil {
		t.Fatalf("SyncIndex(%d)エラー: %v", id, err)
	}
}

// showMaterials は材料ページを鏡ごしに描きます。
func showMaterials(t *testing.T, viewer *auth.User, body string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/000010", nil)
	if viewer != nil {
		req = auth.WithUser(req, viewer)
	}
	return cms.RenderComputedViews(req, 10, body)
}

func materialsBody(rows string) string {
	return `<h1>加工製品</h1>` +
		`<table data-type="` + partMaterialsType + `"><tbody>` +
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>` +
		rows + `</tbody></table>`
}

// TestMaterialPriceShowsLatestPurchase は、⚠ **引けたときに数字が出る**ことを
// 固定します。**この番人がいちばん大事です**——「⚠ が出ない」だけを見る試験は、
// 鏡が一度も走っていなくても通ります。
func TestMaterialPriceShowsLatestPurchase(t *testing.T) {
	setupMaterialsPermsTest(t)
	rows := `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>2</td></tr>`
	seedMaterialAndOrder(t, rows, orderSeed{
		ID: 20, Owner: "root", Mode: "302", Public: true,
		Date: "2026-08-19", Supplier: "みなと商店",
		Rows: `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>800</td></tr>`,
	})

	got := showMaterials(t, &auth.User{Username: "root", IsAdmin: true}, materialsBody(rows))
	for _, want := range []string{"最新単価", "800円", "2026-08-19", "みなと商店"} {
		if !strings.Contains(got, want) {
			t.Fatalf("⚠ 鏡が走っていないか、%q が出ていません:\n%s", want, got)
		}
	}
	// ⚠ **本文ではなくクロームであること。** `vocab-chrome` が無いと、人が画面の表を
	//    コピーして貼ったとき**本当の列として保存されます**（`class` はサニタイズで
	//    落ちるので、貼られた時点では見分けが付かなくなる）。
	if !strings.Contains(got, `class="vocab-chrome mat-price"`) {
		t.Errorf("⚠ 足した列にクロームの印がありません（本文に焼き付く形です）:\n%s", got)
	}
	// ⚠ **出所は消さないこと。** 値段だけ出すと「いつの・誰からの値段か分からない数」
	//    になり、ワンノートの `単価（ロット1）みなと` と同じ問題を作り直します。
	if !strings.Contains(got, `class="mat-price-src"`) {
		t.Errorf("⚠ 出所（時点・仕入先）の欄がありません:\n%s", got)
	}
}

// TestMaterialPricePrefersNewerOrder は、**新しい発注を採る**ことを固定します。
func TestMaterialPricePrefersNewerOrder(t *testing.T) {
	setupMaterialsPermsTest(t)
	rows := `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>2</td></tr>`
	seedMaterialAndOrder(t, rows,
		orderSeed{ID: 20, Owner: "root", Mode: "302", Public: true,
			Date: "2025-11-02", Supplier: "たちばな鋼材",
			Rows: `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>760</td></tr>`},
		orderSeed{ID: 21, Owner: "root", Mode: "302", Public: true,
			Date: "2026-08-19", Supplier: "みなと商店",
			Rows: `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>800</td></tr>`},
	)

	got := showMaterials(t, &auth.User{Username: "root", IsAdmin: true}, materialsBody(rows))
	if !strings.Contains(got, "800円") || !strings.Contains(got, "みなと商店") {
		t.Fatalf("新しいほうの発注を採っていません:\n%s", got)
	}
	if strings.Contains(got, "760円") {
		t.Errorf("⚠ 古い発注の単価が出ています:\n%s", got)
	}
}

// TestMaterialPriceSaysWhenNothingBought は、⚠ **引けなかったことを黙らない**ことを
// 固定します。空欄にすると「単価が出ない」と「買ったことがない」が見分けられません。
func TestMaterialPriceSaysWhenNothingBought(t *testing.T) {
	setupMaterialsPermsTest(t)
	rows := `<tr><td>SUS304</td><td>板</td><td>t1.5</td><td>4</td></tr>`
	seedMaterialAndOrder(t, rows, orderSeed{
		ID: 20, Owner: "root", Mode: "302", Public: true,
		Date: "2026-08-19", Supplier: "みなと商店",
		Rows: `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>800</td></tr>`,
	})

	got := showMaterials(t, &auth.User{Username: "root", IsAdmin: true}, materialsBody(rows))
	if !strings.Contains(got, "⚠ 買った記録がありません") {
		t.Fatalf("引けなかったことを黙っています:\n%s", got)
	}
	if strings.Contains(got, "800円") {
		t.Errorf("⚠ 別の材料の単価を引き当てています:\n%s", got)
	}
}

// TestMaterialPriceRefusesEmptyKey は、⚠ **空の鍵で引き当てない**ことを固定します。
//
// ⚠ **`"" == ""` は偶然一致します**（変異試験で一度踏んだ形）。材質・形状・寸法が
// すべて空の材料行は実データに在るので、素直に突き合わせると**無関係な発注**に
// 当たります。
//
// ⚠ **そして、空欄の意味は1つではありません**（2026-09-21 にユーザー訂正を2度）:
//
//	①「**空の材料表は、買い置きの板を使うレーザー加工だけで作れるということで、
//	   材料は購入しないという意味です**」
//	②「**個数だけ書いてあるのは、加工製品一つに対して何個作るか示したかっただけで、
//	   材料を買うという意味ではありません。不適切な記入なので、あとで私がコツコツ
//	   修正していきます**」
//
// **同じ見た目の行に、違う事情が入っています。** 筆者は最初「⚠ …が空です」と
// **警告**で出し、次に「（材料を買わない）」と**意味を決めつけ**ました。どちらも
// 外れです——機械に分かるのは「引けない」ところまでで、その先は人の領分。
// ⚠ **警告にはしません**（直すべきものに見えてしまいます）。
func TestMaterialPriceRefusesEmptyKey(t *testing.T) {
	setupMaterialsPermsTest(t)
	rows := `<tr><td></td><td></td><td></td><td>2</td></tr>`
	seedMaterialAndOrder(t, rows, orderSeed{
		ID: 20, Owner: "root", Mode: "302", Public: true,
		Date: "2026-08-19", Supplier: "みなと商店",
		Rows: `<tr><td></td><td></td><td></td><td>800</td></tr>`,
	})

	got := showMaterials(t, &auth.User{Username: "root", IsAdmin: true}, materialsBody(rows))
	if strings.Contains(got, "800円") {
		t.Fatalf("⚠ 空の鍵で引き当てています:\n%s", got)
	}
	if !strings.Contains(got, "（材料の指定なし）") {
		t.Errorf("引けない理由（材料の指定が無いこと）を言っていません:\n%s", got)
	}
	if strings.Contains(got, "⚠") {
		t.Errorf("⚠ 意味のある空欄を警告として出しています（直すべきものに見えます）:\n%s", got)
	}
}

// TestMaterialPriceWaitsForMigrationCheck は、⚠ **移行の確認が済むまで表引きしない**
// ことを固定します（2026-09-21 ユーザー提案）。
//
// ワンノートの表には、同じ見た目で違う事情の行が混ざっています（材料を買わない行・
// 個数だけ書いてしまった行・ちゃんとした行）。⚠ **機械には見分けられません**。
// **人が確かめた印だけが、表引きしてよい根拠**です。
//
// ⚠ **印を付けるのは「未確認」のほう**——直したらタグごと消すので、**印の無いページが
// 正常**になります。⚠ **黙って引かないのではなく、そう言います**。
func TestMaterialPriceWaitsForMigrationCheck(t *testing.T) {
	setupMaterialsPermsTest(t)
	rows := `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>2</td></tr>`
	seedMaterialAndOrder(t, rows, orderSeed{
		ID: 20, Owner: "root", Mode: "302", Public: true,
		Date: "2026-08-19", Supplier: "みなと商店",
		Rows: `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>800</td></tr>`,
	})
	// 材料ページに「移行中」の印を付け直す。
	syncBody(t, 10, `<h1>加工製品</h1>`+
		`<dl data-type="tags"><dt>`+MigratingTag+`</dt><dd>確認待ち</dd></dl>`+
		`<table data-type="`+partMaterialsType+`"><tbody>`+
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>`+rows+`</tbody></table>`)

	body := `<h1>加工製品</h1>` +
		`<dl data-type="tags"><dt>` + MigratingTag + `</dt><dd>確認待ち</dd></dl>` +
		`<table data-type="` + partMaterialsType + `"><tbody>` +
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>` + rows + `</tbody></table>`
	got := showMaterials(t, &auth.User{Username: "root", IsAdmin: true}, body)

	if strings.Contains(got, "800円") {
		t.Fatalf("⚠ 移行の確認前なのに表引きしています:\n%s", got)
	}
	if !strings.Contains(got, "移行の確認前") {
		t.Errorf("引かない理由を黙っています:\n%s", got)
	}
	// ⚠ **列は足さないこと**——引かないのに見出しだけ出すと、全行が空に見えます。
	if strings.Contains(got, priceColLabel) {
		t.Errorf("⚠ 引かないのに「%s」の列を足しています:\n%s", priceColLabel, got)
	}
}

// TestMaterialPriceMapHasNoEmptyKey は、**集める側でも**空の鍵を落とすことを固定します。
//
// ⚠ **これは変異試験が見つけた穴です**（2026-09-21）。空の鍵の番人は2か所
// （集める側・描く側）にありますが、**集める側を外しても試験は全部通りました**
// ——描く側が先に止めるからです。「守りを外しても通る試験は、守りではなく別の何かを
// 測っています」。`latestMaterialPrices` は**別の読み手が来る口**（価格推移の
// ビューなど）なので、**出所で止まること**を直に見ます。
func TestMaterialPriceMapHasNoEmptyKey(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedMaterialAndOrder(t, `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>2</td></tr>`,
		orderSeed{ID: 20, Owner: "root", Mode: "302", Public: true,
			Date: "2026-08-19", Supplier: "みなと商店",
			Rows: `<tr><td></td><td></td><td></td><td>800</td></tr>`},
	)

	prices, err := latestMaterialPrices(database.DB, &auth.User{Username: "root", IsAdmin: true})
	if err != nil {
		t.Fatalf("latestMaterialPricesエラー: %v", err)
	}
	if _, bad := prices[""]; bad {
		t.Errorf("⚠ 空の鍵が地図に入っています（`\"\" == \"\"` で引き当てられます）: %#v", prices)
	}
}

// TestMaterialPriceHidesUnreadableOrders は、⚠ **読めない発注書の単価は出ない**ことを
// 固定します。材料表には誰でも行を書けるので、絞らないと**読めない発注書の単価と
// 仕入先が引けてしまいます**（設計総点検で踏んだ穴と同じ形）。
func TestMaterialPriceHidesUnreadableOrders(t *testing.T) {
	setupMaterialsPermsTest(t)
	rows := `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>2</td></tr>`
	seedMaterialAndOrder(t, rows, orderSeed{
		ID: 20, Owner: "alice", Mode: "300", Public: false,
		Date: "2026-08-19", Supplier: "みなと商店",
		Rows: `<tr><td>SS400</td><td>板</td><td>t3.2</td><td>800</td></tr>`,
	})

	mallory := &auth.User{Username: "mallory"}
	if page.GetPerms(20).CanRead(mallory) {
		t.Fatal("前提が崩れています: mallory は発注書を読めてはいけません")
	}
	got := showMaterials(t, mallory, materialsBody(rows))
	if strings.Contains(got, "800円") || strings.Contains(got, "みなと商店") {
		t.Fatalf("⚠ 読めない発注書の単価と仕入先が出ています:\n%s", got)
	}
	if !strings.Contains(got, "⚠ 買った記録がありません") {
		t.Errorf("欄そのものは出すこと（読めないことは知らせない・C案）:\n%s", got)
	}
}

// TestMaterialPriceKeyFoldsWidth は、鍵が**幅と半角カナの揺れを越える**ことを
// 固定します。⚠ ただし畳むのは `NormalizeText` までで、**長音やハイフンには
// 触りません**——寸法を強く畳むと別の寸法に当たります。
func TestMaterialPriceKeyFoldsWidth(t *testing.T) {
	wide := materialKeyOf("ＳＳ４００", "板", "ｔ3.2")
	narrow := materialKeyOf("SS400", "板", "t3.2")
	if wide != narrow {
		t.Errorf("全角の揺れが畳めていません: %q vs %q", wide, narrow)
	}
	if materialKeyOf("", "", "") != "" {
		t.Error("⚠ 空の3つ組が鍵になっています（空の鍵で引き当てる事故のもと）")
	}
	if materialKeyOf("SS400", "板", "t3-2") == materialKeyOf("SS400", "板", "t32") {
		t.Error("⚠ 寸法を強く畳みすぎています（別の寸法に当たります）")
	}
}
