package cms

// ─────────────────────────────────────────────────────────────────────────
// ページを取り除く前の確かめ（2026-10-28）
//
// 拡張が「このページは、この人には消させない・この置き場の外へ動かさせない」を決める登録の仕組みです。
// コアは、ごみ箱へ移す口（DeletePageAPIHandler）と、親を替える口（validateParentChange）でこれを呼びます。
// 動かす口も見るのは、置き場の外へ動かしてから消せば、消す口の確かめをすり抜けられるためです。
//
// 確かめるのは人の操作の口だけです。拡張が自分で書類を片付ける道（送った下書き・空になった製造ページ など）は通りません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"sync"

	"w-cms/internal/auth"
)

// RemovalGuard は、利用者 user がページ pageID を取り除く前に呼ぶ確かめです。
// newParent は動かす先のページ番号で、ごみ箱へ移すときは -1 です。断るときは理由を返します（許すなら空）。
type RemovalGuard func(user *auth.User, pageID, newParent int) string

var removalGuards struct {
	sync.Mutex
	fns []RemovalGuard
}

// RegisterRemovalGuard は確かめを登録します（拡張の init() から）。
func RegisterRemovalGuard(fn RemovalGuard) {
	removalGuards.Lock()
	defer removalGuards.Unlock()
	removalGuards.fns = append(removalGuards.fns, fn)
}

// removalRefusal は、登録された確かめのうち最初に断ったものの理由を返します（どれも断らなければ空）。
func removalRefusal(user *auth.User, pageID, newParent int) string {
	removalGuards.Lock()
	fns := append([]RemovalGuard{}, removalGuards.fns...)
	removalGuards.Unlock()
	for _, fn := range fns {
		if why := fn(user, pageID, newParent); why != "" {
			return why
		}
	}
	return ""
}
