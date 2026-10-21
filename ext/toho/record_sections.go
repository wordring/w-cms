package toho

// 記録の箱に東邦が足す節（2026-10-20・ext/comm/record_box.go・docs/考察/【考察】画面の JS のコアと拡張.md §4.5）。
//
//   - 「📁 整理（N件）」のボタン（整理するものがあるときだけ）——押すと東邦の JS が /api/filing-proposal を引いて行き先の表を組む。
//   - 🤖 解析——記録の添付（PDF・表計算・中に PDF のある ZIP）を並べ、ボタン（🤖 解析／🤖 再解析／まとめて解析）と、解析済みなら
//     結果の印を描く。押したときの働きは東邦の JS が document へ委ねて配線する。それまでは東邦の JS が、本文の添付のリンクの横に
//     ボタンを足していた（2026-10-20 利用者の「この案で進めてください」で、箱の中の一覧へ移した。同じ夕方、ボタンもサーバーが描く形に）。

import (
	stdhtml "html"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

func init() {
	comm.RegisterRecordSection(comm.RecordSection{Order: 30, Render: filingSlotHTML})
	comm.RegisterRecordSection(comm.RecordSection{Order: 40, Render: analyzeSectionHTML})
}

// filingSlotHTML は「📁 整理（N件・ファイル M）」です。**整理するものが実際にあるときだけ出します**——押してから「ありません」と
// 言うより、出さないほうが親切です。押したときに候補（/api/filing-proposal）を引いて行き先の表を組むのは東邦の JS。
// 2026-10-20 夕までは、JS がページを開くたびに候補を全部引いて、このボタンを組んでいた。
func filingSlotHTML(user *auth.User, pageIDInt int, draft bool) string {
	if draft {
		return ""
	}
	pageID := page.FormatID(pageIDInt)
	rows, _ := drawingChildrenOf(user, pageIDInt)
	orders, _ := orderChildrenOf(user, pageIDInt)
	files := mailFilesOf(pageID)
	n := len(rows) + len(orders)
	if n == 0 && len(files) == 0 {
		return ""
	}
	label := "📁 整理（"
	if n > 0 {
		label += strconv.Itoa(n) + "件"
	}
	if n > 0 && len(files) > 0 {
		label += "・"
	}
	if len(files) > 0 {
		label += "ファイル " + strconv.Itoa(len(files))
	}
	label += "）"
	return `<div class="filing-slot"><button type="button" class="vocab-chrome filing-chrome filing-open" data-page="` + pageID + `">` +
		label + `</button></div>`
}

// analyzeKinds は、記録の箱の 🤖 解析に並べる添付の拡張子です（2026-10-21 夜から画像も——FAX と写真のため）。
var analyzeKinds = map[string]bool{".pdf": true, ".xlsx": true, ".zip": true, ".png": true, ".jpg": true, ".jpeg": true, ".webp": true}

// analyzeSectionHTML は「🤖 解析」の節です（解析できる添付が無ければ出さない）。
//
// 並べるのは、**この記録に置いた添付の全部**です（2026-10-21 夜・【考察】通信箱の改善 §4.1）。
//
// > 2026-10-21 利用者:「どの方法で作られたとしても解析ボタンが必要です。そしてメモのように人が書き込んで作るものは、人がPDFを挿入する場合があります。それにも解析ボタンが必要になります。」
//
// それまでは本文の中の添付へのリンク（`<a href="/<ページ>/<ID>.pdf">`）だけを拾っていたので、メールの取り込みが書いた添付には出るが、
// 人がエディタで入れた PDF（ファイル表示の印）と画像（本文の中の絵）には出なかった。いまは添付の目録（files/）を見るので、
// 本文にどう置いたか（リンク・ファイル表示・絵）や、どの入口で作った記録か（メール・電話・FAX・メモ）によらず並ぶ。
// 並びは置いた順（目録の保存時刻）。ZIP の中から取り出したものは「↳ 中のパス」。
func analyzeSectionHTML(user *auth.User, pageIDInt int, draft bool) string {
	if draft {
		return ""
	}
	pageID := page.FormatID(pageIDInt)
	type file struct {
		stored string
		meta   cms.AttachmentMeta
	}
	var files []file
	for stored, m := range cms.ReadAttachmentMetas(pageID) {
		if analyzeKinds[strings.ToLower(filepath.Ext(stored))] {
			files = append(files, file{stored, m})
		}
	}
	if len(files) == 0 {
		return ""
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].meta.SavedAt != files[j].meta.SavedAt {
			return files[i].meta.SavedAt < files[j].meta.SavedAt
		}
		return files[i].stored < files[j].stored
	})
	done, _ := analyzedAttachments(user, pageID)
	zips := zipPDFsOf(pageID)
	var items strings.Builder
	for _, f := range files {
		id := strings.TrimSuffix(f.stored, filepath.Ext(f.stored))
		ext := strings.ToLower(filepath.Ext(f.stored))
		var btn string
		if ext == ".zip" {
			btn = zipAnalyzeButtonHTML(pageID, id, zips[id], done)
			if btn == "" {
				continue // 中に PDF の無い ZIP・中の PDF が全部解析済みの ZIP は、解析するものが無い
			}
		} else {
			label := "🤖 解析"
			if _, ok := done[id]; ok {
				label = "🤖 再解析" // 読み違いはあるので、解析済みでも押し直せる
			}
			btn = `<button type="button" class="vocab-chrome attach-analyze" data-page="` + pageID +
				`" data-file="` + stdhtml.EscapeString(f.stored) + `">` + label + `</button>`
		}
		name := strings.TrimSpace(f.meta.Name)
		if src, ok := strings.CutPrefix(f.meta.Source, "zip:"); ok {
			if _, inner, ok := strings.Cut(src, "/"); ok && strings.TrimSpace(inner) != "" {
				name = "↳ " + inner
			}
		}
		if name == "" {
			name = f.stored
		}
		mark := ""
		if r, ok := done[id]; ok {
			mark = ` ` + comm.MadeMarkHTML(r.PageID, r.Title, r.Kind)
		}
		items.WriteString(`<li><a href="/` + pageID + `/` + stdhtml.EscapeString(f.stored) + `" target="_blank" rel="noopener">` +
			stdhtml.EscapeString(name) + `</a> ` + btn + mark + `</li>`)
	}
	if items.Len() == 0 {
		return ""
	}
	return `<div class="record-files"><p class="record-files-head">🤖 解析（添付）</p><ul>` + items.String() + `</ul></div>`
}

// zipAnalyzeButtonHTML は ZIP の「🤖 中のPDFをまとめて解析（N件）」です（2026-09-30 利用者:「添付ファイルのPDFについて、
// ZIPの場合まとめて解析するオプションが欲しいです」）。⚠ **解析済みのものは数えない**（押し直しで同じページを二重に作らない
// ——読み違いの解析し直しは1件ずつの「🤖 再解析」で）。解析するものが無ければ空。
// 2026-10-20 夕までは東邦の JS が /api/analyzed を引いて組んでいた（いまは押したときだけ引き直す）。
func zipAnalyzeButtonHTML(pageID, zipID string, pdfs []zipPDF, done map[string]analyzedResult) string {
	todo := 0
	for _, p := range pdfs {
		if _, ok := done[p.ID]; !ok {
			todo++
		}
	}
	if todo == 0 {
		return ""
	}
	title := "中のPDFを1つずつ順に解析します（Gemini を " + strconv.Itoa(todo) + "回呼びます・1件に数十秒）"
	if skipped := len(pdfs) - todo; skipped > 0 {
		title += "。解析済みの " + strconv.Itoa(skipped) + "件は飛ばします"
	}
	return `<button type="button" class="vocab-chrome attach-analyze attach-analyze-zip" data-page="` + pageID +
		`" data-zip="` + stdhtml.EscapeString(zipID) + `" title="` + stdhtml.EscapeString(title) + `">🤖 中のPDFをまとめて解析（` +
		strconv.Itoa(todo) + `件）</button>`
}
