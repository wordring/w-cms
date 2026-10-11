package cms

import (
	"sync"

	"w-cms/internal/cms/page"
)

// ─────────────────────────────────────────────────────────────────────────
// ページごとの書き込みの錠（2026-10-12）
//
// 利用者:「排他のために編集モードを設けたが、今後は編集モードの代わりに、閲覧に邪魔なものを表示しない目的で、
// 閲覧モードを設けることにします。そして、ブロック単位の保護に変更し…」（【考察】編集モードの再考 §6）。
//
// 編集権（人がページを握る・editlock）をやめると、人の保存と業務のボタン（RewriteBody）が同じページへ同時に
// 書きうる。どちらも本文のファイルを**読んで・変えて・書き戻す**ので、重なると片方が黙って消える。
// そこで本文を書く道はすべて、**1回の書き込みのあいだだけ**この錠を取る（人が握り続ける編集権とは別物）。
//
// ⚠ **錠を取ったまま、同じページの本文を書く関数を呼ばないこと**（同じ錠を2度取ると止まる——Go の
//    sync.Mutex は再入できない）。取るのは書き込みのいちばん外側（保存の口・rewritePageBody・版を戻す）だけ。
// ─────────────────────────────────────────────────────────────────────────

var bodyLocks = struct {
	sync.Mutex
	m map[string]*sync.Mutex
}{m: map[string]*sync.Mutex{}}

// LockPageBody はページ id の本文の錠を取り、外す関数を返します（`defer cms.LockPageBody(id)()`）。
// id はここで6桁へ畳みます（"12" と "000012" を同じ錠にする）。
func LockPageBody(id string) func() {
	if norm, ok := page.NormalizeID(id); ok {
		id = norm
	}
	bodyLocks.Lock()
	mu := bodyLocks.m[id]
	if mu == nil {
		mu = &sync.Mutex{}
		bodyLocks.m[id] = mu
	}
	bodyLocks.Unlock()
	mu.Lock()
	return mu.Unlock
}
