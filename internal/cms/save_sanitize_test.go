package cms

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/database"

	_ "modernc.org/sqlite"
)

// setupSaveTest は data/master を一時ディレクトリへ切り替え、インメモリDBを用意します。
func setupSaveTest(t *testing.T) *sql.DB {
	t.Helper()

	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	database.DB = db

	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}
	return db
}

// postSave は SaveAPIHandler へ保存リクエストを送り、レスポンスJSONを返します。
func postSave(t *testing.T, id, htmlBody string) map[string]interface{} {
	t.Helper()

	payload, _ := json.Marshal(map[string]string{"page_id": id, "html": htmlBody})
	req := httptest.NewRequest("POST", "/api/save", strings.NewReader(string(payload)))
	req = auth.WithUser(req, &auth.User{Username: "tester", IsAdmin: true})

	rr := httptest.NewRecorder()
	SaveAPIHandler(rr, req)

	if rr.Code != 200 {
		t.Fatalf("保存が失敗しました: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("レスポンスJSONのパースに失敗: %v (body=%s)", err, rr.Body.String())
	}
	return got
}

// TestSaveSanitizesAndEchoesBack は、危険な本文を保存すると
//   - 正本ファイルにはサニタイズ済みが書かれ
//   - レスポンスの html がその内容と一致し
//   - sanitized=true で「除去が起きた」ことがエディタへ伝わる
//
// ことを検証します（保存時エコーバック方式の核）。
func TestSaveSanitizesAndEchoesBack(t *testing.T) {
	setupSaveTest(t)

	const id = "000042"
	if err := WriteSidecar(id, PageMeta{Owner: "tester", Mode: DefaultMode}); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}

	dangerous := `<h1>見出し</h1><script>alert(1)</script><p onclick="alert(2)">本文</p>`
	resp := postSave(t, id, dangerous)

	// レスポンスの sanitized フラグ
	if sanitized, _ := resp["sanitized"].(bool); !sanitized {
		t.Error("危険な本文を保存したのに sanitized=false になっています")
	}

	echoed, _ := resp["html"].(string)
	if strings.Contains(echoed, "script") || strings.Contains(echoed, "onclick") {
		t.Errorf("返却された html に危険な記述が残っています: %s", echoed)
	}
	if !strings.Contains(echoed, "<h1>見出し</h1>") || !strings.Contains(echoed, "<p>本文</p>") {
		t.Errorf("正常な本文まで失われています: %s", echoed)
	}

	// 正本ファイルの中身がサニタイズ済みで、返却内容と一致すること
	saved, err := os.ReadFile(filepath.Join(GetPageDir(id), id+".html"))
	if err != nil {
		t.Fatalf("保存ファイルの読み取りに失敗: %v", err)
	}
	if string(saved) != echoed {
		t.Errorf("保存内容と返却内容が一致しません:\nファイル: %s\n返却: %s", saved, echoed)
	}
	if strings.Contains(string(saved), "script") || strings.Contains(string(saved), "onclick") {
		t.Errorf("正本ファイルに危険な記述が残っています: %s", saved)
	}
}

// TestSaveKeepsNormalContentIntact は、通常の編集で保存した本文が
// サニタイズによって変化しない（sanitized=false・内容そのまま）ことを検証します。
// エディタのシリアライザ updateHtmlPreview が出力する語彙は許可リストの範囲内なので、
// 普段の保存でエコーバックによる差し替えが起きてはいけません。
func TestSaveKeepsNormalContentIntact(t *testing.T) {
	setupSaveTest(t)

	const id = "000043"
	if err := WriteSidecar(id, PageMeta{Owner: "tester", Mode: DefaultMode}); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}

	// updateHtmlPreview が実際に出力する形（改行・インデント込み）を模した本文
	normal := "<m-tag name=\"発注元\" value=\"株式会社テスト\"></m-tag>\n" +
		"<h1>各マシーン用部品の調達</h1>\n" +
		"<p>以下の通り発注しました。</p>\n" +
		"<m-file src=\"po.pdf\" name=\"発注書.pdf\" tag=\"顧客の発注書\" order-no=\"PO-1\" client-name=\"得意先\" ordered-at=\"2026-06-18\">\n" +
		"    <m-item item-id=\"A-1\" item-name=\"側板\" quantity=\"20\" status=\"未着手\" price=\"1200\"></m-item>\n" +
		"</m-file>"

	resp := postSave(t, id, normal)

	if sanitized, _ := resp["sanitized"].(bool); sanitized {
		t.Errorf("通常の本文なのに sanitized=true になりました（誤検知）:\n返却: %v", resp["html"])
	}
	if echoed, _ := resp["html"].(string); echoed != normal {
		t.Errorf("通常の本文が変化しました:\n入力: %q\n返却: %q", normal, echoed)
	}
}
