package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// 未手配の一覧（2026-09-21）——発注書を作る起点です。
//
// ⚠ **受注を横断します。** ユーザー:「弊社の発注書は、**受注明細の単位とは無関係に、
// 納期のグループなどから発行**されます」。だから番人も「1つの受注ページの中で
// 正しいか」ではなく、**全社を読んでいるか**を見ます。
//
// 下ごしらえは [procurement_test.go] の `seedProcurement` を借ります
// （受注30／加工製品31／発注書32。⚠ **発注書はどことも繋がっていません**）。

// TestUnorderedLeavesOutWhatIsOrdered は、⚠ **手配済みを出さない**ことと
// **未手配を落とさない**ことを、同じ下ごしらえで一度に固定します。
//
// ⚠ **片方だけでは足りません**——「全部出す」でも「全部出さない」でも、
// 片方の試験は通ってしまいます。
func TestUnorderedLeavesOutWhatIsOrdered(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)

	list, err := UnorderedItems(&auth.User{Username: "root", IsAdmin: true})
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	// 板 t3.2 は 必要6・発注済6 → 残0 なので出ない。FB t4.5 は 必要3・発注0 → 残3。
	if len(list) != 1 {
		t.Fatalf("未手配が %d 件です（1件のはず）: %#v", len(list), list)
	}
	u := list[0]
	if !strings.Contains(u.Name, "t4.5") {
		t.Errorf("⚠ 手配済みのほうが出ています: %#v", u)
	}
	if u.Remaining != 3 {
		t.Errorf("残は3のはずです: %#v", u)
	}
	if u.ProductPageID != 31 || u.OrderPageID != 30 {
		t.Errorf("辿り先が違います: %#v", u)
	}
	// ⚠ **材質・形状・寸法が埋まること。** これが無いと発注書に書けず、
	//    品名（3つの焼き直し）を書く羽目になります。
	if u.Material != "鉄" || u.Shape != "FB" || u.Size != "t4.5*75*1090" {
		t.Errorf("⚠ 材料の3つ組が埋まっていません: %#v", u)
	}
}

// TestUnorderedSkipsDoneLines は、⚠ **完了した受注明細は手配の対象ではない**ことを
// 固定します（受注残表と同じ線引き）。
func TestUnorderedSkipsDoneLines(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	syncBody(t, 30, `<h1>受注</h1>`+
		`<table data-type="`+clientOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>状態</th></tr>`+
		`<tr><td>000031</td><td>K-1</td><td>ブラケット</td><td>3</td><td>`+StatusDone+`</td></tr>`+
		`</tbody></table>`)

	list, err := UnorderedItems(&auth.User{Username: "root", IsAdmin: true})
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("⚠ 完了した行から未手配が出ています: %#v", list)
	}
}

// TestUnorderedHidesUnreadableProducts は、⚠ **読めない加工製品は出さない**ことを
// 固定します。
//
// ⚠ 一覧は**全社の受注明細**を読みます——絞りを外すと、読めないはずの品名・寸法が
// **他人の画面に並びます**。しかもエラーは出ません。
//
// ⚠ **「0件になること」だけを見ません。** それでは「そもそも引けていない」と
// 区別が付かないので、**同じ種まきで root には1件出ること**も一緒に見ます。
func TestUnorderedHidesUnreadableProducts(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 51, 0, "ブラケット", "alice", "300", false) // ⚠ alice 専有
	addPage(t, 50, 0, "受注", "root", "302", true)
	syncBody(t, 51, `<h1>ブラケット</h1>`+
		`<table data-type="`+partMaterialsType+`"><tbody>`+
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>`+
		`<tr><td>鉄</td><td>FB</td><td>t4.5*75*1090</td><td>1</td></tr>`+
		`</tbody></table>`)
	syncBody(t, 50, `<h1>受注</h1>`+
		`<table data-type="`+clientOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th></tr>`+
		`<tr><td>000051</td><td>K-1</td><td>ブラケット</td><td>3</td></tr>`+
		`</tbody></table>`)

	// まず、引けていることを確かめる（静けさと成功を区別するため）。
	if list, err := UnorderedItems(&auth.User{Username: "alice"}); err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	} else if len(list) != 1 {
		t.Fatalf("前提が崩れています: 持ち主には1件出るはずです: %#v", list)
	}

	mallory := &auth.User{Username: "mallory"}
	if page.GetPerms(51).CanRead(mallory) {
		t.Fatal("前提が崩れています: mallory は加工製品を読めてはいけません")
	}
	list, err := UnorderedItems(mallory)
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("⚠ 読めない加工製品の中身が出ています: %#v", list)
	}
}

// TestSortUnorderedPutsUndatedFirst は、⚠ **日付として読めない納期を落とさず先頭へ**
// 置くことを固定します。
//
// ⚠ `最短納期`・`都度` は**急ぎの合図**として書かれます。後ろへ回すと見落とし、
// 落とすと「書いてあるのに出てこない」になります。
func TestSortUnorderedPutsUndatedFirst(t *testing.T) {
	list := []UnorderedItem{
		{Due: "2026-10-01", Name: "あ"},
		{Due: "最短納期", Name: "い"},
		{Due: "2026-09-25", Name: "う"},
		{Due: "", Name: "え"},
	}
	sortUnordered(list)
	got := make([]string, len(list))
	for i, u := range list {
		got[i] = u.Name
	}
	want := []string{"え", "い", "う", "あ"} // 空 → 最短納期 → 9/25 → 10/1
	if strings.Join(got, "") != strings.Join(want, "") {
		t.Errorf("並びが %v です（%v を期待）", got, want)
	}
}

// TestSortUnorderedGroupsByMachine は、⚠ **同じ納期の中は装置順**であることを
// 固定します（2026-09-22 ユーザー）。
//
// ⚠ **納期より先に装置で並べてはいけません**——発注をまとめる単位は納期です。
// だから「納期が違えば装置を見ない」ことも一緒に見ます。
func TestSortUnorderedGroupsByMachine(t *testing.T) {
	list := []UnorderedItem{
		{Due: "2026-10-01", Machine: "B装置", Name: "あ"},
		{Due: "2026-09-25", Machine: "B装置", Name: "い"},
		{Due: "2026-10-01", Machine: "A装置", Name: "う"},
		{Due: "2026-09-25", Machine: "A装置", Name: "え"},
		// ⚠ **装置が空のものは後ろ**（読めなかったものを先頭に置かない）。
		{Due: "2026-09-25", Machine: "", Name: "お"},
	}
	sortUnordered(list)
	got := make([]string, len(list))
	for i, u := range list {
		got[i] = u.Name
	}
	// 9/25（A → B → 空）→ 10/1（A → B）
	want := []string{"え", "い", "お", "う", "あ"}
	if strings.Join(got, "") != strings.Join(want, "") {
		t.Errorf("並びが %v です（%v を期待）", got, want)
	}
}

// TestUnorderedSubtractsWhatIsInTheDraft は、⚠ **発注部材表に入れた分は一覧から
// 消える**ことを固定します（2026-09-22）。
//
// ユーザーの構想:「表作成ボタンをクリックすると発注部材表が開き、**すると元の表からは
// それらの行が消えます**」。
//
// ⚠ **1段目の実装はこれを落としていて、二重に出ていました**（実データで確認）——
// **同じものを2回発注しかねません。**
//
// ⚠ **「消えること」だけを見ません**——それでは「そもそも引けていない」と区別が
// 付かないので、**入れる前に出ていること**と**一部だけ入れたら残りが出ること**も見ます。
func TestUnorderedSubtractsWhatIsInTheDraft(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	root := &auth.User{Username: "root", IsAdmin: true}

	before, err := UnorderedItems(root)
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	if len(before) != 1 || before[0].Remaining != 3 {
		t.Fatalf("前提が崩れています（残3が1件のはず）: %#v", before)
	}

	// 発注部材表を持つページを1枚（⚠ **発注ページでなくてもよい**——横断で読みます）。
	addPage(t, 60, 0, "発注", "root", "302", true)
	syncBody(t, 60, `<h1>発注</h1>`+orderDraftHTML([]ourOrderLine{
		{ProductID: "000031", Material: "鉄", Shape: "FB", Size: "t4.5*75*1090", Quantity: "1"},
	}))

	mid, err := UnorderedItems(root)
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	// ⚠ **一部だけ入れたら、残りが出ること**（3 − 1 = 2）。
	if len(mid) != 1 || mid[0].Remaining != 2 {
		t.Fatalf("⚠ 引き算が効いていません（残2が1件のはず）: %#v", mid)
	}

	// 残りも入れたら、消えること。
	syncBody(t, 60, `<h1>発注</h1>`+orderDraftHTML([]ourOrderLine{
		{ProductID: "000031", Material: "鉄", Shape: "FB", Size: "t4.5*75*1090", Quantity: "3"},
	}))
	after, err := UnorderedItems(root)
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("⚠ 発注部材表に入れたのに一覧に残っています（二重に発注しかねません）: %#v", after)
	}
}

// TestUnorderedIncludesOutsourcing は、**外注加工と支給部品も必要部材表に出る**ことを固定します
// （2026-09-27・09-28 に種類ごとの運び方を設定へ）。
//
// 利用者:「外注加工の表を埋めてみましたが、発注ページに出ません」——集計が材料と購入部品だけを
// 数えていました。外注先へも発注書で頼むので、同じ道（必要部材表 → 発注部材表 → 発注書）に乗せます。
// ⚠ **支給部品も出します**（2026-09-28 利用者:「支給品は購入しませんが、支給願いという形で
// 発注します」——09-27 は「出さない」でした）。形はテンプレートと同じ「見出しの節＋キャプションの表」。
//
// 外注加工が運ぶ値は設定 `order_kinds` のとおり——品番は加工製品ページの `図面番号` タグ、
// 品名は `名称` が空なら `図面名称` タグ。手配済みの鍵は **種類＋番号＋加工内容**です
// （1つの加工製品に外注加工が複数あり得るので番号で分ける・利用者 09-28）。
func TestUnorderedIncludesOutsourcing(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	syncBody(t, 31, `<h1>ブラケット</h1>`+
		`<dl data-type="tags"><dt>図面番号</dt><dd>A100-B01-001</dd>`+
		`<dt>図面名称</dt><dd>ブラケット</dd></dl>`+
		`<section><h2>外注加工</h2><table><caption>外注加工</caption><tbody>`+
		`<tr><th>番号</th><th>名称</th><th>加工内容</th><th>材質</th><th>表面</th><th>個数</th><th>支給</th><th>推奨業者</th><th>資料</th><th>備考</th><th>区分</th></tr>`+
		`<tr><td>1</td><td></td><td>レーザー切断</td><td>SS400</td><td></td><td>1</td><td>材料支給</td><td>ひかりレーザー</td><td></td><td></td><td></td></tr>`+
		`<tr><td>2</td><td>カバー</td><td>塗装</td><td></td><td>緑</td><td>2</td><td></td><td></td><td></td><td></td><td></td></tr>`+
		`</tbody></table></section>`+
		`<section><h2>支給部品</h2><table><caption>支給部品</caption><tbody>`+
		`<tr><th>品名</th><th>仕様</th><th>個数</th><th>備考</th><th>区分</th></tr>`+
		`<tr><td>支給シャフト</td><td>φ20</td><td>1</td><td></td><td></td></tr>`+
		`</tbody></table></section>`)
	root := &auth.User{Username: "root", IsAdmin: true}

	list, err := UnorderedItems(root)
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	byWork := map[string]UnorderedItem{}
	var supplied *UnorderedItem
	for i := range list {
		if list[i].Kind == "外注加工" {
			byWork[list[i].Values["加工内容"]] = list[i]
		}
		if list[i].Kind == "支給部品" {
			supplied = &list[i]
		}
	}
	laser, ok := byWork["レーザー切断"]
	if !ok {
		t.Fatalf("外注加工が必要部材表に出ていません: %#v", list)
	}
	if laser.Remaining != 3 || laser.ProductPageID != 31 {
		t.Errorf("外注加工の行が違います（残＝1個×受注3・弊社品番）: %#v", laser)
	}
	// ⚠ **品番と品名はタグから来ること**——名称が空なら図面名称（外注先の紙に図番と名前が要る）。
	if laser.Values["番号"] != "1" || laser.Values["品番"] != "A100-B01-001" ||
		laser.Values["品名"] != "ブラケット" || laser.Values["材質"] != "SS400" ||
		laser.Values["支給"] != "材料支給" {
		t.Errorf("⚠ 外注加工の運ぶ値が違います: %#v", laser.Values)
	}
	if laser.Name != "1 ブラケット レーザー切断" {
		t.Errorf("必要部材表の名前は 番号・品名・加工内容・表面 のはずです: %q", laser.Name)
	}
	paint, ok := byWork["塗装"]
	if !ok {
		t.Fatalf("2つ目の外注加工が出ていません: %#v", list)
	}
	if paint.Values["品名"] != "カバー" || paint.Values["表面"] != "緑" || paint.Remaining != 6 {
		t.Errorf("⚠ 名称と表面が運ばれていません（名称があれば図面名称より先）: %#v", paint)
	}
	// ⚠ 推奨業者は運ばない（発注の表の列ではない——設定の columns に無い）。
	if _, carried := laser.Values["推奨業者"]; carried {
		t.Errorf("⚠ 推奨業者まで運んでいます: %#v", laser.Values)
	}
	if supplied == nil {
		t.Fatalf("⚠ 支給部品（支給願い）が必要部材表に出ていません: %#v", list)
	}
	if supplied.Name != "支給シャフト φ20" || supplied.Remaining != 3 {
		t.Errorf("支給部品の行が違います: %#v", *supplied)
	}

	// 発注書に同じ種類・番号・加工内容で入れれば、手配済みとして消える（発注部材表・発注書と同じ鍵）。
	// ⚠ 番号が違えば別の外注加工——塗装は残る。
	addPage(t, 33, 0, "発注 ひかりレーザー", "root", "302", true)
	syncBody(t, 33, `<h1>発注 ひかりレーザー</h1>`+
		`<dl data-type="tags"><dt>`+SupplierTag+`</dt><dd>ひかりレーザー</dd></dl>`+
		`<table data-type="`+ourOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>種類</th><th>番号</th><th>品名</th><th>加工内容</th><th>数量</th></tr>`+
		`<tr><td>000031</td><td>外注加工</td><td>1</td><td>ブラケット</td><td>レーザー切断</td><td>3</td></tr>`+
		`</tbody></table>`)
	list, err = UnorderedItems(root)
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	var stillPaint bool
	for _, u := range list {
		if u.Kind == "外注加工" && u.Values["加工内容"] == "レーザー切断" {
			t.Errorf("⚠ 発注書に入れた外注加工がまだ出ています: %#v", u)
		}
		if u.Kind == "外注加工" && u.Values["加工内容"] == "塗装" {
			stillPaint = true
		}
	}
	if !stillPaint {
		t.Errorf("⚠ 番号の違う外注加工（塗装）まで消えています: %#v", list)
	}
}

// TestOrderRowKeyInfersLegacyKind は、**種類の列が無い古い発注明細**を、鍵の列のどれかに値がある
// 最初の種類として読むことを固定します（2026-09-28 より前の紙——材料は材質・形状・寸法、
// 購入部品は品名）。⚠ 読めないと、既に発注した部材が必要部材表に戻ります（二重発注）。
func TestOrderRowKeyInfersLegacyKind(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	syncBody(t, 31, `<h1>ブラケット</h1>`+
		`<table><caption>購入部品</caption><tbody>`+
		`<tr><th>品名</th><th>仕様</th><th>個数</th></tr>`+
		`<tr><td>六角ボルト</td><td></td><td>4</td></tr>`+
		`</tbody></table>`)
	syncBody(t, 32, `<h1>発注 みなと商店</h1>`+
		`<dl data-type="tags"><dt>`+SupplierTag+`</dt><dd>みなと商店</dd></dl>`+
		`<table data-type="`+ourOrderItemsType+`"><tbody>`+
		`<tr><th>弊社品番</th><th>品名</th><th>数量</th></tr>`+
		`<tr><td>000031</td><td>六角ボルト</td><td>12</td></tr>`+
		`</tbody></table>`)
	list, err := UnorderedItems(&auth.User{Username: "root", IsAdmin: true})
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	for _, u := range list {
		if u.Kind == "購入部品" {
			t.Errorf("⚠ 種類の無い古い発注明細が購入部品として数えられていません: %#v", u)
		}
	}
}

// TestUnorderedLeavesOutObsoleteRows は、**区分が「廃版」の行を必要部材表に出さない**ことを
// 固定します（2026-09-27 利用者:「廃版は含まなくてよいと思います」）。材料・購入部品・外注加工とも。
func TestUnorderedLeavesOutObsoleteRows(t *testing.T) {
	setupMaterialsPermsTest(t)
	seedProcurement(t, "root", "302", true)
	syncBody(t, 31, `<h1>ブラケット</h1>`+
		`<table><caption>材料</caption><tbody>`+
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th><th>区分</th></tr>`+
		`<tr><td>鉄</td><td>FB</td><td>t4.5*75*1090</td><td>1</td><td>現行</td></tr>`+
		`<tr><td>鉄</td><td>丸棒</td><td>φ20*300</td><td>1</td><td>廃版</td></tr>`+
		`</tbody></table>`+
		`<table><caption>購入部品</caption><tbody>`+
		`<tr><th>品名</th><th>仕様</th><th>個数</th><th>区分</th></tr>`+
		`<tr><td>古いボルト</td><td></td><td>4</td><td>廃版</td></tr>`+
		`</tbody></table>`+
		`<table><caption>外注加工</caption><tbody>`+
		`<tr><th>加工内容</th><th>個数</th><th>区分</th></tr>`+
		`<tr><td>古い曲げ</td><td>1</td><td>廃版</td></tr>`+
		`</tbody></table>`)

	list, err := UnorderedItems(&auth.User{Username: "root", IsAdmin: true})
	if err != nil {
		t.Fatalf("UnorderedItemsエラー: %v", err)
	}
	var names []string
	for _, u := range list {
		names = append(names, u.Name)
		if strings.Contains(u.Name, "丸棒") || u.Name == "古いボルト" || u.Name == "古い曲げ" {
			t.Errorf("⚠ 廃版の行が必要部材表に出ています: %#v", u)
		}
	}
	if len(list) != 1 || !strings.Contains(list[0].Name, "t4.5") {
		t.Errorf("現行の行だけが出るはずです: %v", names)
	}
}
