package cms

// ─────────────────────────────────────────────────────────────────────────
// 状態を変えるハンドラが、自分で GET を断ることを固定します（2026-09-14）
//
// **なぜ `cmd/w-cms/route_guard_test.go` では足りないのか。**
// あちらは `buildHandler()` にリクエストを流しますが、`RequireAuth` が**ハンドラより
// 先に**401を返すので、**ハンドラ自身のメソッド確認が一度も走りません**。
// 「401 か 405 なら合格」という判定だったため、**メソッド確認を丸ごと外しても緑のまま**
// でした——実際 `PageChownHandler` には 2026-09-14 まで確認が無く、この試験は
// それを一度も知らせませんでした（状態を変えるハンドラで唯一の例外だった）。
//
// ここでは**ハンドラを直接呼びます**。認証ミドルウェアを通らないので、返ってくるのは
// ハンドラ自身の判断です。確認を外すと 405 が 401 や panic に変わって落ちます。
//
// **なぜ GET を断つのか**——CSRF の守り（`CSRFProtect`）は GET を検証しません。
// 本文に `<img src="/api/…">` を1つ保存すると、そのページを開いた全員がその口を
// 叩くことになります（2026-08-21 に `/api/logout` で実際に起きました）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStateChangingHandlersRejectGET は、状態を変えるハンドラが**自分で** GET を
// 405 で断ることを固定します。
//
// 表に足すときは「状態を変えるか」だけで判断すること——読むだけのハンドラは
// ここではなく `cmd/w-cms/route_guard_test.go` の認可の表が見ます。
func TestStateChangingHandlersRejectGET(t *testing.T) {
	cases := []struct {
		name string
		path string
		fn   http.HandlerFunc
	}{
		{"本文保存", "/api/save", SaveAPIHandler},
		{"ブロック保存", "/api/save-block", SaveBlockAPIHandler},
		{"ページ作成", "/api/new-page", NewPageAPIHandler},
		{"ページ削除", "/api/delete-page?id=000001", DeletePageAPIHandler},
		{"親の付け替え", "/api/set-parent", SetParentAPIHandler},
		{"子ページの並べ替え", "/api/reorder", ReorderAPIHandler},
		{"版の書き戻し", "/api/revert", RevertAPIHandler},
		{"DB再構築", "/api/rebuild-db", RebuildDBAPIHandler},
		{"所有者変更（admin）", "/api/page-chown?id=000001", PageChownHandler},
		{"添付（汎用）", "/api/upload-file", UploadFileHandler},
		{"添付（画像）", "/api/upload-image", UploadImageHandler},
		{"添付（PDF）", "/api/upload-pdf", UploadPDFHandler},
		{"対応：不要を付ける", "/api/intake/handled", MarkHandledAPIHandler},
		{"手で記録を作る", "/api/intake/memo", NewMemoAPIHandler},
		// 連絡先の口（`/api/contacts/…`）は `ext/comm/contacts` の試験が見ます
		// （2026-09-15 に移設。ハンドラがこのパッケージから出たため）。
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// **panic も失敗です**——メソッド確認が無いとハンドラ本体まで進み、
			// DBの無いこの試験では落ちます。「落ちるから安全」ではありません
			// （本番にはDBがあるので、そのまま動いてしまう）。
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

// TestPagePermsHandlerChecksAuthFirst は、権限変更が**認可を先に見る**ことを記録します。
//
// 上の表に入れていないのは、こちらだけ `RequireAdmin` がメソッド確認より前にあり、
// 未認証の GET が 401 を返すためです。**メソッド確認が無いわけではありません**
// （認可を通った先にあります）。順序の違いをここに書き留めておかないと、
// 「表から漏れている」と誤読されて、次の人が表へ足して赤くします。
func TestPagePermsHandlerChecksAuthFirst(t *testing.T) {
	rr := httptest.NewRecorder()
	PagePermsHandler(rr, httptest.NewRequest("GET", "/api/page-perms?id=000001", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("未認証の GET が 401 で止まりません: code=%d body=%s", rr.Code, rr.Body.String())
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
