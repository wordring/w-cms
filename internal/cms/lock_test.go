package cms

import (
	"testing"
	"time"
)

func newTestLockManager() *lockManager {
	return &lockManager{locks: make(map[int]*pageLock)}
}

// TestLockAcquireBasic は取得・自分の再取得・他者のbusyを検証します。
func TestLockAcquireBasic(t *testing.T) {
	m := newTestLockManager()
	t0 := time.Now()

	// 空き → 取得成功
	a := m.tryAcquireAt(1, "alice", t0)
	if !a.Acquired || a.Token == "" {
		t.Fatalf("空きページの取得に失敗: %+v", a)
	}

	// 自分が再取得 → 同じトークン
	a2 := m.tryAcquireAt(1, "alice", t0.Add(time.Second))
	if !a2.Acquired || a2.Token != a.Token {
		t.Errorf("自分の再取得でトークンが変わりました: %+v", a2)
	}

	// 他者 → busy、保持者名と猶予残りを返す
	b := m.tryAcquireAt(1, "bob", t0.Add(2*time.Second))
	if b.Acquired {
		t.Errorf("他者が保持中なのに取得できました")
	}
	if b.Holder != "alice" {
		t.Errorf("保持者名が違います: %q", b.Holder)
	}
	if b.GraceRemaining <= 0 || b.GraceRemaining > lockGraceDuration {
		t.Errorf("猶予残りが不正です: %v", b.GraceRemaining)
	}
}

// TestLockForcedHandover は、保持者が生存しても他者要求から2分で明け渡すことを検証します。
func TestLockForcedHandover(t *testing.T) {
	m := newTestLockManager()
	t0 := time.Now()
	tokA := m.tryAcquireAt(1, "alice", t0).Token

	// bob が要求 → 猶予開始（t0+2分）
	if r := m.tryAcquireAt(1, "bob", t0); r.Acquired {
		t.Fatal("bob が即取得してしまいました")
	}
	// 猶予満了まで、両者がそれぞれの閾値内でポーリングし続ける
	// （alice=status で生存／bob=再要求で待機継続）。猶予中は busy のまま。
	for d := 10; d <= 120; d += 10 {
		ts := t0.Add(time.Duration(d) * time.Second)
		m.statusAt(1, "alice", tokA, ts)
		if r := m.tryAcquireAt(1, "bob", ts); r.Acquired {
			t.Fatalf("猶予中(%ds)に bob が取得してしまいました", d)
		}
	}

	// 猶予満了直後 → bob が取得（強制明け渡し）
	r := m.tryAcquireAt(1, "bob", t0.Add(121*time.Second))
	if !r.Acquired {
		t.Errorf("猶予満了後も bob が取得できません: %+v", r)
	}
	// alice の旧トークンでの保存は拒否される
	if m.validateAt(1, "alice", tokA, t0.Add(122*time.Second)) {
		t.Errorf("明け渡し後も alice のトークンが有効です")
	}
}

// TestLockHolderStaleSteal は、保持者がポーリングを止めたら待機者が2分待たず奪取することを検証します。
func TestLockHolderStaleSteal(t *testing.T) {
	m := newTestLockManager()
	t0 := time.Now()
	m.tryAcquireAt(1, "alice", t0) // alice は以後ポーリングしない（落ちた想定）

	// bob は30秒以内の間隔でポーリングし続ける
	m.tryAcquireAt(1, "bob", t0.Add(20*time.Second)) // busy（aliceまだstaleでない）
	r := m.tryAcquireAt(1, "bob", t0.Add(35*time.Second))
	if !r.Acquired {
		t.Errorf("保持者が落ちているのに bob が奪取できません: %+v", r)
	}
}

// TestLockRequesterLeft は、要求者がポーリングを止めたら猶予がキャンセルされ保持者が残ることを検証します。
func TestLockRequesterLeft(t *testing.T) {
	m := newTestLockManager()
	t0 := time.Now()
	tokA := m.tryAcquireAt(1, "alice", t0).Token
	m.tryAcquireAt(1, "bob", t0) // bob が一度要求（猶予開始）。以後ポーリングしない。

	// alice が status を見る頃には bob は離脱扱い → 猶予キャンセル・alice 継続
	st := m.statusAt(1, "alice", tokA, t0.Add(40*time.Second))
	if !st.IsHolder {
		t.Errorf("猶予キャンセル後も alice が保持者でない: %+v", st)
	}
	if st.WaiterPresent {
		t.Errorf("離脱した要求者がまだ待機中扱いです: %+v", st)
	}
}

// TestLockValidate はトークン検証（ロック無しは許可、他者保持は拒否）を検証します。
func TestLockValidate(t *testing.T) {
	m := newTestLockManager()
	t0 := time.Now()

	// ロックが無いページ → 許可（無競合。フロント未対応でも保存可）
	if !m.validateAt(99, "alice", "", t0) {
		t.Errorf("ロック無しページの保存が拒否されました")
	}

	tokA := m.tryAcquireAt(1, "alice", t0).Token
	if !m.validateAt(1, "alice", tokA, t0.Add(time.Second)) {
		t.Errorf("保持者本人の保存が拒否されました")
	}
	if m.validateAt(1, "bob", "wrong", t0.Add(time.Second)) {
		t.Errorf("非保持者の保存が許可されました")
	}

	// 解放後はロック無し → 再び許可
	m.Release(1, "alice", tokA)
	if !m.validateAt(1, "alice", tokA, t0.Add(2*time.Second)) {
		t.Errorf("解放後の保存が拒否されました")
	}
}

// TestLockReleaseAndForce は明示解放・強制解除・lost検知を検証します。
func TestLockReleaseAndForce(t *testing.T) {
	m := newTestLockManager()
	t0 := time.Now()

	tokA := m.tryAcquireAt(1, "alice", t0).Token
	// 他者は解放できない
	m.Release(1, "bob", "x")
	if !m.validateAt(1, "alice", tokA, t0) {
		t.Errorf("他者の Release で alice のロックが消えました")
	}
	// 本人は解放できる
	m.Release(1, "alice", tokA)
	if st := m.statusAt(1, "alice", tokA, t0); !st.Lost {
		t.Errorf("解放後の status が lost を返しません: %+v", st)
	}

	// 強制解除 → lost
	tokA2 := m.tryAcquireAt(2, "alice", t0).Token
	m.ForceRelease(2)
	if st := m.statusAt(2, "alice", tokA2, t0); st.IsHolder || !st.Lost {
		t.Errorf("強制解除後の status が不正です: %+v", st)
	}
}

// TestLockStatusWaiterNotice は、待機者がいると保持者の status に通知が出ることを検証します。
func TestLockStatusWaiterNotice(t *testing.T) {
	m := newTestLockManager()
	t0 := time.Now()
	tokA := m.tryAcquireAt(1, "alice", t0).Token
	m.tryAcquireAt(1, "bob", t0.Add(time.Second)) // bob 要求

	st := m.statusAt(1, "alice", tokA, t0.Add(2*time.Second))
	if !st.IsHolder || !st.WaiterPresent {
		t.Errorf("待機者通知が保持者の status に出ません: %+v", st)
	}
	if st.GraceRemaining <= 0 {
		t.Errorf("猶予残りが出ません: %+v", st)
	}
}
