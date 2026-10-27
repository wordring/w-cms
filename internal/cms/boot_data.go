package cms

// ─────────────────────────────────────────────────────────────────────────
// 最初の答えをページと一緒に送る（2026-10-27）
//
// 利用者:「外部回線からのアクセスがちょっと遅いように感じます」→（測ったら）ページを開くたびに、本文のあとで問い合わせを
// 3段つなげて往復していた（ログイン中の人・セッション・タグの決まり → ページの属性・権限・版・子ページ → QR）。
// 事務所の中なら1往復 1 ミリ秒で気にならないが、外からは1往復 60〜100 ミリ秒で、その3倍待つ。
// →「最初の問い合わせをページと一緒に送る直しと、版の問い合わせを2回から1回にする直しを進めてください。」
//
//   - ログインした人の殻（編集用の index.html）を返すときに、立ち上がりで画面が問い合わせる API の答えを、ページの中に
//     データ（`<script type="application/json" id="w-boot-data">`）として埋めます。画面（app.js・session.js）は最初の1回だけ
//     それを使い、あとはいままでどおり問い合わせます。
//   - 答えは、**本物の API の口を内側で呼んで**作ります（SetBootHandler で渡された口）。権限の確かめも答えの形も、外から
//     問い合わせたときと同じになるように——別に組み立てると、片方だけ直して食い違う。
//   - 埋めるのは GET で、200 で JSON（として読めるもの）を返したものだけ。失敗したものは埋めない（画面がいつもどおり問い合わせる）。
//   - ⚠ `<script type="application/json">` はデータで、実行されません（CSP の script-src に当たらない）。中の `<` は
//     `\u003c` にして、`</script>` で閉じられないようにします。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// bootPlaceholder は、編集用の殻に最初の答えを差し込む印です。
const bootPlaceholder = "<!--WCMS_BOOT-->"

var (
	bootMu      sync.RWMutex
	bootHandler http.Handler
)

// SetBootHandler は、最初の答えを作るときに内側で呼ぶ API の口を決めます（cmd/w-cms の buildHandler が、組み上げた口を渡す）。
// 渡さなければ、最初の答えは埋めません。
func SetBootHandler(h http.Handler) {
	bootMu.Lock()
	bootHandler = h
	bootMu.Unlock()
}

// bootURLs は、ページ pageID を開いたときに画面が最初に問い合わせる API です（app.js・session.js の呼び手と、住所の
// 文字列まで同じであること——画面は住所で引く）。
func bootURLs(pageID string) []string {
	return []string{
		"/api/me",
		"/api/session",
		"/api/tag-schema",
		"/api/page-meta?id=" + pageID,
		"/api/page-perms?id=" + pageID,
		"/api/children?parent_id=" + pageID,
		"/api/versions?id=" + url.QueryEscape(pageID),
	}
}

// bootRecorder は内側で呼んだ口の答えを受け取ります。
type bootRecorder struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (b *bootRecorder) Header() http.Header         { return b.header }
func (b *bootRecorder) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bootRecorder) WriteHeader(code int) {
	if b.code == 0 {
		b.code = code
	}
}

// bootDataHTML は、ページ pageID の最初の答えを埋める `<script type="application/json">` です（作れなければ空）。
// r は、殻を求めてきた要求です（同じ Cookie で内側の口を呼ぶ）。
func bootDataHTML(r *http.Request, pageID string) string {
	bootMu.RLock()
	h := bootHandler
	bootMu.RUnlock()
	if h == nil {
		return ""
	}
	data := map[string]json.RawMessage{}
	for _, u := range bootURLs(pageID) {
		target, err := url.Parse(u)
		if err != nil {
			continue
		}
		req := r.Clone(r.Context())
		req.Method = http.MethodGet
		req.URL = target
		req.RequestURI = u
		req.Body = http.NoBody
		req.ContentLength = 0
		req.Header = r.Header.Clone()
		for _, k := range []string{"If-None-Match", "If-Modified-Since", "Range", "Content-Type"} {
			req.Header.Del(k)
		}
		rec := &bootRecorder{header: http.Header{}}
		h.ServeHTTP(rec, req)
		if rec.code == 0 {
			rec.code = http.StatusOK
		}
		if rec.code != http.StatusOK || !json.Valid(rec.body.Bytes()) {
			continue
		}
		data[u] = json.RawMessage(append([]byte(nil), rec.body.Bytes()...))
	}
	if len(data) == 0 {
		return ""
	}
	b, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	// json.Marshal は < > & を \u003c などにするが、念のため `<` は必ず消す（`</script>` で閉じられないように）。
	// ⚠ 2026-10-27 の見直しまで、ここは `<` を `<` に替える（何もしない）形になっていた（書いたときに `\u003c` の逆スラッシュが
	// 落ちた）。json.Marshal が文字列の中の `<` を替えていたので、`</script>` で閉じられることはなかった。
	s := strings.ReplaceAll(string(b), "<", `\u003c`) // ⚠ `\u003c`（6文字）——書き換えの道具が `<` に戻すことがある。直したら od -c で確かめる
	return `<script type="application/json" id="w-boot-data">` + s + `</script>`
}
