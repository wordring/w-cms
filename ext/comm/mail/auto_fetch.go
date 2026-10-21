package mail

// ─────────────────────────────────────────────────────────────────────────
// 決まった間隔で新しいメールを読む（2026-10-21 夜・docs/考察/【考察】通信箱の改善.md §7.1）
//
// 利用者:「メールは日中5分間隔、夜間1時間間隔で自動で読み込むと思います」
// 問いへの答え（2026-10-21）:「会社の代表メールとして指定されたアドレス」「8時から20時までが日中。土日は夜間と同じ」
//
// それまでの決まり「起動は人が押したときだけ」（fetch_job.go の冒頭）を、利用者が変えたものです。押して読む口は残します。
//
//   - 読むのは、環境変数 WCMS_MAIL_AUTO_ADDRESS の代表のアドレスです（会社の実在のアドレスなので、リポジトリの設定には書かない
//     ——秘密の設定 w-cms.env に人が書く）。
//   - そのアドレスの M365 の口座で、だれか1人の w-cms の利用者が「✉️ メールにサインイン」でサインインしておきます。読み込みは、
//     その利用者のサインインで動きます（取り込んだ記録の書き手もその人）。
//   - 間隔は設定 `extensions.mail.auto_fetch`（平日の日中と、それ以外）。
//   - 読み込みの本体は、押したときと同じ startFetch（同じ口座の読み込みは同時に1つ・重複は Message-ID で弾く）。
//   - 様子は GET /api/mail/auto-fetch（通信箱の読み込みのボタンのそばに出す）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// autoFetchSettings は設定 `extensions.mail.auto_fetch` です。
type autoFetchSettings struct {
	DayStartHour   int  `json:"day_start_hour"`   // 日中の始まり（時・この時を含む）
	DayEndHour     int  `json:"day_end_hour"`     // 日中の終わり（時・この時を含まない）
	DayMinutes     int  `json:"day_minutes"`      // 日中の間隔（分）
	NightMinutes   int  `json:"night_minutes"`    // 夜間の間隔（分）
	WeekendAsNight bool `json:"weekend_as_night"` // 土日は夜間と同じ
}

// mailSettingsSection は設定の節 `extensions.mail` です。
type mailSettingsSection struct {
	AutoFetch *autoFetchSettings `json:"auto_fetch,omitempty"`
}

var (
	autoFetchMu  sync.Mutex
	autoFetchCfg *autoFetchSettings // nil なら自動の読み込みはしない
)

func init() {
	cms.RegisterSettingsSection("mail", parseMailSettings)
	cms.RegisterBackgroundTask("メールの自動の読み込み", startAutoFetch)
}

// parseMailSettings は節を読んで検査し、効かせる関数を返します。
func parseMailSettings(raw json.RawMessage) (func(), error) {
	var s mailSettingsSection
	if len(raw) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("書式が不正です（手で直してください）: %w", err)
		}
	}
	if a := s.AutoFetch; a != nil {
		if a.DayStartHour < 0 || a.DayStartHour > 23 || a.DayEndHour < 1 || a.DayEndHour > 24 || a.DayStartHour >= a.DayEndHour {
			return nil, fmt.Errorf("auto_fetch: 日中の時刻が読めません（day_start_hour %d・day_end_hour %d）", a.DayStartHour, a.DayEndHour)
		}
		if a.DayMinutes < 1 || a.NightMinutes < 1 {
			return nil, fmt.Errorf("auto_fetch: 間隔は1分以上です（day_minutes %d・night_minutes %d）", a.DayMinutes, a.NightMinutes)
		}
	}
	return func() {
		autoFetchMu.Lock()
		defer autoFetchMu.Unlock()
		autoFetchCfg = s.AutoFetch
	}, nil
}

// autoFetchConfig はいまの設定の写しです（無ければ nil）。
func autoFetchConfig() *autoFetchSettings {
	autoFetchMu.Lock()
	defer autoFetchMu.Unlock()
	if autoFetchCfg == nil {
		return nil
	}
	c := *autoFetchCfg
	return &c
}

// officeZone は日中・土日を決める時刻の帯です（日本の時刻・夏時間は無い）。
// ⚠ サーバーの時刻の帯には頼らない——Ubuntu のサーバー機は UTC のことがあり、そのままだと「8〜20時」が日本の17〜5時になる
// （2026-10-22 の見直し）。
var officeZone = time.FixedZone("JST", 9*60*60)

// autoFetchInterval は、時刻 now の読み込みの間隔です（平日の日中か、それ以外か。日本の時刻で決める）。
func autoFetchInterval(c autoFetchSettings, now time.Time) time.Duration {
	now = now.In(officeZone)
	h := now.Hour()
	weekend := now.Weekday() == time.Saturday || now.Weekday() == time.Sunday
	if (!weekend || !c.WeekendAsNight) && h >= c.DayStartHour && h < c.DayEndHour {
		return time.Duration(c.DayMinutes) * time.Minute
	}
	return time.Duration(c.NightMinutes) * time.Minute
}

// autoFetchAddress は代表のアドレスです（環境変数 WCMS_MAIL_AUTO_ADDRESS・前後の空白と大小をそろえる）。
func autoFetchAddress() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("WCMS_MAIL_AUTO_ADDRESS")))
}

// autoFetchUser は、代表のアドレスでサインインしている w-cms の利用者です（いなければ空）。2人以上なら、名前の順で先の人。
// 無効にした利用者は使いません（取り込んだ記録の書き手になるため）。
func autoFetchUser(address string) string {
	if address == "" {
		return ""
	}
	entries, err := os.ReadDir(mailStoreDir())
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		u := usernameOfTokenFile(strings.TrimSuffix(name, ".json"))
		if u == "" || !strings.EqualFold(strings.TrimSpace(SignedInAddress(u)), address) {
			continue
		}
		if usr, err := auth.LookupUser(u); err != nil || usr == nil {
			continue
		}
		return u
	}
	return ""
}

// autoFetchState は自動の読み込みの様子です（GET /api/mail/auto-fetch）。
type autoFetchState struct {
	Enabled  bool   `json:"enabled"`            // 設定があり、代表のアドレスが決まっている
	Address  string `json:"address,omitempty"`  // 代表のアドレス
	User     string `json:"user,omitempty"`     // 代表のアドレスでサインインしている w-cms の利用者
	Reason   string `json:"reason,omitempty"`   // 休んでいるわけ
	LastRun  string `json:"last_run,omitempty"` // 前に始めた時刻
	NextRun  string `json:"next_run,omitempty"` // 次に始める時刻（目安）
	Interval int    `json:"interval_minutes,omitempty"`
}

var autoFetchStatus = struct {
	sync.Mutex
	last time.Time
}{}

// autoFetchNow は、いまの自動の読み込みの様子です。
func autoFetchNow(now time.Time) autoFetchState {
	c := autoFetchConfig()
	addr := autoFetchAddress()
	st := autoFetchState{Address: addr}
	switch {
	case c == nil:
		st.Reason = "設定 extensions.mail.auto_fetch がありません"
		return st
	case addr == "":
		st.Reason = "代表のアドレス（WCMS_MAIL_AUTO_ADDRESS）が決まっていません"
		return st
	}
	st.Enabled = true
	st.User = autoFetchUser(addr)
	if st.User == "" {
		st.Reason = "代表のアドレス " + addr + " でサインインしている人がいません（通信箱の「✉️ メールにサインイン」で、その口座でサインインしてください）"
	}
	iv := autoFetchInterval(*c, now)
	st.Interval = int(iv / time.Minute)
	autoFetchStatus.Lock()
	last := autoFetchStatus.last
	autoFetchStatus.Unlock()
	if !last.IsZero() {
		st.LastRun = last.Format(time.RFC3339)
		st.NextRun = last.Add(iv).Format(time.RFC3339)
	}
	return st
}

// autoFetchTick は1分ごとに呼ばれ、間隔が経っていれば読み込みを始めます（始めたら true）。
func autoFetchTick(now time.Time) bool {
	st := autoFetchNow(now)
	if !st.Enabled || st.User == "" {
		return false
	}
	autoFetchStatus.Lock()
	due := autoFetchStatus.last.IsZero() || now.Sub(autoFetchStatus.last) >= time.Duration(st.Interval)*time.Minute
	if due {
		autoFetchStatus.last = now
	}
	autoFetchStatus.Unlock()
	if !due {
		return false
	}
	if _, started := startFetch(st.User); !started {
		log.Printf("メールの自動の読み込み: %s の読み込みが動いているので、この回は飛ばします", st.User)
	}
	return true
}

// startAutoFetch は1分ごとの見張りを始めます（cms.StartBackgroundTasks から）。
func startAutoFetch() {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for now := range t.C {
			func() {
				defer func() {
					if p := recover(); p != nil {
						log.Printf("メールの自動の読み込みが落ちました: %v", p)
					}
				}()
				autoFetchTick(now)
			}()
		}
	}()
}

// MailAutoFetchAPIHandler は GET /api/mail/auto-fetch です——自動の読み込みの様子（通信箱に出す）。
func MailAutoFetchAPIHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := cms.GateJSONGet(w, r); !ok {
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "auto": autoFetchNow(time.Now())})
}
