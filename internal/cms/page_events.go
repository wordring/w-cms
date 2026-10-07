package cms

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"w-cms/internal/cms/page"
)

// ── ページの更新の知らせ（2026-10-07） ─────────────────────────────────────
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
// ⚠ **どのブロックが変わったかは送りません**。画面が /api/load を読み直し、前に読んだ
//    ものと比べて決めます——鏡の中身は見る人ごとに描かれ、ほかのページの変化でも変わる
//    ので、書き手の差分だけでは「その人の画面で何が変わったか」になりません。
//
// 知らせるのは**本文を書く口**——エディタの保存（全文・ブロック／writeBody）・機械の
// 書き換え（RewriteBody・SetPageH1／rewritePageBody）・版を戻す（RevertToVersion）——の、
// 書き込みの直後です。添付の上書き（ローカル編集・WebDAV）は本文を変えないので知らせません。
//
// 編集ロック（editlock）の SSE とは別の流れです——あちらは編集権を持つ人・待つ人のための
// もので write 権限が要り、こちらは読める人なら誰でも購読できます。

// pageEvent は購読者へ送る1件です。
type pageEvent struct {
	Type      string `json:"type"` // hello（つないだ直後）・updated（本文が書き換わった）
	By        string `json:"by,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// pageWatchers はページごとの購読者です（タブ1つにつき1本）。
type pageWatchers struct {
	mu   sync.Mutex
	subs map[string]map[chan pageEvent]struct{}
}

var watchers = &pageWatchers{subs: map[string]map[chan pageEvent]struct{}{}}

func (p *pageWatchers) subscribe(id string) chan pageEvent {
	ch := make(chan pageEvent, 4)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.subs[id] == nil {
		p.subs[id] = map[chan pageEvent]struct{}{}
	}
	p.subs[id][ch] = struct{}{}
	return ch
}

func (p *pageWatchers) unsubscribe(id string, ch chan pageEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.subs[id], ch)
	if len(p.subs[id]) == 0 {
		delete(p.subs, id)
	}
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
	if norm, ok := page.NormalizeID(id); ok {
		id = norm
	}
	watchers.publish(id, pageEvent{Type: "updated", By: by, UpdatedAt: updatedAt})
}

// pageEventsKeepalive は、つないだままの線を中継の機器に切られないための間隔です。
const pageEventsKeepalive = 25 * time.Second

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
	ch := watchers.subscribe(id)
	defer watchers.unsubscribe(id, ch)

	send := func(ev pageEvent) {
		b, _ := json.Marshal(ev)
		w.Write([]byte("data: " + string(b) + "\n\n"))
		flusher.Flush()
	}
	hello := pageEvent{Type: "hello"}
	if meta, ok := page.ReadSidecar(id); ok {
		hello.UpdatedAt = meta.UpdatedAt
	}
	send(hello)

	keepalive := time.NewTicker(pageEventsKeepalive)
	defer keepalive.Stop()
	for {
		select {
		case ev := <-ch:
			send(ev)
		case <-keepalive.C:
			w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
