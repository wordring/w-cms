package cms

import (
	"os"
	"strconv"
	"sync"

	"w-cms/internal/cms/page"
)

// ─────────────────────────────────────────────────────────────────────────
// ページ番号を使い回さない（2026-10-26）
//
// pages の id は SQLite の自動採番（INTEGER PRIMARY KEY・AUTOINCREMENT なし）で、いちばん大きい番号のページを消すと、
// 次に作るページが同じ番号をもらっていました（本番のごみ箱に、同じ番号が何度も入っていた）。番号はほかのページの参照・
// リンク・ファイル表示が指す名前なので、使い回すと、消したページを指していた所が、関係の無い新しいページを開きます。
//
// 新しい番号は「いまのページ」と「ごみ箱のページ」のどちらよりも大きくします。ごみ箱は data/trash/<2桁>/<番号>[-n] の形。
// 索引（cms.db）は作り直せるので、番号の上限を DB には持たない——ごみ箱のフォルダが正本です。
// ─────────────────────────────────────────────────────────────────────────

var trashFloor struct {
	sync.Mutex
	loaded bool
	n      int
}

// trashHighWater は、ごみ箱にあるページのいちばん大きい番号です（初めの1回だけフォルダを数え、あとは noteTrashed で上げる）。
func trashHighWater() int {
	trashFloor.Lock()
	defer trashFloor.Unlock()
	if !trashFloor.loaded {
		trashFloor.loaded = true
		trashFloor.n = scanTrashHighWater()
	}
	return trashFloor.n
}

// noteTrashed は、ページ id をごみ箱へ移したことを、番号の上限に入れます。
func noteTrashed(id string) {
	n, err := strconv.Atoi(id)
	if err != nil {
		return
	}
	trashFloor.Lock()
	defer trashFloor.Unlock()
	if trashFloor.loaded && n > trashFloor.n {
		trashFloor.n = n
	}
}

// masterHighWater は、正本（data/master）のフォルダの名前から、いちばん大きいページ番号を数えます（2026-10-26）。
// 新しい番号がディスクのページと重なったときだけ呼ぶ（reserveNewPageID）——索引が作り直しの途中でも、ディスクが正本。
func masterHighWater() int { return scanHighWater(page.MasterDir()) }

// scanTrashHighWater はごみ箱のフォルダの名前から、いちばん大きいページ番号を数えます。
func scanTrashHighWater() int { return scanHighWater(page.TrashDir()) }

// scanHighWater は、base/<2桁>/<番号>[-n] の形のフォルダの名前から、いちばん大きいページ番号を数えます。
func scanHighWater(base string) int {
	max := 0
	shards, err := os.ReadDir(base)
	if err != nil {
		return 0
	}
	for _, s := range shards {
		if !s.IsDir() {
			continue
		}
		entries, err := os.ReadDir(base + string(os.PathSeparator) + s.Name())
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			digits := 0
			for digits < len(name) && name[digits] >= '0' && name[digits] <= '9' {
				digits++
			}
			if digits == 0 {
				continue
			}
			if n, err := strconv.Atoi(name[:digits]); err == nil && n > max {
				max = n
			}
		}
	}
	return max
}
