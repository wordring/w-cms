package subcon

import (
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// **受注残表**（2026-09-21）。ユーザー:「受注フォルダのトップページには、**納期順の
// 受注残表**が必要です。顧客、納期ごとに別の表として分けて、ワンタッチで印刷も」。
//
// ⚠ **顧客×納期の組が、そのまま納品の単位**です（ユーザー決定）。

// seedOrder は受注ページを1枚、索引に作ります（親は root）。
func seedOrder(t *testing.T, idInt, rootInt int, client, due string, rows string) {
	t.Helper()
	addPage(t, idInt, rootInt, "受注", "alice", "302", true)
	body := `<h1>受注</h1><dl data-type="tags">` +
		`<dt>発注書番号</dt><dd>PO-` + page.FormatID(idInt) + `</dd>` +
		`<dt>発注元</dt><dd>` + client + `</dd>` +
		`<dt>納期</dt><dd>` + due + `</dd></dl>` +
		`<table data-type="` + clientOrderItemsType + `"><caption>受注明細</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th>` +
		`<th>単価</th><th>納期</th><th>出荷済み</th><th>備考</th><th>状態</th></tr>` +
		rows + `</tbody></table>`
	if err := cms.SyncIndex(page.FormatID(idInt), body); err != nil {
		t.Fatalf("SyncIndex(%d): %v", idInt, err)
	}
}

// item は明細1行を組みます。
func item(itemNo, name, qty, due, shipped string) string {
	return `<tr><td></td><td>` + itemNo + `</td><td>` + name + `</td><td>` + qty +
		`</td><td>個</td><td>100</td><td>` + due + `</td><td>` + shipped +
		`</td><td></td><td>未着手</td></tr>`
}

func adminUser() *auth.User { return &auth.User{Username: "root", IsAdmin: true} }

// TestBacklogGroupsByClientAndDue は、**顧客×納期の組ごとに1枚**になることを固定します。
func TestBacklogGroupsByClientAndDue(t *testing.T) {
	setupExtTest(t, "000100", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 101, -1, "受注", "alice", "302", true) // 箱
	seedOrder(t, 102, 101, "南北", "2026-10-15",
		item("A-1", "ブラケット", "100", "2026-10-15", ""))
	seedOrder(t, 103, 101, "南北", "2026-10-20",
		item("A-2", "カバー", "50", "2026-10-20", ""))
	seedOrder(t, 104, 101, "しらかば", "2026-10-15",
		item("B-1", "シャフト", "10", "2026-10-15", ""))

	gs := backlogGroups(adminUser(), 101)
	if len(gs) != 3 {
		t.Fatalf("組が %d です（顧客×納期で3組を期待）: %+v", len(gs), gs)
	}
	// ⚠ **納期順**（同じ納期なら顧客名順）。
	want := [][2]string{{"南北", "2026-10-15"}, {"しらかば", "2026-10-15"}, {"南北", "2026-10-20"}}
	for i, w := range want {
		if gs[i].Client != w[0] || gs[i].Due != w[1] {
			t.Errorf("%d番目が %q %q です（%q %q を期待）", i, gs[i].Client, gs[i].Due, w[0], w[1])
		}
	}
}

// TestBacklogDropsShippedRows は、**出し切った行を出さない**ことを固定します。
//
// ⚠ 全部出すと納品済みに埋もれて「あと何を作るか」が見えません——**受注残表の役目は
// そこだけ**です。
func TestBacklogDropsShippedRows(t *testing.T) {
	setupExtTest(t, "000110", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 111, -1, "受注", "alice", "302", true)
	seedOrder(t, 112, 111, "南北", "2026-10-15",
		item("A-1", "出し切った", "100", "2026-10-15", "100")+
			item("A-2", "半分だけ", "100", "2026-10-15", "40"))

	gs := backlogGroups(adminUser(), 111)
	if len(gs) != 1 || len(gs[0].Rows) != 1 {
		t.Fatalf("残っている行だけになっていません: %+v", gs)
	}
	r := gs[0].Rows[0]
	if r.ItemName != "半分だけ" || r.Remaining != 60 {
		t.Errorf("⚠ 分納の残が違います: %+v（残60を期待）", r)
	}
}

// TestBacklogPutsUnreadableDueFirst は、⚠ **日付として読めない納期を埋めない**ことを
// 固定します。
//
// ⚠ **実データの1通目が「最短納期」でした。** いちばん急ぐものを最後に置くと
// **見落とします**。日付でないものは先頭へ出し、画面でも断ります。
func TestBacklogPutsUnreadableDueFirst(t *testing.T) {
	setupExtTest(t, "000120", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 121, -1, "受注", "alice", "302", true)
	seedOrder(t, 122, 121, "南北", "2026-10-15",
		item("A-1", "ブラケット", "100", "2026-10-15", ""))
	seedOrder(t, 123, 121, "南北", "最短納期",
		item("A-9", "大至急", "5", "最短納期", ""))

	gs := backlogGroups(adminUser(), 121)
	if len(gs) != 2 {
		t.Fatalf("組が %d です: %+v", len(gs), gs)
	}
	if gs[0].Due != "最短納期" {
		t.Errorf("⚠ 日付として読めない納期を後ろへ埋めています: %+v", gs)
	}
	// 画面でも断ること。
	req := httptest.NewRequest("GET", "/000121", nil)
	req = auth.WithUser(req, adminUser())
	out := cms.RenderComputedViews(req, 121,
		`<h1>受注</h1><section data-type="`+BacklogViewType+`"></section>`)
	if !strings.Contains(out, "日付として読めない納期") {
		t.Errorf("画面で断っていません:\n%s", out)
	}
}

// TestBacklogScopeIsDescendantsOnly は、⚠ **置いたページの子孫だけ**を見ることを
// 固定します。
//
// ⚠ 箱を名前で決め打ちすると、置き場所を変えた日に**黙って空**になります。
func TestBacklogScopeIsDescendantsOnly(t *testing.T) {
	setupExtTest(t, "000130", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 131, -1, "受注", "alice", "302", true)
	addPage(t, 132, -1, "よその箱", "alice", "302", true)
	seedOrder(t, 133, 131, "南北", "2026-10-15", item("A-1", "中", "10", "2026-10-15", ""))
	seedOrder(t, 134, 132, "南北", "2026-10-15", item("B-1", "外", "10", "2026-10-15", ""))

	gs := backlogGroups(adminUser(), 131)
	if len(gs) != 1 || len(gs[0].Rows) != 1 || gs[0].Rows[0].ItemName != "中" {
		t.Errorf("⚠ よその箱の受注まで拾っています: %+v", gs)
	}
}

// TestBacklogRespectsVisibility は、⚠ **読めない受注を混ぜない**ことを固定します
// （見せ分け・C案）。
//
// ⚠ **同じ閲覧者で2枚を比べます**——読める1枚は出て、読めない1枚は出ない。
// 片方だけ見ると「そもそも動いていない」を正しさと取り違えます（2026-09-21 に
// 結びの気づきで実際に空振りしました）。
func TestBacklogRespectsVisibility(t *testing.T) {
	setupExtTest(t, "000140", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 141, -1, "受注", "alice", "302", true)
	seedOrder(t, 142, 141, "見える", "2026-10-15", item("A-1", "見える品", "10", "2026-10-15", ""))
	seedOrder(t, 143, 141, "見えない", "2026-10-16", item("B-1", "秘密の品", "10", "2026-10-16", ""))
	// 143 だけ alice 専有にする。
	if _, err := dbExec(`UPDATE page_perms SET mode = '300', public = 0 WHERE page_id = 143`); err != nil {
		t.Fatalf("権限の更新: %v", err)
	}

	gs := backlogGroups(&auth.User{Username: "bob"}, 141)
	joined := ""
	for _, g := range gs {
		for _, r := range g.Rows {
			joined += r.ItemName + " "
		}
	}
	if strings.Contains(joined, "秘密の品") {
		t.Errorf("⚠ 読めない受注を混ぜています: %q", joined)
	}
	if !strings.Contains(joined, "見える品") {
		t.Fatalf("読める受注まで落としています（そもそも動いていない疑い）: %q", joined)
	}
}

// TestBacklogViewSaysSoWhenEmpty は、**空のときに理由を言う**ことを固定します。
//
// ⚠ 無言の空白は「壊れた」と「まだ無い」の区別が付きません。
func TestBacklogViewSaysSoWhenEmpty(t *testing.T) {
	setupExtTest(t, "000150", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 151, -1, "受注", "alice", "302", true)

	req := httptest.NewRequest("GET", "/000151", nil)
	req = auth.WithUser(req, adminUser())
	out := cms.RenderComputedViews(req, 151,
		`<h1>受注</h1><section data-type="`+BacklogViewType+`"></section>`)
	if !strings.Contains(out, "受注残はありません") {
		t.Errorf("空の理由を言っていません:\n%s", out)
	}
}

// TestBacklogHasPrintButtonPerSheet は、**表ごとに印刷ボタンが出る**ことを固定します
// （ユーザー:「表ごとに印刷ボタン」）。
func TestBacklogHasPrintButtonPerSheet(t *testing.T) {
	setupExtTest(t, "000160", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 161, -1, "受注", "alice", "302", true)
	seedOrder(t, 162, 161, "南北", "2026-10-15", item("A-1", "甲", "10", "2026-10-15", ""))
	seedOrder(t, 163, 161, "しらかば", "2026-10-16", item("B-1", "乙", "10", "2026-10-16", ""))

	req := httptest.NewRequest("GET", "/000161", nil)
	req = auth.WithUser(req, adminUser())
	out := cms.RenderComputedViews(req, 161,
		`<h1>受注</h1><section data-type="`+BacklogViewType+`"></section>`)
	if n := strings.Count(out, "data-backlog-print="); n != 2 {
		t.Errorf("印刷ボタンが %d 個です（表2枚ぶんの2を期待）:\n%s", n, out)
	}
	// ⚠ **クロームであること**——本文には保存されません。
	if !strings.Contains(out, "vocab-chrome") {
		t.Errorf("クロームに包まれていません:\n%s", out)
	}
}

// dbExec は試験の中で権限などを直に直すための薄い包みです。
func dbExec(q string, args ...any) (any, error) { return database.DB.Exec(q, args...) }

// TestBacklogDropsDoneRows は、⚠ **人が「完了」と言った行を出さない**ことを
// 固定します。
//
// ユーザー:「受注トップページの受注残集計表には、**完了状態の行は入れない**ように
// しましょう」（2026-09-21）。
//
// ⚠ **数が残っていても落とします**——`完了` は「どこまで進んだか」ではなく
// **「もう受注残に出さない」という人の宣言**だからです。**機械の数より人の判断が上。**
func TestBacklogDropsDoneRows(t *testing.T) {
	setupExtTest(t, "000170", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 171, -1, "受注", "alice", "302", true)
	// 3行とも**数の上では残っています**（出荷済みは空）。
	body := item("A-1", "まだ作る", "10", "2026-10-15", "") +
		strings.Replace(item("A-2", "取りやめ", "10", "2026-10-15", ""),
			"<td>未着手</td>", "<td>"+StatusDone+"</td>", 1) +
		strings.Replace(item("A-3", "納めた", "10", "2026-10-15", ""),
			"<td>未着手</td>", "<td>納品済</td>", 1)
	seedOrder(t, 172, 171, "南北", "2026-10-15", body)

	gs := backlogGroups(adminUser(), 171)
	got := map[string]bool{}
	for _, g := range gs {
		for _, r := range g.Rows {
			got[r.ItemName] = true
		}
	}
	if got["取りやめ"] {
		t.Errorf("⚠ 完了の行を出しています: %+v", gs)
	}
	if !got["まだ作る"] {
		t.Fatalf("残っている行まで落としています（そもそも動いていない疑い）: %+v", gs)
	}
	// ⚠ **`納品済` は落としません**——出したという事実で、残数があればまだ作ります
	// （半分だけ納めた行がそれです）。`完了` とは別物、という線引きの番人です。
	if !got["納めた"] {
		t.Errorf("⚠ `納品済` を `完了` と同じに扱っています（分納の残りが消えます）: %+v", gs)
	}
}

// TestBacklogStatusEnumHasDone は、**選択肢に「完了」がある**ことを固定します。
//
// ⚠ 除外だけ入れて選択肢を足し忘れると、**人が押す手段が画面にありません**
// （一覧に無い値も保存はされますが、打ち込むしかなくなります）。
func TestBacklogStatusEnumHasDone(t *testing.T) {
	def, ok := cms.VocabDefByType(clientOrderItemsType)
	if !ok {
		t.Fatal("受注明細の宣言がありません")
	}
	for _, c := range def.Columns {
		if c.Label != "状態" {
			continue
		}
		for _, v := range c.Enum {
			if v == StatusDone {
				return
			}
		}
		t.Fatalf("⚠ 状態の選択肢に %q がありません（押す手段が画面にありません）: %v", StatusDone, c.Enum)
	}
	t.Fatal("状態の列がありません")
}

// TestBacklogCellsCarryWrapClasses は、⚠ **折り返しの印をサーバーが付ける**ことを
// 固定します。
//
// ⚠ **本文の表とは経路が違います。** `app.js` の `validateTypedTables` は
// **サーバー所有の表を意図的に飛ばす**ので（クロームは殻の持ち物）、ここで付けないと
// 受注残表だけ全列が折り返します。
func TestBacklogCellsCarryWrapClasses(t *testing.T) {
	setupExtTest(t, "000180", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 181, -1, "受注", "alice", "302", true)
	seedOrder(t, 182, 181, "南北", "2026-10-15",
		item("A-1", "とても長い品名がここに入ります", "10", "2026-10-15", ""))

	req := httptest.NewRequest("GET", "/000181", nil)
	req = auth.WithUser(req, adminUser())
	out := cms.RenderComputedViews(req, 181,
		`<h1>受注</h1><section data-type="`+BacklogViewType+`"></section>`)

	if !strings.Contains(out, `class="cell-atomic"`) {
		t.Errorf("⚠ 折り返さない印がありません（全列が折り返します）:\n%s", out)
	}
	// ⚠ **備考だけは折り返す。** 自由文なので1行に保つと表が果てしなく伸びます。
	if strings.Count(out, `class="cell-wrap"`) != 1 {
		t.Errorf("備考の折り返しの印が %d 個です（1行ぶんの1を期待）:\n%s",
			strings.Count(out, `class="cell-wrap"`), out)
	}
}
