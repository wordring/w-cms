package mail

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"w-cms/internal/database"
)

// TestAutoFetchInterval は自動の読み込みの間隔を固定します（2026-10-21 夜・【考察】通信箱の改善 §7.1）。
//
// > 2026-10-21 利用者:「8時から20時までが日中。土日は夜間と同じ」
func TestAutoFetchInterval(t *testing.T) {
	c := autoFetchSettings{DayStartHour: 8, DayEndHour: 20, DayMinutes: 5, NightMinutes: 60, WeekendAsNight: true}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct {
		when string
		want time.Duration
	}{
		{"2026-10-21 08:00", 5 * time.Minute},  // 水曜の8時ちょうどは日中
		{"2026-10-21 19:59", 5 * time.Minute},  // 20時の前まで日中
		{"2026-10-21 20:00", 60 * time.Minute}, // 20時から夜間
		{"2026-10-21 07:59", 60 * time.Minute},
		{"2026-10-24 10:00", 60 * time.Minute}, // 土曜の昼は夜間と同じ
		{"2026-10-25 10:00", 60 * time.Minute}, // 日曜も
	} {
		if got := autoFetchInterval(c, at(tc.when)); got != tc.want {
			t.Errorf("%s の間隔が %v です（%v を期待）", tc.when, got, tc.want)
		}
	}
}

// TestAutoFetchSettingsAndReasons は設定の検査と、休むときのわけを固定します。
func TestAutoFetchSettingsAndReasons(t *testing.T) {
	if _, err := parseMailSettings(json.RawMessage(`{"auto_fetch":{"day_start_hour":20,"day_end_hour":8,"day_minutes":5,"night_minutes":60}}`)); err == nil {
		t.Errorf("⚠ 日中の始まりが終わりより後の設定を通しています")
	}
	if _, err := parseMailSettings(json.RawMessage(`{"auto_fetch":{"day_start_hour":8,"day_end_hour":20,"day_minutes":0,"night_minutes":60}}`)); err == nil {
		t.Errorf("⚠ 間隔0分の設定を通しています")
	}
	if _, err := parseMailSettings(json.RawMessage(`{"auto_fech":{}}`)); err == nil {
		t.Errorf("⚠ 打ち間違えたキーを黙って通しています")
	}
	apply, err := parseMailSettings(json.RawMessage(`{"auto_fetch":{"day_start_hour":8,"day_end_hour":20,"day_minutes":5,"night_minutes":60,"weekend_as_night":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	apply()
	t.Cleanup(func() { autoFetchMu.Lock(); autoFetchCfg = nil; autoFetchMu.Unlock() })

	t.Setenv("WCMS_MAIL_AUTO_ADDRESS", "")
	if st := autoFetchNow(time.Now()); st.Enabled || !strings.Contains(st.Reason, "WCMS_MAIL_AUTO_ADDRESS") {
		t.Errorf("⚠ 代表のアドレスが無いのに動くか、わけが違います: %+v", st)
	}
	t.Setenv("WCMS_MAIL_AUTO_ADDRESS", " Daihyo@Example.COM ")
	database.SetDataDir(t.TempDir()) // サインインの保管は空
	t.Cleanup(func() { database.SetDataDir("") })
	st := autoFetchNow(time.Now())
	if !st.Enabled || st.Address != "daihyo@example.com" || st.User != "" || !strings.Contains(st.Reason, "サインインしている人がいません") {
		t.Errorf("⚠ サインインしている人がいないときの様子が違います: %+v", st)
	}
	if autoFetchTick(time.Now()) {
		t.Errorf("⚠ サインインしている人がいないのに読み込みを始めました")
	}
}
