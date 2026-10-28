package comm

// ─────────────────────────────────────────────────────────────────────────
// 出ていない着信の履歴（2026-10-28）
//
// 利用者:「今日、スマホに転送されてから電話を受けたと思うのですが、サーバーに記録が残っていませんでした。ND6側では、転送されると
// 単に呼び出しが切れたように見えると思うのですが、そういう場合でも記録を残すことは出来ますか」
// 「電話が鳴るだけでページを作るということではなく、なった記録だけして、編集者がページにするか選択すればよいと思うのです」
//
//   - 電話の道具（cmd/w-cms-phone）は、出る前に鳴りやんだ着信を {event:"missed", number, at（鳴り始め）, ended（鳴りやみ）} で
//     知らせます（machine_call.go）。ND6 には、携帯へ転送されたのか、相手が切ったのかは見分けられません。
//   - ここでは**ページを作りません**。履歴（data/phone/missed.json）に1行残すだけです。
//   - 通信箱の未処理の表に「☎ 出ていない着信」の行として時刻の順に混ぜ、人が「記録にする」を押したときに初めて、電話の記録のページを
//     作ります（出た着信と同じ形・`着信の結果：出ていない`・`呼び出し`）。「✕ 外す」は一覧から外すだけです（履歴には残る）。
//   - 履歴は 90 日・1000 件まで残します。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 出ていない着信の記録に書くタグです。
const (
	CallResultTag    = "着信の結果"
	CallResultMissed = "出ていない"
	CallRingTag      = "呼び出し" // 鳴っていた長さ（例「16秒」）
)

const (
	missedKeepDays = 90             // 履歴を残す日数
	missedKeepMax  = 1000           // 履歴を残す件数
	missedShowDays = 14             // 一覧に並べる日数
	missedRingMax  = 10 * time.Minute // 呼び出しの長さの上限（時刻が狂っていても変な数を書かない）
)

// MissedCall は出ていない着信1つです。
type MissedCall struct {
	ID        string `json:"id"`
	Number    string `json:"number"`
	At        string `json:"at"`       // 鳴り始め（RFC 3339・土地の時刻）
	RingSec   int    `json:"ring_sec"` // 鳴っていた秒数（分からなければ 0）
	PageID    string `json:"page_id,omitempty"`
	MadeBy    string `json:"made_by,omitempty"`
	Dismissed bool   `json:"dismissed,omitempty"`
	By        string `json:"by,omitempty"` // 外した人
}

var missedMu sync.Mutex

func missedPath() string { return database.DataPath("phone", "missed.json") }

func readMissedLocked() []MissedCall {
	b, err := os.ReadFile(missedPath())
	if err != nil {
		return nil
	}
	var out []MissedCall
	json.Unmarshal(b, &out)
	return out
}

func writeMissedLocked(list []MissedCall) error {
	cut := time.Now().AddDate(0, 0, -missedKeepDays)
	var kept []MissedCall
	for _, m := range list {
		if t, err := time.Parse(time.RFC3339, m.At); err == nil && t.Before(cut) {
			continue
		}
		kept = append(kept, m)
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].At > kept[j].At })
	if len(kept) > missedKeepMax {
		kept = kept[:missedKeepMax]
	}
	b, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(missedPath()), 0700); err != nil {
		return err
	}
	return page.WriteFileAtomic(missedPath(), b, 0600)
}

// addMissedCall は出ていない着信を履歴に足します。同じ番号・同じ鳴り始めは1つにします（道具の送り直し）。
func addMissedCall(number string, at, ended time.Time) (MissedCall, error) {
	ring := ended.Sub(at)
	if ring < 0 || ring > missedRingMax {
		ring = 0
	}
	m := MissedCall{
		Number:  strings.TrimSpace(number),
		At:      at.In(time.Local).Format(time.RFC3339),
		RingSec: int(ring.Round(time.Second) / time.Second),
	}
	m.ID = at.In(time.Local).Format("20060102T150405") + "-" + digitsOf(m.Number)
	missedMu.Lock()
	defer missedMu.Unlock()
	list := readMissedLocked()
	for _, o := range list {
		if o.ID == m.ID {
			return o, nil
		}
	}
	return m, writeMissedLocked(append(list, m))
}

// RecentMissedCalls は一覧に並べる出ていない着信です（まだ記録にしていない・外していない・14 日以内・新しい順）。
func RecentMissedCalls() []MissedCall {
	missedMu.Lock()
	list := readMissedLocked()
	missedMu.Unlock()
	cut := time.Now().AddDate(0, 0, -missedShowDays)
	var out []MissedCall
	for _, m := range list {
		t, err := time.Parse(time.RFC3339, m.At)
		if err != nil || t.Before(cut) || m.PageID != "" || m.Dismissed {
			continue
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out
}

var errMissedNotFound = errors.New("その着信が履歴に見つかりません")

// updateMissed は id の着信を書き換えます（fn が false を返したら書かない）。
func updateMissed(id string, fn func(*MissedCall) error) (MissedCall, error) {
	missedMu.Lock()
	defer missedMu.Unlock()
	list := readMissedLocked()
	for i := range list {
		if list[i].ID != id {
			continue
		}
		if err := fn(&list[i]); err != nil {
			return list[i], err
		}
		return list[i], writeMissedLocked(list)
	}
	return MissedCall{}, errMissedNotFound
}

// createCallRecord は電話の記録のページを作ります（出た着信と、出ていない着信を「記録にする」とき）。extra が本文に足すタグを書きます。
func createCallRecord(user *auth.User, number string, at time.Time, missed bool, extra func(d *cms.PageDraft)) (string, string, error) {
	boxID, ok := MailBoxPageID()
	if !ok {
		return "", "", ErrNoMailBox
	}
	tmpl, err := cms.PageTemplateBody(MemoTemplate)
	if err != nil {
		return "", "", fmt.Errorf("記録を作れません: %w", err)
	}
	number = strings.TrimSpace(number)
	counterpart, who := "", ""
	if id, title, found := phoneOwner(user, number); found {
		counterpart, who = page.FormatID(id), title
	}
	title := callTitle(number, who, at)
	if missed {
		title = strings.Replace(title, ChannelPhone, ChannelPhone+"（出ていない）", 1)
	}
	body := memoBodyHTML(tmpl, ChannelPhone, title, DirectionIn, digitsOf(number), counterpart, at)
	if extra != nil {
		d := cms.EditBody(body)
		extra(d)
		body = d.HTML()
	}
	pageID, err := CreateRecordPage(boxID, user.Username, at, body)
	if err != nil {
		return "", "", fmt.Errorf("記録を作れません: %w", err)
	}
	return pageID, title, nil
}

// MissedCallMakeAPIHandler は POST /api/phone/missed/make {id} です——出ていない着信から電話の記録のページを作り、開く先を返します。
func MissedCallMakeAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	boxID, ok := MailBoxPageID()
	if !ok {
		cms.JSONFail(w, http.StatusConflict, ErrNoMailBox.Error())
		return
	}
	if !page.RequirePageWrite(w, r, boxID) {
		return
	}
	var made, title string
	m, err := updateMissed(strings.TrimSpace(req.ID), func(m *MissedCall) error {
		if m.PageID != "" {
			made = m.PageID // もう記録にしてある（2人が同時に押した）——その記録を開く
			return errors.New("made")
		}
		at, err := time.Parse(time.RFC3339, m.At)
		if err != nil {
			at = time.Now()
		}
		id, t, err := createCallRecord(user, m.Number, at, true, func(d *cms.PageDraft) {
			d.SetTag(CallResultTag, CallResultMissed)
			if m.RingSec > 0 {
				d.SetTag(CallRingTag, strconv.Itoa(m.RingSec)+"秒")
			}
		})
		if err != nil {
			return err
		}
		m.PageID, m.MadeBy, made, title = id, user.Username, id, t
		return nil
	})
	if title != "" {
		cms.NotifyViewsChanged(boxID) // 開いている通信箱から、この行を消す
	}
	if made != "" {
		if title != "" {
			auth.Audit(user.Username, "intake.call.missed", made+" ("+digitsOf(m.Number)+")")
		}
		cms.WriteJSON(w, map[string]any{"success": true, "page_id": made, "url": "/" + made, "title": title})
		return
	}
	if errors.Is(err, errMissedNotFound) {
		cms.JSONFail(w, http.StatusNotFound, err.Error())
		return
	}
	cms.JSONFail(w, http.StatusInternalServerError, err.Error())
}

// MissedCallDismissAPIHandler は POST /api/phone/missed/dismiss {id} です——一覧から外します（履歴には残る）。
func MissedCallDismissAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	boxID, ok := MailBoxPageID()
	if !ok {
		cms.JSONFail(w, http.StatusConflict, ErrNoMailBox.Error())
		return
	}
	if !page.RequirePageWrite(w, r, boxID) {
		return
	}
	if _, err := updateMissed(strings.TrimSpace(req.ID), func(m *MissedCall) error {
		m.Dismissed, m.By = true, user.Username
		return nil
	}); err == nil {
		cms.NotifyViewsChanged(boxID) // 開いているほかの通信箱からも、この行を消す
	} else {
		code := http.StatusInternalServerError
		if errors.Is(err, errMissedNotFound) {
			code = http.StatusNotFound
		}
		cms.JSONFail(w, code, err.Error())
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true})
}

// missedRowHTML は、出ていない着信1つを通信箱の未処理の表の1行にします（ボタンは「記録」「不要」——2026-10-28 利用者:「記録にすると外すボタンを記録と不要に変えてください」）（2026-10-28——はじめは別の表にしていたが、
// 利用者:「出てましたが、違う表なので気が付きませんでした」）。選ぶ升目は付けない（「済」「不要」のまとめての対象ではない）。
func missedRowHTML(user *auth.User, m MissedCall) string {
	who := ""
	if _, title, found := phoneOwner(user, m.Number); found {
		who = title
	} else if mark, ok := callerMarks[m.Number]; ok {
		who = mark
	} else if d := digitsOf(m.Number); d != "" {
		who = d
	} else {
		who = "番号なし"
	}
	// 名前は短く（未処理のほかの行と同じ）、電話番号は切らない（2026-10-28 利用者:「電話番号が省略されていますが、もう少し文字数が在っても良いと思います」——11桁まで）。
	shown := ellipsize(who, unhandledFromMax)
	if who == digitsOf(m.Number) {
		shown = ellipsize(who, 15)
	}
	ring := ""
	if m.RingSec > 0 {
		ring = "呼び出し " + strconv.Itoa(m.RingSec) + "秒"
	}
	id := stdhtml.EscapeString(m.ID)
	return `<tr class="missed-row" data-missed-id="` + id + `">` +
		`<td class="vocab-chrome unhandled-pick"></td>` +
		`<td class="unhandled-channel">` + channelWithDirection(ChannelPhone, DirectionIn) + `</td>` +
		`<td class="unhandled-when">` + stdhtml.EscapeString(shortTime(m.At)) + `</td>` +
		`<td class="unhandled-from" title="` + stdhtml.EscapeString(digitsOf(m.Number)) + `">` + stdhtml.EscapeString(shown) + `</td>` +
		`<td class="unhandled-subject"><span class="unhandled-missed" title="出る前に鳴りやんだ着信です（携帯へ転送して出た電話も、こう見えます）。要るものは「記録」で電話の記録のページにします">☎ 出ていない着信</span> ` +
		stdhtml.EscapeString(ring) + `</td>` +
		`<td class="vocab-chrome unhandled-act">` +
		`<button type="button" class="chip-btn missed-make" data-missed-id="` + id + `" title="電話の記録のページを作って開きます">記録</button>` +
		`<button type="button" class="chip-btn unhandled-skip missed-dismiss" data-missed-id="` + id + `" title="記録にせず、一覧から外します（履歴には残ります）">不要</button></td>` +
		`</tr>`
}
