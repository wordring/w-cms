package cms

// 索引づけの失敗の直し直し（2026-10-30・【考察】コードの矛盾 §11.1）。
//
// 保存は 書く → 版 → 索引 の順なので、索引が失敗しても（SQLite の busy など）本文はもう書けています。画面が送り直しても、本文は同じなので
// 書き直されず、索引は呼ばれません。そのページの索引（タグ・表・リンク・案件残表 など）だけが、次に中身が変わるまで古いまま残っていました。
// そこで、失敗したページは少し待ってから、いまの本文で索引を作り直します。起動し直すと覚えていません（そのときは次の保存か DB の作り直しで直る）。

import (
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

// syncIndexOrRetry は SyncIndex で、失敗したら直し直しを予約します（誤りはそのまま返す）。
func syncIndexOrRetry(id, html string) error {
	err := SyncIndex(id, html)
	if err != nil {
		scheduleIndexRetry(id)
	}
	return err
}

// scheduleIndexRetry は、ページ id の索引の直し直しを予約します（もう予約があれば何もしない）。
func scheduleIndexRetry(id string) {
	indexRetryMu.Lock()
	defer indexRetryMu.Unlock()
	if indexRetryPending[id] {
		return
	}
	indexRetryPending[id] = true
	time.AfterFunc(indexRetryDelay, func() { retryIndex(id, 1) })
}

// retryIndex は、いまの本文で索引を作り直します。⚠ 本文は錠の中で読みます（古い本文で索引を上書きしない）。
func retryIndex(id string, attempt int) {
	unlock := LockPageBody(id)
	body, err := os.ReadFile(page.BodyPath(id))
	if err == nil {
		err = SyncIndex(id, string(body))
	} else {
		err = nil // 本文が無い（ごみ箱へ移した）——直すものが無い
	}
	unlock()
	if err == nil {
		indexRetryMu.Lock()
		delete(indexRetryPending, id)
		indexRetryMu.Unlock()
		log.Printf("索引を直し直しました page=%s（%d 回目）", id, attempt)
		return
	}
	if attempt >= indexRetryMax {
		indexRetryMu.Lock()
		delete(indexRetryPending, id)
		indexRetryMu.Unlock()
		log.Printf("⚠ 索引を直せませんでした page=%s（%d 回）: %v——次の保存か DB の作り直しで直ります", id, attempt, err)
		return
	}
	time.AfterFunc(indexRetryDelay, func() { retryIndex(id, attempt+1) })
}
