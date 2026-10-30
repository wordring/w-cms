package cms

// 索引づけの失敗の直し直し（2026-10-30・【考察】コードの矛盾 §11.1・§13.1）。
//
// 保存は 書く → 版 → 索引 の順なので、索引が失敗しても（SQLite の busy など）本文はもう書けています。画面が送り直しても、本文は同じなので
// 書き直されず、索引は呼ばれません。そのページの索引（タグ・表・リンク・案件残表 など）だけが、次に中身が変わるまで古いまま残っていました。
// そこで、失敗したページは少し待ってから、いまの本文で索引を作り直します。本文が無くなっていれば（ごみ箱へ移した）、索引から消します。
//
// **予約するのは SyncIndex と PurgePageIndex 自身です**——口ごとに呼び分けると、取り込み・複製・置き場づくりが漏れました。
// 表の写し（tables.db）だけが失敗したときも予約します（保存は止めない。写しは派生なので、失敗しても SyncIndex は誤りを返さない）。
// 起動し直すと覚えていません（そのときは次の保存か DB の作り直しで直る）。

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"sync"
	"time"

	"w-cms/internal/cms/page"
)

// 直し直しの間隔と回数です（試験で短くする）。
var (
	indexRetryDelay = 30 * time.Second
	indexRetryMax   = 10
)

var (
	indexRetryMu      sync.Mutex
	indexRetryPending = map[string]bool{}
)

// scheduleIndexRetry は、ページ id の索引の直し直しを予約します（もう予約があれば何もしない）。
func scheduleIndexRetry(id string) {
	indexRetryMu.Lock()
	defer indexRetryMu.Unlock()
	if indexRetryPending[id] {
		return
	}
	indexRetryPending[id] = true
	dir := page.MasterDir()
	time.AfterFunc(indexRetryDelay, func() { retryIndex(id, 1, dir) })
}

// retryIndex は、いまの本文で索引を作り直します。⚠ 本文は錠の中で読みます（古い本文で索引を上書きしない）。
// dir は予約したときのデータの場所です（場所が替わっていたら、別のデータなので何もしない）。
func retryIndex(id string, attempt int, dir string) {
	finish := func() {
		indexRetryMu.Lock()
		delete(indexRetryPending, id)
		indexRetryMu.Unlock()
	}
	if page.MasterDir() != dir {
		finish()
		return
	}
	unlock := LockPageBody(id)
	var tablesErr error
	body, err := os.ReadFile(page.BodyPath(id))
	switch {
	case err == nil:
		err = syncPageIndex(id, string(body), &tablesErr)
	case errors.Is(err, fs.ErrNotExist):
		tablesErr, err = purgePageIndex(id)
	}
	unlock()
	if err == nil {
		err = tablesErr
	}
	if err == nil {
		finish()
		log.Printf("索引を直し直しました page=%s（%d 回目）", id, attempt)
		return
	}
	if attempt >= indexRetryMax {
		finish()
		log.Printf("⚠ 索引を直せませんでした page=%s（%d 回）: %v——次の保存か DB の作り直しで直ります", id, attempt, err)
		return
	}
	time.AfterFunc(indexRetryDelay, func() { retryIndex(id, attempt+1, dir) })
}
