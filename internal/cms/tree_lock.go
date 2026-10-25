package cms

import (
	"errors"
	"os"
	"sync"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ─────────────────────────────────────────────────────────────────────────
// 木の付け替えの見張り（2026-10-26・【考察】コードの矛盾 §4.2）
//
// ページの削除は「子ページが無いか」を数えてからごみ箱へ移します。ところが数えてから移すまでのあいだに、ほかの人が
// そのページの下に子ページを作る（取り込み・整理・新しいページ）・子ページを動かしてくると、子ページが、ごみ箱へ移った
// 親を指したまま残りました（画面の木から見えないページ）。
//
// 子を足す側（作る・親を付け替える）と、消す側の「子が無いか数えて、消すと決める」を、treeMu で1つずつ通します。
//   - 消す側は、treeMu の中で子を数え、無ければ deleting に印を付けてから、treeMu の外でごみ箱へ移す（移すにはページの
//     錠が要る——treeMu を持ったままページの錠を待つと、本文を書き替えながら子を作る道と互いに待ち合う）。
//   - 足す側は、treeMu の中で、親が在って消されかけていないかを見てから、pages に行を足す（足した行は、あとから
//     数える消す側に見える）。
// ─────────────────────────────────────────────────────────────────────────

var tree struct {
	sync.Mutex
	deleting map[int]bool
}

// ErrParentGone は、子を足そうとした親ページが無い（消された・消されかけている）ことです。
var ErrParentGone = errors.New("親のページがありません（ごみ箱へ移ったか、いま消しているところです）")

// beginDeleteIfChildless は、ページ id に子が無ければ「消している」印を付けて true と終わりの関数を返します。
// 子があれば false と子の数。終わりの関数は、消し終えた（または消せなかった）ときに呼ぶ。
func beginDeleteIfChildless(id int) (ok bool, children int, done func(), err error) {
	tree.Lock()
	defer tree.Unlock()
	if err := database.DB.QueryRow("SELECT COUNT(*) FROM pages WHERE parent_id = ?", id).Scan(&children); err != nil {
		return false, 0, nil, err
	}
	if children > 0 {
		return false, children, nil, nil
	}
	if tree.deleting == nil {
		tree.deleting = map[int]bool{}
	}
	tree.deleting[id] = true
	return true, 0, func() {
		tree.Lock()
		delete(tree.deleting, id)
		tree.Unlock()
	}, nil
}

// beginDelete は、子の有無を見ずに「消している」印を付けます（拡張の中の、子ごと動かしてから消す道など）。
// 終わりの関数は、消し終えたときに呼ぶ。
func beginDelete(id int) func() {
	tree.Lock()
	if tree.deleting == nil {
		tree.deleting = map[int]bool{}
	}
	tree.deleting[id] = true
	tree.Unlock()
	return func() {
		tree.Lock()
		delete(tree.deleting, id)
		tree.Unlock()
	}
}

// withLiveParent は、親ページ parent が在って消されかけていないことを treeMu の中で確かめてから add を呼びます
// （add の中で pages の行を足す・親を付け替える）。親が無ければ ErrParentGone。
func withLiveParent(parent int, add func() error) error {
	tree.Lock()
	defer tree.Unlock()
	if tree.deleting[parent] {
		return ErrParentGone
	}
	if parent == 0 {
		return add() // トップページは消せない
	}
	// 在るかは、ページのフォルダ（正本）か索引の行のどちらかで見る（ごみ箱へ移すとフォルダが無くなる・消し終えると行も無くなる）。
	var exists bool
	if fi, err := os.Stat(page.GetPageDir(page.FormatID(parent))); err == nil && fi.IsDir() {
		exists = true
	} else if err := database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM pages WHERE id = ?)", parent).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrParentGone
	}
	return add()
}
