package cms

// ─────────────────────────────────────────────────────────────────────────
// タグの値を置き換える（管理者・2026-10-01）
//
// 利用者:「見積もりという見出しや値は見積と短くした方がよいと思います。これは設定ファイルも変える必要があると思います。」
//
// 設定の語彙で選択肢の言葉を変えると（`区分` の「見積もり」→「見積」）、**既にあるページのタグは古い言葉のまま**残り、
// 新しい選択肢では絞れなくなります。どの業務でも起きることなのでコアに置きます（語の中身は知らない）。
//
//   - 当てるのは**可変タグの組だけ**——`<dt>名前</dt><dd>今の値</dd>`（間の空白は問わない）。本文の地の文・表・件名は触らない。
//   - 探すのは索引（`PagesByTag`・生の値の完全一致）。書くのは正本のファイル（`RewriteBody`——版が残るので戻せる・索引も直る）。
//   - 同じページに新しい値の組が既にあれば、古い組を消すだけ（同じ値を2つにしない）。
//   - **編集中のページは飛ばす**（オートセーブと上書きし合う・競合対策 §9）——飛ばしたページを返すので、閉じてからもう一度押す。
//   - テンプレートの中は書き換えない（`RewriteBody` が断る）——飛ばしたと返す。
//   - `dry` なら数えるだけ（画面は数を見せてから確かめる）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	stdhtml "html"
	"net/http"
	"regexp"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// tagRenameResult は置き換えた結果です（ページIDは6桁の文字列）。
type tagRenameResult struct {
	Found    []string `json:"found"`    // 索引で当たったページ
	Changed  []string `json:"changed"`  // 書き換えたページ
	Editing  []string `json:"editing"`  // 編集中で飛ばしたページ
	Template []string `json:"template"` // テンプレートの中で飛ばしたページ
	Missed   []string `json:"missed"`   // 索引では当たったのに本文に組が見つからなかったページ
}

// renameTagValue は本文の可変タグ name の値 from を to に置き換えた本文を返します（変わらなければ同じ文字列）。
func renameTagValue(body, name, from, to string) string {
	dt := `<dt>` + regexp.QuoteMeta(stdhtml.EscapeString(name)) + `</dt>`
	pair := func(v string) *regexp.Regexp {
		return regexp.MustCompile(`(` + dt + `\s*)<dd>\s*` + regexp.QuoteMeta(stdhtml.EscapeString(v)) + `\s*</dd>`)
	}
	old := pair(from)
	if !old.MatchString(body) {
		return body
	}
	if pair(to).MatchString(body) {
		// 新しい値の組が既にある——古い組（dt と dd）を消す。
		return regexp.MustCompile(dt+`\s*<dd>\s*`+regexp.QuoteMeta(stdhtml.EscapeString(from))+`\s*</dd>`).ReplaceAllString(body, "")
	}
	return old.ReplaceAllString(body, `${1}<dd>`+strings.ReplaceAll(stdhtml.EscapeString(to), "$", "$$")+`</dd>`)
}

// renameTagValueEverywhere は、可変タグ name の値が from のページ全部で to に置き換えます（dry なら数えるだけ）。
func renameTagValueEverywhere(author, name, from, to string, dry bool) (tagRenameResult, error) {
	var res tagRenameResult
	ids, err := PagesByTag(database.DB, name, from)
	if err != nil {
		return res, err
	}
	for _, id := range ids {
		pid := page.FormatID(id)
		res.Found = append(res.Found, pid)
		if IsTemplateArea(pid) {
			res.Template = append(res.Template, pid)
			continue
		}
		if _, open := editlock.Locks.EditorOpen(id); open {
			res.Editing = append(res.Editing, pid)
			continue
		}
		body, err := ReadPageBody(pid)
		if err != nil {
			return res, err
		}
		if renameTagValue(body, name, from, to) == body {
			res.Missed = append(res.Missed, pid)
			continue
		}
		if dry {
			res.Changed = append(res.Changed, pid)
			continue
		}
		if err := RewriteBody(pid, author, func(cur string) string { return renameTagValue(cur, name, from, to) }); err != nil {
			return res, err
		}
		res.Changed = append(res.Changed, pid)
	}
	return res, nil
}

// TagRenameAPIHandler は POST /api/admin/tag-rename です（admin 限定）。
// 入力: {name, from, to, dry}——可変タグ name の値が from のページを全部 to に置き換えます。
func TagRenameAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !page.RequireAdmin(w, r) {
		return
	}
	user, ok := GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
		From string `json:"from"`
		To   string `json:"to"`
		Dry  bool   `json:"dry"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	req.Name, req.From, req.To = strings.TrimSpace(req.Name), strings.TrimSpace(req.From), strings.TrimSpace(req.To)
	if req.Name == "" || req.From == "" || req.To == "" {
		JSONFail(w, http.StatusBadRequest, "タグの名前・今の値・新しい値を書いてください")
		return
	}
	if req.From == req.To {
		JSONFail(w, http.StatusBadRequest, "今の値と新しい値が同じです")
		return
	}
	res, err := renameTagValueEverywhere(user.Username, req.Name, req.From, req.To, req.Dry)
	if err != nil {
		// 途中まで書いたものは残る（版があるので戻せる）——どこまで進んだかを添える。
		json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "置き換えの途中で止まりました: " + err.Error(), "result": res})
		return
	}
	if !req.Dry && len(res.Changed) > 0 {
		auth.Audit(user.Username, "tag.rename", req.Name+": "+req.From+" → "+req.To+"（"+strings.Join(res.Changed, ",")+"）")
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "dry": req.Dry, "result": res})
}
