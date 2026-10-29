package cms

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// ── ページの更新の通知（2026-10-07） ─────────────────────────────────────
//
// 利用者:「誰かが書き込むと、同じページを見ているほかの人に再読み込み通知が送られて、
// 編集されたブロックだけ再読み込みできますか？」
//
// 閲覧しているタブは GET /api/page-events?id= を購読し、本文が書き換わるたびに
// {type:"updated", by, updated_at} を受け取ります。つないだ直後には
// {type:"hello", updated_at} を1つ送ります——スマホのスリープなどで切れていたあいだの
// 書き込みは、つなぎ直したときに更新日時を比べて画面が気づきます（EventSource は自分で
// つなぎ直す）。
//
// ⚠ **どのブロックが変わったかは送りません**。閲覧の画面は /api/load?view=1 を読み直し、前に読んだ
//    ものと比べて決めます（書ける形の画面は /api/sync で、持っていない・古いブロックだけを受け取ります——
//    2026-10-12）。鏡の中身は見る人ごとに描かれ、ほかのページの変化でも変わる
//    ので、書き手の差分だけでは「その人の画面で何が変わったか」になりません。
//
// 知らせるのは**本文を書く口**——画面の保存（/api/sync・notifyPageUpdatedFrom で書いたタブを添える——
// 2026-10-12）・道具の保存（全文 /api/save・ブロック /api/save-block）・機械の
// 書き換え（RewriteBody・SetPageH1／rewritePageBody）・版を戻す（RevertToVersion）——の、
// 書き込みの直後です。添付の上書き（ローカル編集）は本文を変えないので知らせません。
// 取り込みの UpdatePage（ext/comm）も知らせません（作った直後のページで、まだ誰も開いていないため）。
//
// 編集ロック（editlock）の SSE とは別の流れです——あちらは編集権を持つ人・待つ人のための
// もので write 権限が要り、こちらは読める人なら誰でも購読できます。2026-10-12 から画面は
// 編集ロックを取らないので、あちらの SSE（/api/lock-events）は、いまは誰も張っていません
// （道具は /api/lock で握るだけです）。
//
// ── ほかの人のキャレットのあるブロック（2026-10-12 昼） ──
//
// 利用者:「他人が同じブロックにキャレットを置いている、あるいはすでに文字を打ち込んでいるような場合は、早期に警告して欲しいです」
// →「置いただけで背景色を黄色系の彩度の低い色にしては？説明が多すぎるのでシンプルに」「表ごと」。
//
// 書ける形のタブは、キャレットのある上の段のブロックが変わるたびに POST /api/presence で知らせ、ここは購読（タブ1つ）ごとに
// 覚えて、同じページを開いているタブへ {type:"presence", presence:[{tab, user, block}]} を配ります（つないだ直後の hello にも
// 入れる）。**心拍は要りません**——タブを閉じれば購読が切れ、そのタブの印も消えて配り直します。

// pageEvent は購読者へ送る1件です。
type pageEvent struct {
	Type      string `json:"type"` // hello（つないだ直後）・updated（本文が書き換わった）・presence（キャレットの場所——2026-10-12 昼）・views（鏡の中身だけが変わった——2026-10-28）
	By        string `json:"by,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	// Tab は書いたタブ（2026-10-12）——書ける形のタブは自分の保存の知らせを受けても読み直さない。
	// 業務のボタン・道具の書き込みは空（押したタブも読み直す——ボタンが書いた中身を受け取るため）。
	Tab string `json:"tab,omitempty"`
	// Presence はキャレットのあるブロック（type が presence と hello のとき・2026-10-12 昼）。
	Presence []presenceItem `json:"presence,omitempty"`
}

// presenceItem は、あるタブのキャレットのある上の段のブロックです。
type presenceItem struct {
	Tab   string `json:"tab"`
	User  string `json:"user"`
	Block string `json:"block"`
}

// pageWatcher は購読している1本（タブ1つ）です。誰が開いているかは、ページを消すときの確かめに使う
// （2026-10-12 利用者が選んだ形——消す前の確かめのダイアログに「○○さんが開いています」）。
type pageWatcher struct {
	user  string
	tab   string
	block string // キャレットのある上の段のブロック（空はどこにも置いていない——2026-10-12 昼）
}

// pageWatchers はページごとの購読者です（タブ1つにつき1本）。
type pageWatchers struct {
	mu   sync.Mutex
	subs map[string]map[chan pageEvent]pageWatcher
}

var watchers = &pageWatchers{subs: map[string]map[chan pageEvent]pageWatcher{}}

func (p *pageWatchers) subscribe(id string, who pageWatcher) chan pageEvent {
	ch := make(chan pageEvent, 4)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.subs[id] == nil {
		p.subs[id] = map[chan pageEvent]pageWatcher{}
	}
	p.subs[id][ch] = who
	return ch
}

// unsubscribe は購読を外します。外したタブがブロックにキャレットを置いていたら true（配り直す）。
func (p *pageWatchers) unsubscribe(id string, ch chan pageEvent) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	had := p.subs[id][ch].block != ""
	delete(p.subs[id], ch)
	if len(p.subs[id]) == 0 {
		delete(p.subs, id)
	}
	return had
}

// setBlock は、そのタブ（user の購読に限る）のキャレットのあるブロックを覚えます。変わったら true。
// 購読がまだ無い（つなぐ前）なら覚えない——画面はつないだときの hello で送り直します。
func (p *pageWatchers) setBlock(id, tab, user, block string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	changed := false
	for ch, w := range p.subs[id] {
		if w.tab != tab || w.user != user || w.block == block {
			continue
		}
		w.block = block
		p.subs[id][ch] = w
		changed = true
	}
	return changed
}

// presence は、そのページでキャレットをブロックに置いているタブです（並びはタブの印の順）。
func (p *pageWatchers) presence(id string) []presenceItem {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []presenceItem
	for _, w := range p.subs[id] {
		if w.block != "" {
			out = append(out, presenceItem{Tab: w.tab, User: w.user, Block: w.block})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tab < out[j].Tab })
	return out
}

// publishPresence は、そのページを開いているタブへキャレットの場所を配り直します。
func publishPresence(id string) {
	watchers.publish(id, pageEvent{Type: "presence", Presence: watchers.presence(id)})
}

// who はそのページを開いているタブを返します（exceptTab のタブを除く）。
func (p *pageWatchers) who(id, exceptTab string) []pageWatcher {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []pageWatcher
	for _, w := range p.subs[id] {
		if exceptTab != "" && w.tab == exceptTab {
			continue
		}
		out = append(out, w)
	}
	return out
}

// publish は購読者へ配ります。**詰まった購読者は待ちません**——書き込みの口を止めない
// ためです。取りこぼしても困らないのは、まだ読まれていない知らせが残っているからです
// （画面はそれを受けてから本文を読み直すので、いちばん新しい本文を読みます）。
func (p *pageWatchers) publish(id string, ev pageEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for ch := range p.subs[id] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// notifyPageUpdated は、そのページを開いているタブへ「本文が書き換わった」を知らせます。
// 購読の鍵と揃えるため、id はゼロ詰め6桁へ畳んでから配ります。
func notifyPageUpdated(id, by, updatedAt string) {
	notifyPageUpdatedFrom(id, by, "", updatedAt)
}

// NotifyViewsChanged は、そのページの鏡（計算ビュー）の中身だけが変わったことを、開いているタブへ知らせます（2026-10-28）。
// 本文は変わらないので、画面は鏡の中身だけをその場で描き直します（スクロールや開いている欄は動かさない）。
// 拡張が、そのページの本文を書かずに、鏡が描くもとのデータを変えたときに呼びます。
//
// > 2026-10-28 利用者:「再描画でスクロールしたり、開いていたパネルが閉じたりしないなら、描き直してください。」
func NotifyViewsChanged(id string) {
	if norm, ok := page.NormalizeID(id); ok {
		id = norm
	}
	watchers.publish(id, pageEvent{Type: "views"})
}

// notifyPageUpdatedFrom は書いたタブを添えて知らせます（/api/sync——2026-10-12）。
func notifyPageUpdatedFrom(id, by, tab, updatedAt string) {
	if norm, ok := page.NormalizeID(id); ok {
		id = norm
	}
	watchers.publish(id, pageEvent{Type: "updated", By: by, UpdatedAt: updatedAt, Tab: tab})
}

// PageWatchersAPIHandler は GET /api/page-watchers?id=&tab= です——そのページを開いている人の名前（重ならないように）を
// 返します（tab のタブ＝自分は除く）。ページを消す前の確かめに使う（2026-10-12）。
func PageWatchersAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, ok := page.NormalizeID(r.URL.Query().Get("id"))
	if !ok {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	if !page.RequirePageRead(w, r, id) {
		return
	}
	seen := map[string]bool{}
	names := []string{}
	for _, wt := range watchers.who(id, r.URL.Query().Get("tab")) {
		if wt.user == "" || seen[wt.user] {
			continue
		}
		seen[wt.user] = true
		names = append(names, wt.user)
	}
	sort.Strings(names)
	WriteJSON(w, map[string]any{"success": true, "users": names})
}

// presenceBlockRe はブロックの ID の形です（上の段の data-id——英数字。空は「どこにも置いていない」）。
var presenceBlockRe = regexp.MustCompile(`^[0-9A-Za-z_-]{1,32}$`)

// PresenceAPIHandler は POST /api/presence {id, tab, block} です——書ける形のタブが、キャレットのある上の段のブロックを
// 知らせます（2026-10-12 昼）。そのタブの購読（/api/page-events）に覚え、変わったら同じページのタブへ配ります。
// 名乗れるのは自分の購読だけ（別の人のタブの印は変えられない）。対象ページの read 権限を要求します。
func PresenceAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		ID    string `json:"id"`
		Tab   string `json:"tab"`
		Block string `json:"block"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	id, ok := page.NormalizeID(req.ID)
	if !ok {
		JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	if req.Tab == "" || (req.Block != "" && !presenceBlockRe.MatchString(req.Block)) {
		JSONFail(w, http.StatusBadRequest, "タブかブロックの印が不正です")
		return
	}
	if !page.RequirePageRead(w, r, id) {
		return
	}
	if watchers.setBlock(id, req.Tab, user.Username, req.Block) {
		publishPresence(id)
	}
	WriteJSON(w, map[string]any{"success": true})
}

// pageEventsKeepalive は、つないだままの線を中継の機器に切られないための間隔です。
const pageEventsKeepalive = 25 * time.Second

// pageEventsWriteTimeout は、知らせ1つを書き終えるまでの期限です（読まない相手で止まり続けない）。
const pageEventsWriteTimeout = 30 * time.Second

// atoiOr0 はページ番号を数にします（読めなければ 0——0 のページは無いので、読めないと判定される）。
// PageEventsAPIHandler は、ページの更新を SSE（text/event-stream）で知らせます。
// GET /api/page-events?id= 。対象ページの read 権限を要求します。
func PageEventsAPIHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := page.NormalizeID(r.URL.Query().Get("id"))
	if !ok {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	if !page.RequirePageRead(w, r, id) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "ストリーミング非対応", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // リバースプロキシのバッファリング抑止

	// 先に購読してから hello を送る——逆だと、そのあいだの書き込みを取りこぼす。
	ch := watchers.subscribe(id, pageWatcher{user: auth.UsernameOf(r), tab: r.URL.Query().Get("tab")})
	defer func() {
		if watchers.unsubscribe(id, ch) {
			publishPresence(id) // 閉じたタブの印を消す
		}
	}()

	// 書けない相手で止まり続けないよう、書くたびに期限を置く。読めなくなった人（権限を替えた・読めない所へ動かした）には
	// 流し続けない——知らせには、だれがいつ書いたか・ほかの人の名前が入るため（2026-10-22 の見直し）。
	rc := http.NewResponseController(w)
	// 利用者は毎回いまの状態で引き直す（2026-10-26。受けたときの写しのままだと、ログアウト・無効・グループから外す・
	// 管理者から外す、のあとも流し続けた）。
	idInt := AtoiSafe(id)
	stillReadable := func() bool {
		u := auth.FreshUser(r)
		return u != nil && page.CanView(u, idInt)
	}
	write := func(s string) bool {
		rc.SetWriteDeadline(time.Now().Add(pageEventsWriteTimeout))
		if _, err := w.Write([]byte(s)); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	send := func(ev pageEvent) bool {
		b, _ := json.Marshal(ev)
		return write("data: " + string(b) + "\n\n")
	}
	hello := pageEvent{Type: "hello", Presence: watchers.presence(id)}
	if meta, ok := page.ReadSidecar(id); ok {
		hello.UpdatedAt = meta.UpdatedAt
	}
	send(hello)

	keepalive := time.NewTicker(pageEventsKeepalive)
	defer keepalive.Stop()
	for {
		select {
		case ev := <-ch:
			if !stillReadable() || !send(ev) {
				return
			}
		case <-keepalive.C:
			if !stillReadable() || !write(": ping\n\n") {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
