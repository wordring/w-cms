package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ルート表の保護レベルを固定します。
//
// ここは**黙って壊れる層**です。ハンドラを protected から root へ移す・OptionalAuth を
// 付け忘れる・ハンドラ側の r.Method ガードを消す・ミドルウェアの入れ子を外す——
// どれが起きても、既存のテスト（認可・サニタイズ・CSP）は全部 green のまま
// 実害だけが出ます。設計総点検の時点では、この層を見るテストが1本もありませんでした。
//
// csp_test.go はポリシー**文字列**を固定していますが、それが**配線されていること**は
// 見ていません（CSPProtect を外しても通る）。ここでは実際に buildHandler() が返す
// ハンドラへリクエストを流し、ヘッダが付くことまで確かめます。
//
// 注意: DBを用意しないので、ハンドラ本体まで到達するケースは panic しうる。
// この試験が見るのは**関門（認証・CSRF・メソッド）で止まること**だけなので、
// 到達してよいケースは扱わない。

// TestRoutesRequireAuth は、認証が要るAPIが未認証で 401 になることを固定します。
// 匿名で到達してよいルートと混ざらないよう、両方を1つの表に並べます。
func TestRoutesRequireAuth(t *testing.T) {
	h := buildHandler()

	tests := []struct {
		path      string
		method    string
		anonymous int // 未認証でのステータス
		note      string
	}{
		// 要認証（/api/ 配下・RequireAuth）。未認証は一律 401。
		{"/api/save", "POST", 401, "本文保存"},
		{"/api/save-block", "POST", 401, "ブロック保存"},
		{"/api/upload-pdf", "POST", 401, "添付アップロード（PDF）"},
		{"/api/upload-image", "POST", 401, "添付アップロード（画像）"},
		{"/api/parse-pdf", "POST", 401, "PDF解析（外部LLMへ送る）"},
		{"/api/new-page", "POST", 401, "ページ作成"},
		{"/api/delete-page", "POST", 401, "ページ削除"},
		{"/api/set-parent", "POST", 401, "親の付け替え"},
		{"/api/page-perms", "POST", 401, "権限変更"},
		{"/api/page-chown", "POST", 401, "所有者変更（admin）"},
		{"/api/rebuild-db", "POST", 401, "DB再構築（admin）"},
		{"/api/versions", "GET", 401, "版の一覧"},
		{"/api/version", "GET", 401, "版の本文"},
		{"/api/revert", "POST", 401, "版の書き戻し"},
		{"/api/templates", "GET", 401, "テンプレート一覧"},
		{"/api/lock", "POST", 401, "編集ロック取得"},
		{"/api/unlock", "POST", 401, "編集ロック解放"},
		{"/api/lock/force", "POST", 401, "編集ロック強制解除（admin）"},
		{"/api/admin/users", "GET", 401, "利用者一覧（admin）"},
		{"/api/admin/groups", "GET", 401, "グループ一覧（admin）"},
		{"/api/admin/audit", "GET", 401, "監査ログ（admin）"},

		// 認証不要（公開ルート）。401 にはならない。
		{"/login", "GET", 200, "ログイン画面"},
		{"/api/tag-schema", "GET", 200, "本文の語彙（秘密ではない）"},
		{"/api/me", "GET", 200, "認証状態（未認証は authenticated:false）"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		if tt.method == "POST" {
			// CSRF の関門を先に通す（ここで見たいのは認証の関門）。
			req.Header.Set("Origin", "http://example.com")
			req.Host = "example.com"
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if tt.anonymous == 401 && rr.Code != http.StatusUnauthorized {
			t.Errorf("%s %s (%s) が未認証で 401 になりません: code=%d",
				tt.method, tt.path, tt.note, rr.Code)
		}
		if tt.anonymous != 401 && rr.Code == http.StatusUnauthorized {
			t.Errorf("%s %s (%s) が匿名に閉じています: code=%d",
				tt.method, tt.path, tt.note, rr.Code)
		}
	}
}

// TestCSRFProtectIsWired は、状態変更系がオリジン検証を通ることを固定します。
// CSRFProtect のテストは総点検の時点で1本もなく、Origin を設定するテストも
// リポジトリ全体でゼロでした。
func TestCSRFProtectIsWired(t *testing.T) {
	h := buildHandler()

	// 別オリジンからの POST は 403（認証の関門より手前で止まる）。
	for _, path := range []string{"/api/save", "/api/new-page", "/api/logout", "/api/login"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(""))
		req.Header.Set("Origin", "https://evil.example")
		req.Host = "example.com"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("別オリジンからの POST %s が 403 になりません: code=%d", path, rr.Code)
		}
	}

	// Origin も Referer も無い POST は拒否する（両方欠落なら通さない）。
	req := httptest.NewRequest("POST", "/api/save", strings.NewReader(""))
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("Origin/Referer の無い POST が 403 になりません: code=%d", rr.Code)
	}

	// Referer だけでも同一オリジンなら通す（Origin を送らない環境向けの後退路）。
	req = httptest.NewRequest("POST", "/api/save", strings.NewReader(""))
	req.Header.Set("Referer", "http://example.com/000000")
	req.Host = "example.com"
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code == http.StatusForbidden {
		t.Error("同一オリジンの Referer 付き POST が 403 になりました")
	}
}

// TestStateChangingRoutesRejectGET は、状態を変える経路が GET で動かないことを
// 固定します。CSRFProtect は GET を検証しないので、GET を受けると防御が
// 素通りされます（本文へ <img src="/api/logout"> を保存する保存型CSRF）。
func TestStateChangingRoutesRejectGET(t *testing.T) {
	h := buildHandler()

	for _, path := range []string{
		"/api/logout",
		"/api/new-page",
		"/api/save",
		"/api/save-block",
		"/api/delete-page",
		"/api/set-parent",
		"/api/rebuild-db",
		"/api/lock",
		"/api/unlock",
		"/api/lock/force",
		"/api/upload-pdf",
		"/api/upload-image",
		"/api/revert",
		"/api/parse-pdf",
		"/api/login",
	} {
		req := httptest.NewRequest("GET", path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		// 401（未認証で止まる）か 405（メソッド違い）なら安全。200/302 は状態が
		// 変わりうるので許さない。
		if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s が関門で止まりません: code=%d body=%s",
				path, rr.Code, rr.Body.String())
		}
	}
}

// TestCSPIsWired は CSP が実際に配線されていることを固定します。
// csp_test.go はポリシー文字列だけを見ており、CSPProtect を buildHandler から
// 外してもそちらは通ってしまいます。
func TestCSPIsWired(t *testing.T) {
	h := buildHandler()

	// 認証を要求されるルートでも、ヘッダは最外周で付くので必ず付く。
	for _, path := range []string{"/login", "/api/save"} {
		req := httptest.NewRequest("GET", path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		csp := rr.Header().Get("Content-Security-Policy")
		if csp == "" {
			t.Errorf("%s の応答に CSP ヘッダがありません", path)
			continue
		}
		if strings.Contains(csp, "unsafe-inline") {
			t.Errorf("%s の CSP に unsafe-inline が入っています: %s", path, csp)
		}
	}
}

// TestPublicSurfaceIsWired は、クローラ向けの2本が**認証なしで**到達できることを
// 固定します（要件定義書 §4.4）。protected へ入れてしまうと 401 になり、
// 検索エンジンからはサイトが存在しないのと同じになります。
func TestPublicSurfaceIsWired(t *testing.T) {
	h := buildHandler()

	for _, path := range []string{"/robots.txt", "/sitemap.xml"} {
		req := httptest.NewRequest("GET", path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Errorf("%s が認証なしで返りません: code=%d body=%s", path, rr.Code, rr.Body.String())
		}
	}
}

// TestAuthenticatedAPIsAreNotCacheable は、要認証APIの応答がキャッシュされない
// ことを固定します。
//
// 公開ページをキャッシュ可能にした以上（要件 §4.4）、**認証済みの応答を絶対に
// キャッシュさせない**のは前提条件です。Cache-Control が無いと、ブラウザや
// 中間キャッシュがヒューリスティックに保存してよいことになり、共用パソコンで
// 前の人の応答が出うる状態が残ります。
func TestAuthenticatedAPIsAreNotCacheable(t *testing.T) {
	h := buildHandler()

	// 未認証でも 401 を返す時点でヘッダは付いている（付ける場所が入口だから）。
	for _, path := range []string{"/api/page-perms", "/api/admin/users", "/api/save"} {
		req := httptest.NewRequest("GET", path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s の応答が no-store ではありません: %q", path, cc)
		}
	}
}
