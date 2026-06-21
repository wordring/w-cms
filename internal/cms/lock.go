package cms

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────
// 同時編集の悲観ロック（ページ単位）— 競合トリガー方式
//
// 設計の根拠と決定ログは [docs/同時編集の競合対策（検討中）.md] を参照。要点:
//   - ロックは ephemeral（揮発的）なランタイム状態。プロセス内の mutex 付き map で持つ
//     （単一プロセス前提。再起動で全ロックが消えるのは許容）。
//   - 無競合なら無期限保持。他者が編集を要求した時点から 2 分の猶予で明け渡す。
//   - 待機キューは持たない。サーバーは busy を返すだけで、待機者はクライアント側で
//     10 秒ごとにポーリングして空きを確認する。猶予満了後は早い者勝ち。
//   - 要求者の離脱はポーリング途絶（約30秒）で検知し、猶予をキャンセルする。
//   - 保持者の死活はポーリング（status/save）の途絶（約30秒）で検知し、待機者がいれば
//     2 分を待たず即解放する。
// ─────────────────────────────────────────────────────────────────────────

// ロックの時間パラメータ（テストから差し替えられるよう var）。
var (
	// lockGraceDuration は他者の編集要求から明け渡しまでの猶予。
	lockGraceDuration = 2 * time.Minute
	// lockWaiterGoneAfter はこの時間ポーリングが途絶えたら要求者は去ったとみなす猶予キャンセルの閾値。
	lockWaiterGoneAfter = 30 * time.Second
	// lockHolderStaleAfter はこの時間ポーリングが途絶えたら保持者は落ちたとみなす即解放の閾値。
	lockHolderStaleAfter = 30 * time.Second
)

// pageLock は1ページ分のロック状態です。
type pageLock struct {
	holder         string    // 保持者のユーザー名
	token          string    // ロックトークン（保存時の検証に使う）
	holderLastSeen time.Time // 保持者が最後に lock/status/save を叩いた時刻（死活検知）
	waiterLastSeen time.Time // 待機者が最後に編集を要求した時刻（ゼロ=待機者なし。離脱検知）
	graceDeadline  time.Time // 競合発生時の明け渡し期限（ゼロ=猶予なし）
}

// lockManager はページIDごとのロックを保持します。
type lockManager struct {
	mu    sync.Mutex
	locks map[int]*pageLock
}

// pageLocks はプロセス全体で共有されるロックマネージャです。
var pageLocks = &lockManager{locks: make(map[int]*pageLock)}

// newLockToken は推測困難なロックトークンを生成します。
func newLockToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// reap は now 時点で保持者を解放すべきか評価します。解放する場合はエントリを削除して
// nil を返します。呼び出し側は m.mu を保持していること。
func (m *lockManager) reap(pageID int, l *pageLock, now time.Time) *pageLock {
	if l == nil {
		return nil
	}
	if l.waiterLastSeen.IsZero() {
		// 待機者なし → 解放トリガーなし（無競合は無期限保持）。
		return l
	}
	if now.Sub(l.waiterLastSeen) > lockWaiterGoneAfter {
		// 要求者が去った → 猶予キャンセル（保持者は保持を継続）。
		l.waiterLastSeen = time.Time{}
		l.graceDeadline = time.Time{}
		return l
	}
	// 待機者は健在。保持者の死活 or 猶予満了で解放する。
	if now.Sub(l.holderLastSeen) > lockHolderStaleAfter {
		delete(m.locks, pageID) // 保持者が落ちた → 即解放
		return nil
	}
	if !l.graceDeadline.IsZero() && now.After(l.graceDeadline) {
		delete(m.locks, pageID) // 猶予満了 → 強制明け渡し
		return nil
	}
	return l
}

// AcquireResult は取得試行の結果です。
type AcquireResult struct {
	Acquired       bool
	Token          string        // 取得成功時のロックトークン
	Holder         string        // busy 時の現保持者
	GraceRemaining time.Duration // busy 時の明け渡しまでの残り（猶予未起動なら 0）
}

// tryAcquireAt は user がページのロックを取得しようとします（now を明示指定）。
func (m *lockManager) tryAcquireAt(pageID int, user string, now time.Time) AcquireResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	l := m.reap(pageID, m.locks[pageID], now)
	if l == nil {
		// 空き → 取得
		token := newLockToken()
		m.locks[pageID] = &pageLock{holder: user, token: token, holderLastSeen: now}
		return AcquireResult{Acquired: true, Token: token}
	}
	if l.holder == user {
		// 自分が保持中 → 既存トークンを返し、死活時刻を更新。
		l.holderLastSeen = now
		return AcquireResult{Acquired: true, Token: l.token}
	}
	// 他者が保持中 → busy。競合トリガーを起動/更新する。
	if l.graceDeadline.IsZero() {
		l.graceDeadline = now.Add(lockGraceDuration)
	}
	l.waiterLastSeen = now
	var rem time.Duration
	if d := l.graceDeadline.Sub(now); d > 0 {
		rem = d
	}
	return AcquireResult{Acquired: false, Holder: l.holder, GraceRemaining: rem}
}

// TryAcquire は now=現在時刻で tryAcquireAt を呼びます。
func (m *lockManager) TryAcquire(pageID int, user string) AcquireResult {
	return m.tryAcquireAt(pageID, user, time.Now())
}

// LockStatus は status エンドポイント向けの状態です。
type LockStatus struct {
	IsHolder       bool
	Holder         string
	WaiterPresent  bool
	GraceRemaining time.Duration
	Lost           bool // 自分が保持していたはずだが失った（明け渡し済み）
}

// statusAt は保持者の死活更新＋待機者通知のための状態を返します（now を明示指定）。
func (m *lockManager) statusAt(pageID int, user, token string, now time.Time) LockStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	l := m.reap(pageID, m.locks[pageID], now)
	if l == nil {
		// 誰も保持していない。自分がトークンを持っていたなら失効＝lost。
		return LockStatus{Lost: token != ""}
	}
	if l.holder == user && l.token == token {
		// 自分が保持中 → 死活信号として更新し、待機状況を返す。
		l.holderLastSeen = now
		waiter := !l.waiterLastSeen.IsZero()
		var rem time.Duration
		if waiter && !l.graceDeadline.IsZero() {
			if d := l.graceDeadline.Sub(now); d > 0 {
				rem = d
			}
		}
		return LockStatus{IsHolder: true, Holder: user, WaiterPresent: waiter, GraceRemaining: rem}
	}
	// 他者が保持中（または自分のトークンが失効）。
	return LockStatus{IsHolder: false, Holder: l.holder, Lost: token != ""}
}

// Status は now=現在時刻で statusAt を呼びます。
func (m *lockManager) Status(pageID int, user, token string) LockStatus {
	return m.statusAt(pageID, user, token, time.Now())
}

// validateAt は保存時のロックトークン検証です（now を明示指定）。
// ロックが存在しない場合は許可します（無競合＝誰も保持していない。フロント未対応でも保存できる）。
// 他者が保持中、または自分のトークンが失効している場合のみ拒否します。
func (m *lockManager) validateAt(pageID int, user, token string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	l := m.reap(pageID, m.locks[pageID], now)
	if l == nil {
		return true // ロック無し → 保存を許可（無競合）
	}
	if l.holder == user && l.token == token {
		l.holderLastSeen = now // 保存も死活信号とみなす
		return true
	}
	return false
}

// Validate は now=現在時刻で validateAt を呼びます。
func (m *lockManager) Validate(pageID int, user, token string) bool {
	return m.validateAt(pageID, user, token, time.Now())
}

// Release は保持者本人による明示解放です（トークン一致時のみ）。
func (m *lockManager) Release(pageID int, user, token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.locks[pageID]
	if l != nil && l.holder == user && l.token == token {
		delete(m.locks, pageID)
	}
}

// ForceRelease は admin による強制解除です（保持者が落ちてスタックした場合の救済）。
func (m *lockManager) ForceRelease(pageID int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.locks, pageID)
}
