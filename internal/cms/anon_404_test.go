package cms

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// 匿名に対しては「読めないページ」と「存在しないページ」の応答を区別しません
// （どちらも 404）。狙いは**権限の無い書類を開いた社員が迷わないこと**です——
// ユーザーの言葉:「1は気にしません（＝数が数えられること）／2はありませんと出るほうが
// 良いです（＝ログイン画面と『ありません』のどちら）」。列挙対策は動機ではありません。
//
// 認証済み×read不可は 403 のまま（存在は分かるが読めない、という Unix の作法）。
//
// 例外は入口が失われないよう、**トップ経由**（`/` → `/000000`）のみ従来どおり
// `/login` へ誘導します。ログイン画面自体は `/login` で常に到達可能です。

// setupAnonTest は「公開ページ1枚・非公開ページ1枚」を用意します。
func setupAnonTest(t *testing.T) {
	t.Helper()
	setupUploadTest(t, "000000", page.PageMeta{Owner: "admin", Mode: "302", Public: true})
	seedMasterPages(t, "000000", "000005", "000006") // 本文を置く（サイドカーは下で上書きする）

	// トップ = 公開（パスゲートの起点）
	if err := page.WriteSidecar("000000", page.PageMeta{Owner: "admin", Mode: "302", Public: true}); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}
	// 000005 = 公開
	if err := page.WriteSidecar("000005", page.PageMeta{
		Owner: "alice", Mode: "302", Public: true, ParentID: "000000"}); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}
	// 000006 = 非公開
	if err := page.WriteSidecar("000006", page.PageMeta{
		Owner: "alice", Mode: "300", Public: false, ParentID: "000000"}); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}
	for _, id := range []string{"000000", "000005", "000006"} {
		if err := SyncIndex(id, "<h1>ページ"+id+"</h1>"); err != nil {
			t.Fatalf("SyncIndex(%s)エラー: %v", id, err)
		}
	}
}

// getAnon は匿名（Cookie なし）でハンドラを叩きます。
func getAnon(t *testing.T, h http.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", target, nil)
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}

// TestAnonymousGetsNotFoundForPrivatePage は、画面の匿名応答が404へ揃うことを固定します。
func TestAnonymousGetsNotFoundForPrivatePage(t *testing.T) {
	setupAnonTest(t)

	// 非公開ページ
	rr := getAnon(t, RootHandler, "/000006")
	if rr.Code != http.StatusNotFound {
		t.Errorf("匿名×非公開が404になりません: code=%d", rr.Code)
	}
	// 存在しないページ（同じ応答であること＝区別されない）
	rr2 := getAnon(t, RootHandler, "/000999")
	if rr2.Code != rr.Code {
		t.Errorf("非公開(%d)と不存在(%d)で応答が違います", rr.Code, rr2.Code)
	}

	// 公開ページは認可を通ること（過剰に隠していないこと）。
	// このテストは一時ディレクトリで動くので assets/index.html が無く、殻の合成は
	// 500 になる——見たいのは「404や302で門前払いされないこと」なのでそこだけ見る。
	if rr := getAnon(t, RootHandler, "/000005"); rr.Code == http.StatusNotFound || rr.Code == http.StatusFound {
		t.Errorf("匿名×公開が門前払いされました: code=%d", rr.Code)
	}
}

// TestAnonymousAPIsReturnNotFound は、API側の匿名応答も404へ揃うことを固定します。
// 画面だけ揃えても、API が 401 を返せば同じことが分かってしまいます。
func TestAnonymousAPIsReturnNotFound(t *testing.T) {
	setupAnonTest(t)

	cases := map[string]struct {
		h      http.HandlerFunc
		target string
	}{
		"/api/load":      {LoadAPIHandler, "/api/load?id=000006"},
		"/api/page-meta": {PageMetaAPIHandler, "/api/page-meta?id=000006"},
		"/api/children":  {ChildPagesAPIHandler, "/api/children?parent_id=000006"},
	}
	for name, c := range cases {
		if rr := getAnon(t, c.h, c.target); rr.Code != http.StatusNotFound {
			t.Errorf("%s の匿名×非公開が404になりません: code=%d body=%s",
				name, rr.Code, rr.Body.String())
		}
	}
}

// TestTopPageStillRedirectsToLogin は、入口が失われないことを固定します。
// トップページ（`/` の飛び先＝`/000000`）だけは従来どおりログインへ誘導します
// ——ここまで404にすると、サイトを閉じたときに誰も入口へ辿り着けなくなります。
func TestTopPageStillRedirectsToLogin(t *testing.T) {
	setupAnonTest(t)
	// トップを非公開にする（サイト全体のキルスイッチ）
	if err := page.WriteSidecar("000000", page.PageMeta{Owner: "admin", Mode: "302"}); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}
	if err := SyncIndex("000000", "<h1>トップ</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rr := getAnon(t, RootHandler, "/000000")
	if rr.Code != http.StatusFound {
		t.Fatalf("トップページがログインへ誘導されません: code=%d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Errorf("誘導先がログイン画面ではありません: %s", loc)
	}
}

// TestNotFoundPageOffersLogin は、匿名の404が**ログインへの入口を持つ**ことを
// 固定します。これが無いと、社員どうしでページのアドレスを共有する使い方が
// 「ありません」で行き止まりになります（404統一と同時に入れる、という決定の片割れ）。
func TestNotFoundPageOffersLogin(t *testing.T) {
	setupAnonTest(t)

	rr := getAnon(t, RootHandler, "/000006")
	body := rr.Body.String()
	if !strings.Contains(body, "/login?next=%2F000006") &&
		!strings.Contains(body, "/login?next=/000006") {
		t.Errorf("404にログインへの戻り先つきリンクがありません:\n%s", body)
	}
	// 存在しないページでも同じ体裁（見分けが付かないこと）
	rr2 := getAnon(t, RootHandler, "/000999")
	if !strings.Contains(rr2.Body.String(), "/login?next=") {
		t.Error("不存在ページの404だけ体裁が違います（区別が付いてしまう）")
	}
}
