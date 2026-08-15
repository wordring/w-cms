package editlock

import (
	"testing"
	"time"
)

// drain は購読者チャネルに来ているイベントの最後のものを返します（中間状態は読み飛ばす）。
func drain(s *subscriber) (lockEvent, bool) {
	var last lockEvent
	got := false
	for {
		select {
		case ev := <-s.ch:
			last, got = ev, true
		default:
			return last, got
		}
	}
}

// TestLockAcquireBasic は取得・自分の再取得・同一ユーザー別タブ・他者のbusyを検証します。
func TestLockAcquireBasic(t *testing.T) {
	m := newLockManager()

	a := m.TryAcquire(1, "alice", "")
	if !a.Acquired || a.Token == "" {
		t.Fatalf("空きページの取得に失敗: %+v", a)
	}

	// 同一エディタ（トークン一致）の再取得 → 同じトークン
	a2 := m.TryAcquire(1, "alice", a.Token)
	if !a2.Acquired || a2.Token != a.Token {
		t.Errorf("トークン一致の再取得でトークンが変わりました: %+v", a2)
	}

	// 同一ユーザーでも別エディタ（トークンなし）→ busy、SameUser=true
	self := m.TryAcquire(1, "alice", "")
	if self.Acquired || !self.SameUser {
		t.Errorf("同一ユーザーの別タブが competition になっていません: %+v", self)
	}

	// 他者 → busy、保持者名、SameUser=false
	b := m.TryAcquire(1, "bob", "")
	if b.Acquired || b.Holder != "alice" || b.SameUser {
		t.Errorf("busy 応答が不正です: %+v", b)
	}
}

// TestLockValidate はトークン検証（ロック無しは許可、他者保持は拒否、解放後は許可）を検証します。
func TestLockValidate(t *testing.T) {
	m := newLockManager()

	if !m.Validate(99, "alice", "") {
		t.Errorf("ロック無しページの保存が拒否されました")
	}
	tokA := m.TryAcquire(1, "alice", "").Token
	if !m.Validate(1, "alice", tokA) {
		t.Errorf("保持者本人の保存が拒否されました")
	}
	if m.Validate(1, "bob", "wrong") {
		t.Errorf("非保持者の保存が許可されました")
	}
	m.Release(1, "alice", tokA)
	if !m.Validate(1, "alice", tokA) {
		t.Errorf("解放後の保存が拒否されました")
	}
}

// TestLockReleaseGuards は他者は解放できず本人/admin は解放できることを検証します。
func TestLockReleaseGuards(t *testing.T) {
	m := newLockManager()
	tokA := m.TryAcquire(1, "alice", "").Token

	m.Release(1, "bob", "x") // 他者は解放できない
	if !m.Validate(1, "alice", tokA) {
		t.Errorf("他者の Release で alice のロックが消えました")
	}
	m.Release(1, "alice", tokA) // 本人は解放できる
	if _, ok := m.locks[1]; ok {
		t.Errorf("本人 Release 後もロックが残っています")
	}

	m.TryAcquire(2, "alice", "")
	m.ForceRelease(2) // admin 強制解除
	if _, ok := m.locks[2]; ok {
		t.Errorf("強制解除後もロックが残っています")
	}
}

// TestWaiterArmsGraceAndNotifiesHolder は、待機者の接続で猶予が起動し保持者へ通知が出ることを検証します。
func TestWaiterArmsGraceAndNotifiesHolder(t *testing.T) {
	m := newLockManager()
	tokA := m.TryAcquire(1, "alice", "").Token
	hs := m.subscribe(1, "holder", "alice", tokA) // 保持者が接続（present）
	drain(hs)

	ws := m.subscribe(1, "waiter", "bob", "") // 待機者が接続 → 猶予起動
	// 保持者へ "waiter" 通知が届く
	if ev, _ := drain(hs); ev.Type != "waiter" {
		t.Errorf("保持者に待機者通知が届きません: %+v", ev)
	}
	// 待機者へは "busy" 通知
	if ev, _ := drain(ws); ev.Type != "busy" || ev.Holder != "alice" {
		t.Errorf("待機者に busy 通知が届きません: %+v", ev)
	}
	if l := m.locks[1]; l == nil || l.graceDeadline.IsZero() {
		t.Errorf("待機者接続で猶予が起動していません")
	}
}

// TestGraceExpiryHandover は、猶予満了で保持者が lost・待機者が available になることを検証します。
func TestGraceExpiryHandover(t *testing.T) {
	m := newLockManager()
	tokA := m.TryAcquire(1, "alice", "").Token
	hs := m.subscribe(1, "holder", "alice", tokA)
	ws := m.subscribe(1, "waiter", "bob", "")
	drain(hs)
	drain(ws)

	// 猶予を過去にして tick → 明け渡し
	m.locks[1].graceDeadline = time.Now().Add(-time.Second)
	m.tick(time.Now())

	if _, ok := m.locks[1]; ok {
		t.Errorf("猶予満了後もロックが残っています")
	}
	if ev, _ := drain(hs); ev.Type != "lost" {
		t.Errorf("保持者に lost が届きません: %+v", ev)
	}
	if ev, _ := drain(ws); ev.Type != "available" {
		t.Errorf("待機者に available が届きません: %+v", ev)
	}
	// 明け渡し後、bob は取得できる
	if r := m.TryAcquire(1, "bob", ""); !r.Acquired {
		t.Errorf("明け渡し後に bob が取得できません: %+v", r)
	}
}

// TestHolderDisconnectHandsOver は、保持者の切断で待機者がいれば即明け渡しになることを検証します。
func TestHolderDisconnectHandsOver(t *testing.T) {
	m := newLockManager()
	tokA := m.TryAcquire(1, "alice", "").Token
	// 取得直後の接続猶予を無効化して「切断＝不在」を確実にする
	m.locks[1].acquiredAt = time.Now().Add(-time.Minute)
	hs := m.subscribe(1, "holder", "alice", tokA)
	ws := m.subscribe(1, "waiter", "bob", "")
	drain(ws)

	m.unsubscribe(1, hs) // 保持者が切断 → 待機者がいるので即明け渡し
	if _, ok := m.locks[1]; ok {
		t.Errorf("保持者切断後もロックが残っています")
	}
	if ev, _ := drain(ws); ev.Type != "available" {
		t.Errorf("保持者切断後に待機者へ available が届きません: %+v", ev)
	}
}

// TestWaiterDisconnectCancelsGrace は、待機者の切断で猶予がキャンセルされ保持者が残ることを検証します。
func TestWaiterDisconnectCancelsGrace(t *testing.T) {
	m := newLockManager()
	tokA := m.TryAcquire(1, "alice", "").Token
	hs := m.subscribe(1, "holder", "alice", tokA)
	ws := m.subscribe(1, "waiter", "bob", "")
	drain(hs)

	m.unsubscribe(1, ws) // 唯一の待機者が離脱 → 猶予キャンセル
	if l := m.locks[1]; l == nil || !l.graceDeadline.IsZero() {
		t.Errorf("待機者離脱後も猶予が残っています: %+v", m.locks[1])
	}
	if ev, _ := drain(hs); ev.Type != "holding" {
		t.Errorf("待機者離脱後に保持者へ holding が届きません: %+v", ev)
	}
}

// TestStaleHolderStolenOnAcquire は、保持者が未接続（接続猶予切れ）かつ待機者ありなら、
// 新たな取得試行で空き扱いになることを検証します。
func TestStaleHolderStolenOnAcquire(t *testing.T) {
	m := newLockManager()
	m.TryAcquire(1, "alice", "")              // alice は SSE 未接続のまま
	m.locks[1].acquiredAt = time.Now().Add(-time.Minute) // 接続猶予切れ
	ws := m.subscribe(1, "waiter", "bob", "") // 待機者あり
	drain(ws)

	if r := m.TryAcquire(1, "bob", ""); !r.Acquired {
		t.Errorf("保持者が接続を失っているのに bob が取得できません: %+v", r)
	}
}
