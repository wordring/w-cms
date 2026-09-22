package subcon

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
