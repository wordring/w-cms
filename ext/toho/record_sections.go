package toho

// 記録の箱に東邦が足す節（2026-10-20・ext/comm/record_box.go・docs/考察/【考察】画面の JS のコアと拡張.md §4.5）。
//
//   - 整理の欄の置き場（filing-slot）——中身は東邦の JS が /api/filing-proposal を引いて組む（整理するものがあるときだけ出す）。
//   - 🤖 解析——記録の添付（PDF・表計算・中に PDF のある ZIP）を並べ、解析済みなら結果の印を添える。ボタンは東邦の JS が
//     置き場（analyze-slot）に組む。それまでは東邦の JS が、本文の添付のリンクの横にボタンを足していた（2026-10-20 利用者の
//     「この案で進めてください」で、箱の中の一覧へ移した）。

import (
	stdhtml "html"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

func init() {
	comm.RegisterRecordSection(comm.RecordSection{Order: 30, Render: filingSlotHTML})
	comm.RegisterRecordSection(comm.RecordSection{Order: 40, Render: analyzeSectionHTML})
}

func filingSlotHTML(user *auth.User, pageIDInt int, draft bool) string {
	if draft {
		return ""
	}
	return `<div class="filing-slot" data-page="` + page.FormatID(pageIDInt) + `"></div>`
}

var recordAttachRe = regexp.MustCompile(`^/([0-9]{6})/([0-9a-z]+)\.(pdf|xlsx|zip)$`)

// analyzeSectionHTML は「🤖 解析」の節です（解析できる添付が無ければ出さない）。
func analyzeSectionHTML(user *auth.User, pageIDInt int, draft bool) string {
	if draft {
		return ""
	}
	pageID := page.FormatID(pageIDInt)
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return ""
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return ""
	}
	done, _ := analyzedAttachments(user, pageID)
	zips := zipPDFsOf(pageID)
	seen := map[string]bool{}
	var items strings.Builder
	for _, n := range nodes {
		cms.WalkElements(n, func(el *html.Node) {
			if el.Data != "a" {
				return
			}
			m := recordAttachRe.FindStringSubmatch(cms.Attr(el, "href"))
			if m == nil || m[1] != pageID || seen[m[2]] {
				return
			}
			if m[3] == "zip" && len(zips[m[2]]) == 0 {
				return // 中に PDF の無い ZIP は解析するものが無い
			}
			seen[m[2]] = true
			name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(textOf(el)), "📎"))
			if name == "" {
				name = m[2] + "." + m[3]
			}
			file := m[2] + "." + m[3]
			attrs := ` data-page="` + pageID + `" data-file="` + stdhtml.EscapeString(file) + `"`
			if m[3] == "zip" {
				attrs += ` data-zip="` + m[2] + `"`
			}
			mark := ""
			if r, ok := done[m[2]]; ok {
				attrs += ` data-done="1"`
				mark = ` <a class="vocab-chrome attach-analyzed" href="/` + stdhtml.EscapeString(r.PageID) + `" title="` +
					stdhtml.EscapeString(firstNonEmptyText(r.Title, r.PageID)+" を作りました（押すと開きます）") + `">✓ ` + stdhtml.EscapeString(r.Kind) + `</a>`
			}
			items.WriteString(`<li><a href="/` + pageID + `/` + stdhtml.EscapeString(file) + `" target="_blank" rel="noopener">` +
				stdhtml.EscapeString(name) + `</a> <span class="analyze-slot"` + attrs + `></span>` + mark + `</li>`)
		})
	}
	if items.Len() == 0 {
		return ""
	}
	return `<div class="record-files"><p class="record-files-head">🤖 解析（添付）</p><ul>` + items.String() + `</ul></div>`
}

func firstNonEmptyText(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
