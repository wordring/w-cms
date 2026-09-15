package comm

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 通信の口のメソッド関門（2026-09-15 に internal/cms/method_guard_test.go から移した）。
//
// **ハンドラを直接呼びます。** `buildHandler()` へ流すと `RequireAuth` が先に 401 を返し、
// ハンドラ自身のメソッド確認が一度も走りません（コア側の同じ試験の冒頭に経緯）。

// TestCommStateChangingHandlersRejectGET は、状態を変える通信の口が**自分で** GET を
// 405 で断ることを固定します。
func TestCommStateChangingHandlersRejectGET(t *testing.T) {
	cases := []struct {
		name string
		path string
		fn   http.HandlerFunc
	}{
		{"対応：不要を付ける", "/api/intake/handled", MarkHandledAPIHandler},
		{"手で記録を作る", "/api/intake/memo", NewMemoAPIHandler},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// **panic も失敗です**——メソッド確認が無いとハンドラ本体まで進み、
			// DBの無いこの試験では落ちます。「落ちるから安全」ではありません。
			defer func() {
				if e := recover(); e != nil {
					t.Fatalf("GET が本体まで届いて落ちました（メソッド確認が要ります）: %v", e)
				}
			}()
			rr := httptest.NewRecorder()
			c.fn(rr, httptest.NewRequest("GET", c.path, nil))
			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("GET %s が自分で断っていません: code=%d（405 を期待）body=%s",
					c.path, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestJSONReadHandlersRejectNonGET は、JSONで答える読み取り口が GET 以外を
// 断ることを固定します。
//
// ⚠ **`handler_replies` と `handler_thread` は、2026-09-14 までメソッド確認が
// 抜けていました**——同じ16行の前置きを7箇所に写していて、写し損ねたぶんです。
// 共通の関門（`GateJSONPageRead`）へ寄せたので、いまは抜けようがありません
// ——が、誰かが関門を通さない口を新しく書いたら、ここへ足しても落ちます。
//
// **JSONで断ることも見ます。** `text/plain` で返すと、受ける側は `res.json()` に
// 失敗して理由を落とします（2026-09-14 に実際に起きていた形）。
func TestJSONReadHandlersRejectNonGET(t *testing.T) {
	cases := []struct {
		name string
		path string
		fn   http.HandlerFunc
	}{
		{"この記録への返信", "/api/replies?page_id=000001", RepliesAPIHandler},
		{"やりとりの前後", "/api/thread?page_id=000001", ThreadAPIHandler},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			c.fn(rr, httptest.NewRequest("POST", c.path, nil))
			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("POST %s を断っていません: code=%d body=%s", c.path, rr.Code, rr.Body.String())
			}
			if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("JSONで断っていません: Content-Type=%q（受ける側が理由を落とします）", ct)
			}
		})
	}
}
