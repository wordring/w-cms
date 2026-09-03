package sheetmetal

import (
	"database/sql"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 手配集計（RequiredMaterials）は部材の定義を部品番号だけで引くため、
// 「読めないページで定義された部材」まで集計に混ぜてしまっていた。
// 集計対象ページの read があれば通ってしまうので、
//   - 匿名は実効公開ページの受注ページを開くだけで、非公開ページ由来の部材名・仕入先が見える
//   - 認証済みユーザーは自分のページに任意の品番を書くだけで、読めないページの原価まで引ける
// という2つの越権が成立していた。ここでは定義元ページの read を要求することを固定する。

// setupMaterialsPermsTest はファイルDBで手配集計のテスト環境を用意します。
// :memory: を使わないのは、権限判定が行を回しながら別クエリを投げる形で、
// 別接続＝別の空DBを見てフェイルクローズしてしまうため（作業引き継ぎの勘所）。
func setupMaterialsPermsTest(t *testing.T) {
	t.Helper()
	origWd, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })

	dsn := filepath.ToSlash(filepath.Join(dir, "t.db")) +
		"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	database.DB = db
	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := cms.ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}
}

// addPage はページ1件とその権限を入れます。parentID が 0 未満なら親なし。
func addPage(t *testing.T, id, parentID int, title, owner, mode string, public bool) {
	t.Helper()
	if parentID < 0 {
		if _, err := database.DB.Exec(
			`INSERT INTO pages (id, title, file_path) VALUES (?, ?, '')`, id, title); err != nil {
			t.Fatalf("pages投入エラー: %v", err)
		}
	} else if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, file_path, parent_id) VALUES (?, ?, '', ?)`,
		id, title, parentID); err != nil {
		t.Fatalf("pages投入エラー: %v", err)
	}
	if _, err := database.DB.Exec(
		`INSERT INTO page_perms (page_id, owner, grp, mode, public) VALUES (?, ?, '', ?, ?)`,
		id, owner, mode, public); err != nil {
		t.Fatalf("page_perms投入エラー: %v", err)
	}
	// 正本（サイドカー）も書く——SyncIndex は権限をサイドカーから引き直すので、
	// これが無いと種まきの本文同期で権限が消える（派生だけ入れても本番と違う形）。
	parentStr := ""
	if parentID >= 0 {
		parentStr = fmt.Sprintf("%06d", parentID)
	}
	if err := page.WriteSidecar(fmt.Sprintf("%06d", id), page.PageMeta{
		Owner: owner, Mode: mode, Public: public, ParentID: parentStr,
	}); err != nil {
		t.Fatalf("サイドカー作成エラー: %v", err)
	}
}

// seedSecretMaterial は「非公開の部品定義ページ2」と「受注ページ」を用意します。
// 部材は SECRET-PART に紐づき、定義元はページ2（other に read なし）。
func seedSecretMaterial(t *testing.T, orderPageID int, orderPagePublic bool, orderOwner string) {
	t.Helper()
	// 0=トップ（公開）, 2=部品定義（非公開・alice専有）
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 2, 0, "部品定義", "alice", "300", false)
	addPage(t, orderPageID, 0, "受注", orderOwner, "302", orderPagePublic)

	// 種まきは**実本文を SyncIndex に通す**——索引へ直接書くより経路が長いが、
	// 見出し行の解決・タグの索引まで本番と同じ道を通る（拡張パッケージからは
	// コアの索引ヘルパが見えない、という事情もある）。
	sync := func(id int, body string) {
		t.Helper()
		pid := fmt.Sprintf("%06d", id)
		if err := cms.SyncIndex(pid, body); err != nil {
			t.Fatalf("SyncIndex(%d)エラー: %v", id, err)
		}
	}
	// 部材定義はページ2。部品番号タグ SECRET-PART が受注明細の品番と結ぶ。
	sync(2, `<h1>部品定義</h1>`+
		`<dl data-type="tags"><dt>部品番号</dt><dd>SECRET-PART</dd></dl>`+
		`<table data-type="part-materials"><tbody>`+
		`<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>`+
		`<tr><td>極秘部材</td><td></td><td></td><td>2</td></tr></tbody></table>`)
	sync(orderPageID, `<h1>受注</h1>`+
		`<section data-type="client-order"><dl>`+
		`<dt>発注書番号</dt><dd>PO-1</dd><dt>発注元</dt><dd>得意先</dd>`+
		`<dt>発注日</dt><dd>2026-08-20</dd></dl>`+
		`<table data-type="client-order-items"><tbody>`+
		`<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th></tr>`+
		`<tr><td>SECRET-PART</td><td>部品</td><td>100</td><td>3</td></tr></tbody></table></section>`)
}

// TestRequiredMaterialsHidesUnreadableDefinitions は、定義元ページを読めない
// 相手には部材が集計されないことを固定します。
func TestRequiredMaterialsHidesUnreadableDefinitions(t *testing.T) {
	setupMaterialsPermsTest(t)
	// 受注ページ3は mallory 所有。mallory はページ2（部品定義）を読めない。
	seedSecretMaterial(t, 3, false, "mallory")

	mallory := &auth.User{Username: "mallory"}
	if page.GetPerms(2).CanRead(mallory) {
		t.Fatal("前提が崩れています: mallory は部品定義ページを読めてはいけません")
	}

	list, err := RequiredMaterials(mallory, 3)
	if err != nil {
		t.Fatalf("RequiredMaterialsエラー: %v", err)
	}
	for _, m := range list {
		if m.MaterialName == "極秘部材" {
			t.Errorf("読めない定義元ページの部材が集計に出ました: %+v", m)
		}
	}

	// 定義元ページを読める alice には従来どおり出る（過剰に隠していないこと）。
	alice := &auth.User{Username: "alice"}
	list, err = RequiredMaterials(alice, 3)
	if err != nil {
		t.Fatalf("RequiredMaterialsエラー: %v", err)
	}
	found := false
	for _, m := range list {
		if m.MaterialName == "極秘部材" {
			found = true
			if m.TotalRequired != 6 { // 1個あたり2 × 受注3個
				t.Errorf("必要総数が想定と違います: %d (期待 6)", m.TotalRequired)
			}
		}
	}
	if !found {
		t.Error("定義元ページを読める相手には部材が出るべきです")
	}

	// admin は無条件に読める。
	list, err = RequiredMaterials(&auth.User{Username: "root", IsAdmin: true}, 3)
	if err != nil {
		t.Fatalf("RequiredMaterialsエラー: %v", err)
	}
	if len(list) == 0 {
		t.Error("adminには部材が出るべきです")
	}
}

// TestRequiredMaterialsViewHidesFromAnonymous は、公開ページの計算ビューSSRが
// 非公開ページ由来の部材を匿名へ描画しないことを固定します。
func TestRequiredMaterialsViewHidesFromAnonymous(t *testing.T) {
	setupMaterialsPermsTest(t)
	// 受注ページ1は実効公開。部品定義ページ2は非公開のまま。
	seedSecretMaterial(t, 1, true, "alice")

	if !page.EffectivePublic(1) {
		t.Fatal("前提が崩れています: 受注ページは実効公開であるべきです")
	}

	req := httptest.NewRequest("GET", "/1", nil) // Cookieなし＝匿名
	out := cms.RenderComputedViews(req, 1, `<section data-type="required-materials"></section>`)

	if strings.Contains(out, "極秘部材") || strings.Contains(out, "㊙商社") {
		t.Errorf("非公開ページ由来の部材が匿名へ描画されました:\n%s", out)
	}
}
