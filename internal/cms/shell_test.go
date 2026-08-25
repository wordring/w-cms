package cms

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// writeTestShell はテスト用の最小シェルを作業ディレクトリへ用意します。
// 本物と同じプレースホルダを持たせ、合成のロジックだけを検証します。
//
// **殻は2種類**あるので両方置きます——認証済みには編集用（assets/index.html）、
// 匿名には公開専用（assets/public.html）が使われるため、片方だけだと
// ページを描くテストが相手によって 500 になります。
func writeTestShell(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll("assets", 0755); err != nil {
		t.Fatalf("assets作成エラー: %v", err)
	}
	shell := `<!DOCTYPE html><html><head><title>w-cms エディタ</title></head>` +
		`<body><div id="editor-content"><!--WCMS_CONTENT--></div></body></html>`
	if err := os.WriteFile(filepath.Join("assets", "index.html"), []byte(shell), 0644); err != nil {
		t.Fatalf("シェル作成エラー: %v", err)
	}
	writeTestPublicShell(t)
	// mtimeキャッシュが他テストの内容を持ち越さないようにする
	shellCache.Lock()
	shellCache.body = ""
	shellCache.modTime = 0
	shellCache.Unlock()
}

// newPage はテスト用のページ（本文ファイル＋サイドカー＋DB同期）を作ります。
func newPage(t *testing.T, id, body string, meta page.PageMeta) {
	t.Helper()
	dir := page.GetPageDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("ページディレクトリ作成エラー: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".html"), []byte(body), 0644); err != nil {
		t.Fatalf("本文書き込みエラー: %v", err)
	}
	if err := page.WriteSidecar(id, meta); err != nil {
		t.Fatalf("page.WriteSidecarエラー: %v", err)
	}
	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
}

// getPage は RootHandler へGETし、レスポンスを返します。u が nil なら匿名。
func getPage(t *testing.T, path string, u *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if u != nil {
		req = auth.WithUser(req, u)
	}
	rr := httptest.NewRecorder()
	RootHandler(rr, req)
	return rr
}

// TestRootHandlerComposesBody は本文とタイトルがサーバー側で埋め込まれることを検証します。
// これが成り立つと、JSを実行しなくても（クローラや curl でも）本文が読めます。
func TestRootHandlerComposesBody(t *testing.T) {
	setupSaveTest(t)
	writeTestShell(t)

	newPage(t, "000010", "<h1>合成テスト</h1><p>本文です</p>",
		page.PageMeta{Owner: "alice", Mode: page.DefaultMode})

	rr := getPage(t, "/000010", &auth.User{Username: "alice"})
	if rr.Code != 200 {
		t.Fatalf("ステータスが200ではありません: %d (%s)", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()

	// 見出しには描画時にページ内アンカー（id）が合成される（anchor.go）。
	if !strings.Contains(body, `<h1 id="合成テスト">合成テスト</h1>`) || !strings.Contains(body, "<p>本文です</p>") {
		t.Errorf("本文が埋め込まれていません:\n%s", body)
	}
	if strings.Contains(body, "WCMS_CONTENT") {
		t.Errorf("プレースホルダが残っています:\n%s", body)
	}
	// タイトルは本文のh1から SyncIndex が拾う
	if !strings.Contains(body, "<title>合成テスト - w-cms</title>") {
		t.Errorf("タイトルが差し替わっていません:\n%s", body)
	}
}

// TestRootHandlerSanitizesOnRender は、保存経路を通さず直接置かれた危険な本文でも
// 配信時に除去されることを検証します（サニタイズ二層目＝最後の防壁）。
func TestRootHandlerSanitizesOnRender(t *testing.T) {
	setupSaveTest(t)
	writeTestShell(t)

	// エディタを経由せずファイルへ直接書いた状況（バックアップ復元・手動配置など）
	newPage(t, "000011", `<h1>題名</h1><script>alert(1)</script><p onclick="alert(2)">本文</p>`,
		page.PageMeta{Owner: "alice", Mode: page.DefaultMode})

	rr := getPage(t, "/000011", &auth.User{Username: "alice"})
	body := rr.Body.String()

	if strings.Contains(body, "alert(1)") || strings.Contains(body, "onclick") {
		t.Errorf("危険な記述が配信HTMLに残っています:\n%s", body)
	}
	if !strings.Contains(body, `<h1 id="題名">題名</h1>`) || !strings.Contains(body, "<p>本文</p>") {
		t.Errorf("正常な本文まで失われています:\n%s", body)
	}
}

// TestRootHandlerAuthorization は画面表示の認可を検証します。
// 匿名×非公開は /login へ誘導し（APIの401とは扱いを変える）、権限の無い認証ユーザーは403。
func TestRootHandlerAuthorization(t *testing.T) {
	setupSaveTest(t)
	writeTestShell(t)

	// owner=alice / other 権限なし（非公開）
	newPage(t, "000012", "<h1>秘密</h1>", page.PageMeta{Owner: "alice", Mode: "300"})
	// 実効公開のページ
	newPage(t, "000013", "<h1>公開ページ</h1>", page.PageMeta{Owner: "alice", Mode: "300", Public: true})

	// 匿名には「読めない」と「存在しない」を区別させない（要件定義書 §2.1）。
	// 入口が失われないよう、トップページだけは従来どおり /login へ誘導する。
	t.Run("匿名×非公開は404（不存在と区別しない）", func(t *testing.T) {
		rr := getPage(t, "/000012", nil)
		if rr.Code != 404 {
			t.Fatalf("404ではありません: %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "/login?next=") {
			t.Error("ログインへの戻り先つき入口がありません")
		}
		if strings.Contains(rr.Body.String(), "秘密") {
			t.Error("非公開ページの本文が漏れています")
		}
	})

	t.Run("匿名×実効公開は200で本文が見える", func(t *testing.T) {
		rr := getPage(t, "/000013", nil)
		if rr.Code != 200 {
			t.Fatalf("200ではありません: %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "公開ページ") {
			t.Error("公開ページの本文が埋め込まれていません")
		}
	})

	t.Run("権限の無い認証ユーザーは403", func(t *testing.T) {
		rr := getPage(t, "/000012", &auth.User{Username: "bob", Groups: []string{"other"}})
		if rr.Code != 403 {
			t.Fatalf("403ではありません: %d", rr.Code)
		}
		if strings.Contains(rr.Body.String(), "秘密") {
			t.Error("非公開ページの本文が漏れています")
		}
	})

	t.Run("存在しないページは404", func(t *testing.T) {
		rr := getPage(t, "/000999", &auth.User{Username: "alice"})
		if rr.Code != 404 {
			t.Errorf("404ではありません: %d", rr.Code)
		}
	})
}
