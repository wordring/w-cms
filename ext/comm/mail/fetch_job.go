package mail

// ─────────────────────────────────────────────────────────────────────────
// 新しいメールの読み込みを裏で続ける（2026-10-09）
//
// 利用者:「新しいメールの読み込みは、ページを閉じても継続するようにしてください」。
// それまでは通信箱の「📥 新しいメールを読み込む」が、受信と送信の箱を順に `/api/mail/import` で1回ずつ（箱ごとに
// 50通まで）呼び、取り込みは**要求の文脈で**動いていた——ページを閉じると要求が切れて途中で止まり、次の箱は呼ばれない。
// 50通を超える分は「もう一度押してください」だった。
//
//   POST /api/mail/fetch-new         … 読み込みを始める（利用者ごとに1つ——動いていれば始めずに、その様子を返す）
//   GET  /api/mail/fetch-new/status  … いまの様子（動いているか・いまの箱・箱ごとの数・終わった時刻）
//
// 仕事は**要求と切り離した文脈で**、受信と送信の箱を、新しいメールが尽きるまで50通ずつ取り込みます（1回分は
// ImportMessages——重複は Message-ID で弾く）。画面は様子を見に来るだけで、閉じても仕事は続きます。
//
// ⚠ 起動は人が押したときだけ（§3 人間ゲート型——自動で回し続けない）。
// ⚠ 様子はサーバーの覚えだけです——**サーバーを入れ替えると、動いていた仕事はそこで止まります**（取り込んだ分は残る・
//   押し直せば続きから——重複は弾くので二重には入らない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// fetchFolders は読み込む箱の順です（受信 → 送信）。
var fetchFolders = []string{"受信", FolderSent}

// fetchTimeout は1回の読み込み全体の上限です（過去分を大量に取り込むときの歯止め——残りは押し直せば続く）。
const fetchTimeout = 30 * time.Minute

// importBatch は1回分（箱ごとに importMax 通まで）の取り込みです。signedIn はサインインしているかです。
// どちらも試験で差し替えます（本物はメールのサーバーへ繋ぐ）。
var (
	importBatch = ImportMessages
	signedIn    = func(username string) bool { return SignedInAddress(username) != "" }
)

// FetchFolderStatus は箱1つの数です。
type FetchFolderStatus struct {
	Imported  int `json:"imported"`
	Duplicate int `json:"duplicate"`
	Failed    int `json:"failed"`
}

// FetchStatus は利用者ごとの読み込みの様子です。Run は始めるたびに増える番号（画面が「自分の始めた回」を見分ける）。
type FetchStatus struct {
	Run        int                           `json:"run"`
	Running    bool                          `json:"running"`
	Folder     string                        `json:"folder,omitempty"` // いま読んでいる箱
	StartedAt  string                        `json:"started_at"`
	FinishedAt string                        `json:"finished_at,omitempty"`
	Folders    map[string]*FetchFolderStatus `json:"folders"`
	Errors     []string                      `json:"errors,omitempty"`
	Titles     []string                      `json:"titles,omitempty"` // 取り込んだものの題（先頭のいくつか）
}

// clone は様子の写しです（裏の仕事が書き換えている最中のものを、そのまま JSON にしない）。
func (s *FetchStatus) clone() FetchStatus {
	c := *s
	c.Folders = map[string]*FetchFolderStatus{}
	for k, v := range s.Folders {
		f := *v
		c.Folders[k] = &f
	}
	c.Errors = append([]string(nil), s.Errors...)
	c.Titles = append([]string(nil), s.Titles...)
	return c
}

var fetchJobs = struct {
	sync.Mutex
	by   map[string]*FetchStatus
	runs int
}{by: map[string]*FetchStatus{}}

// startFetch は username の読み込みを裏で始め、その様子の写しと、始めたか（既に動いていれば false）を返します。
func startFetch(username string) (FetchStatus, bool) {
	fetchJobs.Lock()
	defer fetchJobs.Unlock()
	if st := fetchJobs.by[username]; st != nil && st.Running {
		return st.clone(), false
	}
	fetchJobs.runs++
	st := &FetchStatus{Run: fetchJobs.runs, Running: true, StartedAt: time.Now().Format(time.RFC3339),
		Folders: map[string]*FetchFolderStatus{}}
	for _, f := range fetchFolders {
		st.Folders[f] = &FetchFolderStatus{}
	}
	fetchJobs.by[username] = st
	go runFetch(username, st)
	return st.clone(), true
}

// fetchStatusOf は username の読み込みの様子の写しです（一度も始めていなければ ok=false）。
func fetchStatusOf(username string) (FetchStatus, bool) {
	fetchJobs.Lock()
	defer fetchJobs.Unlock()
	st := fetchJobs.by[username]
	if st == nil {
		return FetchStatus{}, false
	}
	return st.clone(), true
}

// runFetch は裏の仕事の本体です——受信・送信の箱を、新しいメールが尽きるまで importMax 通ずつ取り込みます。
func runFetch(username string, st *FetchStatus) {
	update := func(f func()) {
		fetchJobs.Lock()
		defer fetchJobs.Unlock()
		f()
	}
	total := FetchFolderStatus{}
	// ⚠ **途中で落ちても「動いている」のまま残さない**（残すと二度と始められない）。
	defer func() {
		if p := recover(); p != nil {
			log.Printf("メールの読み込みが落ちました user=%q: %v", username, p)
			update(func() { st.Errors = append(st.Errors, fmt.Sprint("途中で止まりました: ", p)) })
		}
		update(func() {
			st.Running = false
			st.Folder = ""
			st.FinishedAt = time.Now().Format(time.RFC3339)
		})
		auth.Audit(username, "mail.import", fmt.Sprintf("background imported=%d duplicate=%d failed=%d",
			total.Imported, total.Duplicate, total.Failed))
	}()
	// ⚠ **要求の文脈を使いません**——ページを閉じても続けるため（上限だけ置く）。
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	for _, folder := range fetchFolders {
		update(func() { st.Folder = folder })
		for {
			sum, err := importBatch(ctx, username, ListOptions{Folder: folder, Max: importMax})
			update(func() {
				f := st.Folders[folder]
				f.Imported += sum.Imported
				f.Duplicate += sum.Duplicate
				f.Failed += sum.Failed
				for _, t := range sum.Titles {
					if len(st.Titles) < 20 {
						st.Titles = append(st.Titles, t)
					}
				}
			})
			total.Imported += sum.Imported
			total.Duplicate += sum.Duplicate
			total.Failed += sum.Failed
			if err != nil {
				msg := folder + ": " + err.Error()
				if errors.Is(err, context.DeadlineExceeded) {
					msg = fmt.Sprintf("時間の上限（%d分）で止めました——もう一度押すと続きを読みます", int(fetchTimeout/time.Minute))
				}
				update(func() { st.Errors = append(st.Errors, msg) })
				if errors.Is(err, errNotSignedIn) || ctx.Err() != nil {
					return // サインインしていない・時間切れ——残りの箱も読めない
				}
				break // この箱は諦めて次の箱へ
			}
			if sum.Imported < importMax {
				break // 尽きた（1回分に満たない）
			}
		}
	}
}

// MailFetchAPIHandler は POST /api/mail/fetch-new です——新しいメールの読み込みを裏で始めます（入力なし）。
// 応答: {success, started（始めたか——既に動いていれば false）, status}。
func MailFetchAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	if !signedIn(user.Username) {
		cms.JSONFail(w, http.StatusConflict, "メールアカウントにサインインしていません")
		return
	}
	st, started := startFetch(user.Username)
	cms.WriteJSON(w, map[string]any{"success": true, "started": started, "status": st})
}

// MailFetchStatusAPIHandler は GET /api/mail/fetch-new/status です。
// 応答: {success, status（一度も始めていなければ null）}。
func MailFetchStatusAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONGet(w, r)
	if !ok {
		return
	}
	var out any
	if st, ok := fetchStatusOf(user.Username); ok {
		out = st
	}
	cms.WriteJSON(w, map[string]any{"success": true, "status": out})
}
