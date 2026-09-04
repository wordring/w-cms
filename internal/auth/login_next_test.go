package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// 匿名の404統一には「ログイン後に元のページへ戻す」が付いてきます。
// ログインしても必ずトップへ戻されるままだと、社員どうしでページのアドレスを
// 共有する使い方が不便になる——404で「ありません」と言われ、ログインしても
// 目的のページには着かない、という行き止まりになるためです。
//
// 戻り先は外から与えられるので、**開かれたリダイレクトにしない**ことが要点です。

// TestSafeNextPath は戻り先の検証を固定します。
func TestSafeNextPath(t *testing.T) {
	ok := []string{"/000123", "/000123?edit=true", "/"}
	for _, s := range ok {
		if got := safeNextPath(s); got != s {
			t.Errorf("正当な戻り先が落とされました: %q → %q", s, got)
		}
	}

	// 外部サイトへ飛ばせてはいけない（開かれたリダイレクト）
	bad := []string{
		"https://evil.example/",    // 絶対URL
		"//evil.example/",          // プロトコル相対
		"/\\evil.example",          // バックスラッシュ経由
		"http://evil.example",      // スキーム付き
		"javascript:alert(1)",      // スキーム
		"evil",                     // 相対（先頭が / でない）
		"",                         // 空
		"/000123\nSet-Cookie: x=1", // ヘッダ分割
	}
	for _, s := range bad {
		if got := safeNextPath(s); got != "/" {
			t.Errorf("危険な戻り先が通りました: %q → %q（期待 \"/\"）", s, got)
		}
	}
}

// TestLoginPageCarriesNext は、ログイン画面が戻り先を持ち回ることを固定します。
// フォームが next を運ばないと、認証に成功しても戻り先が失われます。
func TestLoginPageCarriesNext(t *testing.T) {
	req := httptest.NewRequest("GET", "/login?next=%2F000123", nil)
	rr := httptest.NewRecorder()
	LoginPageHandler(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, `name="next"`) {
		t.Errorf("ログインフォームが next を運んでいません:\n%s", body)
	}
	if !strings.Contains(body, "/000123") {
		t.Errorf("戻り先がフォームに入っていません:\n%s", body)
	}

	// 戻り先はHTML属性へ入るのでエスケープされること
	req = httptest.NewRequest("GET", `/login?next=%2F%22%3E%3Cscript%3Ealert(1)%3C%2Fscript%3E`, nil)
	rr = httptest.NewRecorder()
	LoginPageHandler(rr, req)
	if strings.Contains(rr.Body.String(), "<script>") {
		t.Errorf("戻り先がエスケープされていません:\n%s", rr.Body.String())
	}
}
