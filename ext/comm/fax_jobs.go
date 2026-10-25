package comm

// 送った FAX の仕事の控え（2026-10-26・fax_send.go）。
//
// 送った FAX の、HylaFAX の仕事の番号・記録のページ・送った人・元のページ・用件・送った紙を、サーバーのデータの置き場
// （data/fax/jobs.json）に控えます。送れたかの見張り・送れたときの用件の後始末（送付日など）・サーバーを起こし直したときの
// 見張りの続き・仕事の様子を見せる相手の確かめは、どれもこの控えだけを見ます。
//
// ⚠ 2026-10-26 まで、これらを記録のページのタグ（FAX送信の様子・FAX送信番号・FAX送信の用件・親ページID など）から読んで
// いました。タグは記録に書ける人ならだれでも書き替えられるので、書けないページを「送った元」にしたタグを書いておくと、
// サーバーを起こし直したときに、記録を作った人（管理者のことがある）として、そのページに送付日や発注済みを書けました。
// タグは、人が読むための写しとして残しています。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// faxJob は送った FAX の仕事1つの控えです。
type faxJob struct {
	Job     string    `json:"job"`     // HylaFAX の仕事の番号
	Record  string    `json:"record"`  // 送った FAX の記録のページ
	User    string    `json:"user"`    // 送った人（用件の後始末はこの人として行う）
	Page    string    `json:"page"`    // 送った元のページ
	Purpose string    `json:"purpose"` // 送る欄の用件（PDF の「⋯」から送ったときは空）
	File    string    `json:"file"`    // 送ったと記録する紙（元のページの添付の保存名）
	Number  string    `json:"number"`  // 送り先の番号
	Started time.Time `json:"started"`
	Done    bool      `json:"done,omitempty"` // 見張りを終えた（送れた・送れなかった・分からない）
	Ended   time.Time `json:"ended,omitempty"`
}

// faxJobsKeep は、見張りを終えた控えを残す長さです（様子を見る口が答えるため）。
const faxJobsKeep = 30 * 24 * time.Hour

var faxJobs struct {
	sync.Mutex
	loaded bool
	m      map[string]*faxJob
}

func faxJobsPath() string { return database.DataPath(filepath.Join("fax", "jobs.json")) }

// loadFaxJobsLocked は控えを読みます（初めの1回だけ・faxJobs を握って呼ぶ）。読めなければ空から始める。
func loadFaxJobsLocked() {
	if faxJobs.loaded {
		return
	}
	faxJobs.loaded = true
	faxJobs.m = map[string]*faxJob{}
	b, err := os.ReadFile(faxJobsPath())
	if err != nil {
		return
	}
	var list []*faxJob
	if json.Unmarshal(b, &list) != nil {
		return
	}
	for _, j := range list {
		if j != nil && j.Job != "" {
			faxJobs.m[j.Job] = j
		}
	}
}

// saveFaxJobsLocked は控えを書きます（faxJobs を握って呼ぶ）。見張りを終えて古くなったものは落とす。
func saveFaxJobsLocked() error {
	cut := time.Now().Add(-faxJobsKeep)
	list := make([]*faxJob, 0, len(faxJobs.m))
	for k, j := range faxJobs.m {
		if j.Done && j.Ended.Before(cut) {
			delete(faxJobs.m, k)
			continue
		}
		list = append(list, j)
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Started.Before(list[b].Started) })
	b, err := json.MarshalIndent(list, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(faxJobsPath()), 0o700); err != nil {
		return err
	}
	return page.WriteFileAtomic(faxJobsPath(), b, 0o600)
}

// putFaxJob は控えを足します。
func putFaxJob(j faxJob) error {
	faxJobs.Lock()
	defer faxJobs.Unlock()
	loadFaxJobsLocked()
	faxJobs.m[j.Job] = &j
	return saveFaxJobsLocked()
}

// sentFaxJobOf は仕事 job の控えです。
func sentFaxJobOf(job string) (faxJob, bool) {
	faxJobs.Lock()
	defer faxJobs.Unlock()
	loadFaxJobsLocked()
	j, ok := faxJobs.m[job]
	if !ok {
		return faxJob{}, false
	}
	return *j, true
}

// finishFaxJob は、仕事 job の見張りを終えたことを控えます。
func finishFaxJob(job string) {
	faxJobs.Lock()
	defer faxJobs.Unlock()
	loadFaxJobsLocked()
	if j, ok := faxJobs.m[job]; ok {
		j.Done, j.Ended = true, time.Now()
		_ = saveFaxJobsLocked()
	}
}

// activeFaxJobs は、まだ見張っている（送れたか分かっていない）仕事の控えです。
func activeFaxJobs() []faxJob {
	faxJobs.Lock()
	defer faxJobs.Unlock()
	loadFaxJobsLocked()
	var out []faxJob
	for _, j := range faxJobs.m {
		if !j.Done {
			out = append(out, *j)
		}
	}
	return out
}
