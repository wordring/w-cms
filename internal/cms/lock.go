package cms

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────
// 同時編集の悲観ロック（ページ単位）— 競合トリガー方式・SSEプッシュ
//
// 設計の根拠と決定ログは [docs/【考察】同時編集の競合対策.md] を参照。要点:
//   - ロックは ephemeral（揮発的）なランタイム状態。プロセス内の mutex 付き map で持つ
//     （単一プロセス前提。再起動で全ロックが消えるのは許容）。
//   - presence は **SSE 接続の有無** で判定する（ポーリングしない）。保持者の接続が切れたら
//     即明け渡し可能、待機者の接続が切れたら猶予をキャンセルする。
//   - 無競合なら無期限保持。待機者（別エディタ）が接続した時点から 2 分の猶予で明け渡す。
//   - 状態変化はサーバーから各購読者へ push する（broadcast）。
// ─────────────────────────────────────────────────────────────────────────

// 時間パラメータ（テストから差し替えられるよう var）。
var (
	// lockGraceDuration は他者の編集要求から明け渡しまでの猶予。
	lockGraceDuration = 2 * time.Minute
	// holderConnectGrace は取得直後、保持者が SSE 接続するまでの猶予（接続前に誤って奪われないため）。
	holderConnectGrace = 10 * time.Second
)

// lockEvent は SSE で push する状態通知です（JSONで送る）。
//
//	type: holder向け = "waiter"（待機者あり）/ "holding"（自分が保持・待機者なし）/ "lost"（明け渡し済み）
//	      waiter向け = "busy"（まだ埋まっている）/ "available"（空いた＝取得を試みてよい）
type lockEvent struct {
	Type              string `json:"type"`
	Holder            string `json:"holder,omitempty"`
	SameUser          bool   `json:"same_user,omitempty"`
	GraceRemainingSec int    `json:"grace_remaining_sec,omitempty"`
}

// subscriber は1つの SSE 接続です。
type subscriber struct {
	role  string // "holder" | "waiter"
	user  string
	token string // holder: ロックトークン / waiter: ""
	ch    chan lockEvent
}

// pageLock は1ページ分のロック保持状態です（保持中のみ存在）。
type pageLock struct {
	holder        string
	token         string
	acquiredAt    time.Time // 取得時刻（接続前の保持者を present とみなす猶予に使う）
	holderConn    bool      // 保持者の SSE 接続が生きているか
	graceDeadline time.Time // 競合発生時の明け渡し期限（ゼロ=猶予なし）
}

// lockManager はページIDごとのロックと購読者を保持します。
type lockManager struct {
	mu    sync.Mutex
	locks map[int]*pageLock
	subs  map[int]map[*subscriber]struct{}
}

// pageLocks はプロセス全体で共有されるロックマネージャです。
var pageLocks = newLockManager()

func newLockManager() *lockManager {
	return &lockManager{
		locks: make(map[int]*pageLock),
		subs:  make(map[int]map[*subscriber]struct{}),
	}
}

// newLockToken は推測困難なロックトークンを生成します。
func newLockToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// 以降の小文字メソッドは、すべて m.mu を保持して呼ぶこと。

func (m *lockManager) anyWaiter(pageID int) bool {
	for s := range m.subs[pageID] {
		if s.role == "waiter" {
			return true
		}
	}
	return false
}

// holderPresent は保持者が存在し、かつ接続中（または取得直後の猶予内）かを返します。
func (m *lockManager) holderPresent(l *pageLock, now time.Time) bool {
	if l == nil || l.holder == "" {
		return false
	}
	return l.holderConn || now.Sub(l.acquiredAt) < holderConnectGrace
}

func remainingSec(l *pageLock, now time.Time) int {
	if l == nil || l.graceDeadline.IsZero() {
		return 0
	}
	if d := l.graceDeadline.Sub(now); d > 0 {
		return int(d.Seconds())
	}
	return 0
}

func pushNonBlocking(ch chan lockEvent, ev lockEvent) {
	select {
	case ch <- ev:
	default: // バッファ満杯（受信側が遅い/死亡）→ 取りこぼしは許容（次の broadcast で回復）
	}
}

// broadcast は現在のロック状態を各購読者へ push します（m.mu 保持必須）。
func (m *lockManager) broadcast(pageID int, now time.Time) {
	l := m.locks[pageID]
	waiter := m.anyWaiter(pageID)
	rem := remainingSec(l, now)
	for s := range m.subs[pageID] {
		var ev lockEvent
		if s.role == "holder" {
			switch {
			case l == nil || l.token != s.token:
				ev = lockEvent{Type: "lost"}
			case waiter:
				ev = lockEvent{Type: "waiter", GraceRemainingSec: rem}
			default:
				ev = lockEvent{Type: "holding"}
			}
		} else { // waiter
			if !m.holderPresent(l, now) {
				ev = lockEvent{Type: "available"}
			} else {
				ev = lockEvent{Type: "busy", Holder: l.holder, SameUser: l.holder == s.user, GraceRemainingSec: rem}
			}
		}
		pushNonBlocking(s.ch, ev)
	}
}

// AcquireResult は取得試行の結果です。
type AcquireResult struct {
	Acquired       bool
	Token          string
	Holder         string
	SameUser       bool
	GraceRemaining time.Duration
}

// TryAcquire はロック取得を試みます。保持者は「エディタ個体＝トークン」で識別し、token に
// 現在の保持トークンを提示した場合のみ同一エディタの再取得として許可します（同一ユーザーの
// 別タブも competition）。保持者が接続を失っていて待機者がいる場合は空きとみなして取得します。
func (m *lockManager) TryAcquire(pageID int, user, token string) AcquireResult {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	l := m.locks[pageID]
	if l != nil && !m.holderPresent(l, now) && m.anyWaiter(pageID) {
		delete(m.locks, pageID) // 保持者不在＋待機者あり → 明け渡し済み扱い
		l = nil
	}
	if l == nil {
		tk := newLockToken()
		m.locks[pageID] = &pageLock{holder: user, token: tk, acquiredAt: now}
		m.broadcast(pageID, now)
		return AcquireResult{Acquired: true, Token: tk}
	}
	if token != "" && l.token == token {
		l.acquiredAt = now
		return AcquireResult{Acquired: true, Token: l.token}
	}
	var rem time.Duration
	if d := l.graceDeadline.Sub(now); d > 0 {
		rem = d
	}
	return AcquireResult{Acquired: false, Holder: l.holder, SameUser: l.holder == user, GraceRemaining: rem}
}

// Validate は保存時のロックトークン検証です。ロックが無ければ許可（無競合）、
// 他者保持／トークン失効なら拒否します。
func (m *lockManager) Validate(pageID int, user, token string) bool {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.locks[pageID]
	if l == nil {
		return true
	}
	if l.holder == user && l.token == token {
		l.acquiredAt = now
		return true
	}
	return false
}

// Release は保持者本人による明示解放です（トークン一致時のみ）。
func (m *lockManager) Release(pageID int, user, token string) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.locks[pageID]
	if l != nil && l.holder == user && l.token == token {
		delete(m.locks, pageID)
		m.broadcast(pageID, now)
	}
}

// ForceRelease は admin による強制解除です（保持者が落ちてスタックした場合の救済）。
func (m *lockManager) ForceRelease(pageID int) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.locks[pageID]; ok {
		delete(m.locks, pageID)
		m.broadcast(pageID, now)
	}
}

// subscribe は SSE 購読者を登録し、現在状態を push したうえで購読者を返します。
func (m *lockManager) subscribe(pageID int, role, user, token string) *subscriber {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	s := &subscriber{role: role, user: user, token: token, ch: make(chan lockEvent, 8)}
	if m.subs[pageID] == nil {
		m.subs[pageID] = make(map[*subscriber]struct{})
	}
	m.subs[pageID][s] = struct{}{}

	l := m.locks[pageID]
	if role == "holder" && l != nil && l.token == token {
		l.holderConn = true
	}
	if role == "waiter" && m.holderPresent(l, now) && l.graceDeadline.IsZero() {
		l.graceDeadline = now.Add(lockGraceDuration) // 待機者の接続で猶予を起動
	}
	m.broadcast(pageID, now)
	return s
}

// unsubscribe は購読者を解除し、presence 変化に応じてロックを再評価します。
func (m *lockManager) unsubscribe(pageID int, s *subscriber) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	if subs := m.subs[pageID]; subs != nil {
		if _, ok := subs[s]; ok {
			delete(subs, s)
			close(s.ch)
		}
		if len(subs) == 0 {
			delete(m.subs, pageID)
		}
	}

	l := m.locks[pageID]
	if s.role == "holder" && l != nil && l.token == s.token {
		l.holderConn = false
		if !m.holderPresent(l, now) && m.anyWaiter(pageID) {
			delete(m.locks, pageID) // 保持者が去り待機者がいる → 即明け渡し
		}
	}
	if s.role == "waiter" && l != nil && !m.anyWaiter(pageID) {
		l.graceDeadline = time.Time{} // 最後の待機者が去った → 猶予キャンセル
	}
	m.broadcast(pageID, now)
}

// tick は猶予満了・presence 変化を定期評価します（StartLockReaper から1秒間隔で呼ぶ）。
func (m *lockManager) tick(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, l := range m.locks {
		waiter := m.anyWaiter(id)
		switch {
		case waiter && !m.holderPresent(l, now):
			delete(m.locks, id) // 保持者不在＋待機者あり → 明け渡し
			m.broadcast(id, now)
		case waiter && l.graceDeadline.IsZero():
			l.graceDeadline = now.Add(lockGraceDuration)
			m.broadcast(id, now)
		case waiter && now.After(l.graceDeadline):
			delete(m.locks, id) // 猶予満了 → 強制明け渡し
			m.broadcast(id, now)
		case !waiter && !l.graceDeadline.IsZero():
			l.graceDeadline = time.Time{} // 待機者なし → 猶予解除
			m.broadcast(id, now)
		}
	}
}

// StartLockReaper は猶予満了などを定期評価するバックグラウンド goroutine を起動します
// （main から1回だけ呼ぶ）。
func StartLockReaper() {
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for range t.C {
			pageLocks.tick(time.Now())
		}
	}()
}
