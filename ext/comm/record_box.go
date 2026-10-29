package comm

// ─────────────────────────────────────────────────────────────────────────
// 記録の箱（2026-10-20・docs/考察/【考察】画面の JS のコアと拡張.md §4.5）
//
// 2026-10-20 利用者:「拡張の機能は、出来る限りタグとして実装してほしいです」（本文に置く印〔鏡の印〕の意味）
// 「この案で進めてください。既にある通信記録のページにも、保存の口で印を書き足してください。」
//
// 通信記録のページの本文に置く印 `<section data-mirror="記録の箱"></section>` の中身です。サーバーが描きます。
//   - 作るページ（記録から作るページ・record_make.go——選べる種類は拡張が登録する。下書きには出さない）
//   - ほかの拡張が足す節（RegisterRecordSection——メールの ✉️ 返信・東邦の整理と 🤖 解析）
//   - 🧵 やりとりの前後（ThreadOf）・📨 この記録への返信（RepliesTo）
//
// それまでは通信の JS が `チャネル` のタグを見て、自分で箱を組んでいました（ext/comm/assets/comm.js の refreshMailChrome）。
// 押したときの働き（作る・返信を開く など）は、いまも拡張の JS が document へ委ねて配線します。
// ⚠ 描くのは文字列です（RegisterView）——値は必ずエスケープしてから連結すること（保存型XSSの前科）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"sort"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// RecordBoxType は「記録の箱」の形式名です（本文の印は表示名 `記録の箱`）。
const RecordBoxType = "record-box"

// RecordBoxTitle は記録の箱の表示名（本文の印 data-mirror の値）です。
const RecordBoxTitle = "記録の箱"

// RecordSection は、ほかの拡張が記録の箱へ足す節です。Order の小さい順に並べます（作るページは 10・前後は 80・返信の一覧は 90）。
type RecordSection struct {
	Order  int
	Render func(user *auth.User, pageIDInt int, draft bool) string // 空なら何も足さない
}

var recordSections []RecordSection

// RegisterRecordSection は記録の箱へ節を足します（拡張の `init()` から呼ぶ）。
func RegisterRecordSection(s RecordSection) {
	recordSections = append(recordSections, s)
	sort.SliceStable(recordSections, func(i, j int) bool { return recordSections[i].Order < recordSections[j].Order })
}

func init() {
	cms.RegisterVocab(cms.VocabDef{
		Type:        RecordBoxType,
		DisplayName: RecordBoxTitle,
		Category:    "ビュー",
		Icon:        "📨",
		Element:     "section",
		View:        true,
	})
	cms.RegisterView(RecordBoxType, recordBoxHTML)
	RegisterRecordSection(RecordSection{Order: 10, Render: recordMakeHTML})
	RegisterRecordSection(RecordSection{Order: 80, Render: threadHTML})
	RegisterRecordSection(RecordSection{Order: 90, Render: repliesHTML})
}

// recordBoxHTML は記録の箱を描きます。通信記録（`チャネル` のタグがある）でなければ何も出しません。
func recordBoxHTML(user *auth.User, pageIDInt int) string {
	if strings.TrimSpace(cms.PageTagValue(database.DB, pageIDInt, ChannelTag)) == "" {
		return ""
	}
	draft := strings.TrimSpace(cms.PageTagValue(database.DB, pageIDInt, DraftTag)) != ""
	var b strings.Builder
	for _, s := range recordSections {
		b.WriteString(s.Render(user, pageIDInt, draft))
	}
	if b.Len() == 0 {
		return ""
	}
	return `<div class="vocab-chrome mail-chrome" contenteditable="false" data-page="` + page.FormatID(pageIDInt) + `">` + b.String() + `</div>`
}

// recordMakeHTML は「作るページ [選ぶ] [作成]」と、もう作ったページの印です（下書きには出さない）。
// 選べる種類は書ける人にだけ（押しても 403 になるだけ）——印だけは見せる。
func recordMakeHTML(user *auth.User, pageIDInt int, draft bool) string {
	if draft {
		return ""
	}
	kinds, made := RecordMakersOf(user, pageIDInt)
	var opts, marks strings.Builder
	n := len(kinds)
	for _, m := range kinds {
		opts.WriteString(`<option value="` + stdhtml.EscapeString(m.Name) + `" title="` + stdhtml.EscapeString(m.Hint) + `">` + stdhtml.EscapeString(m.Name) + `</option>`)
	}
	for _, p := range made {
		marks.WriteString(` ` + MadeMarkHTML(p.PageID, p.Title, p.Kind))
	}
	if n == 0 && marks.Len() == 0 {
		return ""
	}
	out := `<p class="record-make">`
	if n > 0 {
		out += `<label>作るページ <select class="record-make-kind">` + opts.String() + `</select></label> ` +
			`<button type="button" class="record-make-go">作成</button>`
	}
	return out + `<span class="record-make-marks">` + marks.String() + `</span></p>`
}

// threadHTML は「🧵 やりとりの前後」です（前後が無ければ出さない——1通で終わる記録のほうが多い）。
func threadHTML(user *auth.User, pageIDInt int, draft bool) string {
	prev, next, err := ThreadOf(user, pageIDInt)
	if err != nil || (prev == nil && len(next) == 0) {
		return ""
	}
	item := func(label string, r ThreadRef) string {
		dir := ""
		switch r.Direction {
		case DirectionOut:
			dir = "（送信）"
		case "受信":
			dir = "（受信）"
		}
		s := `<li><span class="mail-thread-dir">` + stdhtml.EscapeString(label+dir) + `</span><a href="/` + stdhtml.EscapeString(r.PageID) + `" title="` +
			stdhtml.EscapeString(firstNonEmpty(r.Title, r.PageID)) + `">` + stdhtml.EscapeString(firstNonEmpty(r.Title, r.PageID)) + `</a>`
		if r.When != "" {
			w := r.When
			if len([]rune(w)) > 16 {
				w = string([]rune(w)[:16])
			}
			s += `<span class="mail-thread-when"> ` + stdhtml.EscapeString(strings.Replace(w, "T", " ", 1)) + `</span>`
		}
		return s + `</li>`
	}
	var b strings.Builder
	b.WriteString(`<div class="mail-thread"><p class="mail-thread-head">🧵 やりとりの前後</p><ul>`)
	if prev != nil {
		b.WriteString(item("↑ 返信元", *prev))
	}
	// 次は複数ありえます（同じメールに2人が別々に返信した、など）。
	for _, n := range next {
		b.WriteString(item("↓ この記録への返り", n))
	}
	b.WriteString(`</ul></div>`)
	return b.String()
}

// repliesHTML は「📨 この記録への返信」です（こちらから送った返信だけ・本体は通信箱にある）。
func repliesHTML(user *auth.User, pageIDInt int, draft bool) string {
	replies, err := RepliesTo(user, page.FormatID(pageIDInt))
	if err != nil || len(replies) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="mail-replies"><p class="mail-replies-head">📨 この記録への返信（本体は送信箱にあります）</p><ul>`)
	for _, r := range replies {
		sent := r.SentAt
		if len([]rune(sent)) > 16 {
			sent = string([]rune(sent)[:16])
		}
		b.WriteString(`<li><span class="mail-replies-when">` + stdhtml.EscapeString(sent+" ") + `</span><a href="/` + stdhtml.EscapeString(r.PageID) + `">` +
			stdhtml.EscapeString(firstNonEmpty(r.Title, r.PageID)) + `</a>`)
		if r.To != "" {
			b.WriteString(`<span class="mail-replies-to"> → ` + stdhtml.EscapeString(r.To) + `</span>`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul></div>`)
	return b.String()
}

// MadeMarkHTML は「✓ 種類」の印です（記録から作ったページ・解析で生まれたページ。押すとそのページへ飛ぶ）。
// 記録の箱の中で、作るページ（ここ）と東邦の 🤖 解析（ext/toho/record_sections.go）が使います。
func MadeMarkHTML(pageID, title, kind string) string {
	return `<a class="vocab-chrome attach-analyzed" href="/` + stdhtml.EscapeString(pageID) + `" title="` +
		stdhtml.EscapeString(firstNonEmpty(title, pageID)+" を作りました（押すと開きます）") + `">✓ ` + stdhtml.EscapeString(kind) + `</a>`
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
