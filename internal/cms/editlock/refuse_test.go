package editlock

// ─────────────────────────────────────────────────────────────────────────
// 機械の書き込みを、編集中のページに対して断ることを固定します（2026-09-14）
//
// `append_page.go` は「**ロックは呼ぶ側が取ります**」と宣言していますが、
// `RewriteBody`/`SetPageH1` の呼び手6箇所が誰も取っていませんでした——連絡先の登録・
// 「対応：不要」・整理の実行です。いずれも**一覧画面のボタン**で、エディタを
// 開いていないので編集トークンを持てず、`RequireEditLock` を通すと必ず断られます。
//
// `RewriteBody` は**読んで・変えて・書く**ので、誰かがエディタを開いていると
// オートセーブと機械の書き込みが黙って上書きし合います。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRefuseWhileEditing は関門の3つのふるまいを固定します。
func TestRefuseWhileEditing(t *testing.T) {
	const page = 990001

	t.Run("誰も開いていなければ通す", func(t *testing.T) {
		Locks.ForceRelease(page)
		rr := httptest.NewRecorder()
		if !RefuseWhileEditing(rr, "990001") {
			t.Fatalf("ロックが無いのに断られました: code=%d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("他人が開いていたら断る", func(t *testing.T) {
		Locks.ForceRelease(page)
		res := Locks.TryAcquire(page, "alice", "")
		if !res.Acquired {
			t.Fatalf("下ごしらえのロックが取れません: %+v", res)
		}
		defer Locks.ForceRelease(page)

		rr := httptest.NewRecorder()
		if RefuseWhileEditing(rr, "990001") {
			t.Fatal("編集中なのに通しました（オートセーブと上書きし合います）")
		}
		if rr.Code != http.StatusConflict {
			t.Errorf("409 を期待: code=%d", rr.Code)
		}
		if body := rr.Body.String(); body == "" || !contains(body, "alice") {
			t.Errorf("誰が開いているかを知らせていません: %q", body)
		}
	})

	// **自分が開いていても断ります。** 手元のエディタは書き換え前の本文を持って
	// いるので、そのまま保存すれば機械の変更が消えます——「自分だから安全」ではない。
	t.Run("自分が開いていても断る", func(t *testing.T) {
		Locks.ForceRelease(page)
		if res := Locks.TryAcquire(page, "alice", ""); !res.Acquired {
			t.Fatalf("下ごしらえのロックが取れません: %+v", res)
		}
		defer Locks.ForceRelease(page)

		rr := httptest.NewRecorder()
		if RefuseWhileEditing(rr, "990001") {
			t.Fatal("自分が開いていても断るべきです（手元の本文が古くなっている）")
		}
	})
}

// TestEditorOpenIgnoresAbandonedLock は、**保持者が居なくなったロックを無視する**ことを
// 固定します。
//
// `tick` がロックを消すのは**待機者が居るとき**だけなので、ブラウザを閉じただけの人の
// ロックは残り続けます。それを「開いている」と数えると、**機械の書き込みが永久に
// 止まります**（誰も待っていないので自然には消えない）。
func TestEditorOpenIgnoresAbandonedLock(t *testing.T) {
	const page = 990002
	Locks.ForceRelease(page)
	if res := Locks.TryAcquire(page, "bob", ""); !res.Acquired {
		t.Fatalf("下ごしらえのロックが取れません: %+v", res)
	}
	defer Locks.ForceRelease(page)

	// 取得直後は「接続待ちの猶予」の内側なので開いている扱い。
	if _, open := Locks.EditorOpen(page); !open {
		t.Fatal("取得直後は開いている扱いのはずです（holderConnectGrace の内側）")
	}

	// SSE が繋がらないまま猶予が過ぎた＝ブラウザを閉じた人のロック。
	Locks.mu.Lock()
	Locks.locks[page].acquiredAt = time.Now().Add(-holderConnectGrace - time.Second)
	Locks.locks[page].holderConn = false
	Locks.mu.Unlock()

	if holder, open := Locks.EditorOpen(page); open {
		t.Errorf("保持者が居ないロックで止まっています（機械の書き込みが永久に通らなくなります）: holder=%q", holder)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
