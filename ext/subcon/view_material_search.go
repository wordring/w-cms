package subcon

// ─────────────────────────────────────────────────────────────────────────
// 材料を探す画面（2026-09-21）
//
// ユーザー:「あとで**材質形状寸法で検索**したいときがあります」。
//
// **ビューです**——本文に `<section data-type="material-search">` を置くと、
// そのページに探す欄が出ます。⚠ **どこに置いてもよい**ので、取引先の下にも
// 受注ページにも置けます（計算ビューの流儀）。
//
// ⚠ **欄はサーバーが描きます。** 画面（`app.js`）が自分で組むと、拡張を外した
// ビルドで**空の欄だけが残ります**——引き金が立たないので中身は来ません。
// 押したときの通信だけを `app.js` が受け持ちます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"html"
	"net/http"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// MaterialSearchViewType はビューの形式名です。
const MaterialSearchViewType = "material-search"

// materialSearchViewHTML は探す欄を描きます（結果は押してから）。
//
// ⚠ **最初は結果を出しません。** 条件ゼロで全件を出すと、**探しに来た人が
// 全部を眺める**ことになります（`MaterialQuery.Empty` と同じ判断）。
func materialSearchViewHTML(user *auth.User, pageIDInt int) string {
	f := func(id, label, placeholder string) string {
		return searchFieldHTML("matsearch", id, label, placeholder, "text")
	}
	return `<h3 class="matsearch-title">🔎 材料を探す</h3>` +
		`<p class="matsearch-help">材質・形状は部分一致。厚み・径は一致。` +
		`寸法は書かれている数を空白区切りで（役割は問いません）。</p>` +
		`<div class="matsearch-form">` +
		f("material", "材質", "鉄 / SS400") +
		f("shape", "形状", "角パイプ / FB") +
		f("thickness", "厚み", "3.2") +
		f("diameter", "径", "40") +
		f("values", "寸法", "75 1090") +
		`<button type="button" class="matsearch-go" data-matsearch-go="1">探す</button>` +
		`</div><div class="matsearch-result" data-matsearch-result="1"></div>`
}

// searchFieldHTML は探す欄の1つ（見出し＋入力）を組みます。
//
// `attr` は画面の配線が読む `data-*` の名前（`matsearch`・`unorder`）——材料を探す欄と
// 未手配の欄が同じ3行を写していたので寄せました。サニタイズの後に足すクロームなので、
// **自分でエスケープの責任を負います**。
func searchFieldHTML(attr, id, label, placeholder, typ string) string {
	return `<label class="matsearch-field"><span>` + html.EscapeString(label) + `</span>` +
		`<input type="` + typ + `" class="matsearch-input" data-` + attr + `="` + id + `"` +
		` placeholder="` + html.EscapeString(placeholder) + `"/></label>`
}

// MaterialSearchAPIHandler は POST /api/material-search です。
func MaterialSearchAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Material  string `json:"material"`
		Shape     string `json:"shape"`
		Thickness string `json:"thickness"`
		Diameter  string `json:"diameter"`
		Values    string `json:"values"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	q := MaterialQuery{
		Material:  req.Material,
		Shape:     req.Shape,
		Thickness: parseDimNum(req.Thickness),
		Diameter:  parseDimNum(req.Diameter),
		Values:    parseDimNums(req.Values),
	}
	if q.Empty() {
		cms.JSONFail(w, http.StatusBadRequest, "探す条件を1つ以上入れてください")
		return
	}
	hits, err := SearchMaterials(user, q)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "検索に失敗しました: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true, "hits": hits})
}

// parseDimNum は1つの数を読みます（読めなければ 0＝指定なし）。
//
// ⚠ **接頭辞を受け付けます**——人は `t3.2` と打ちます。分解器と同じ読み方を
// 通すので、`φ40` も `Φ40` も効きます。
func parseDimNum(s string) float64 {
	parts := ParseDimension(s)
	for _, p := range parts {
		if p.HasNum() {
			return p.Num
		}
	}
	return 0
}

// parseDimNums は空白・カンマ・`*` 区切りの数を読みます。
func parseDimNums(s string) []float64 {
	var out []float64
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '　' || r == ',' || r == '、' || r == '*' || r == '×'
	}) {
		if v, err := strconv.ParseFloat(strings.TrimSpace(tok), 64); err == nil {
			out = append(out, v)
			continue
		}
		if v := parseDimNum(tok); v != 0 {
			out = append(out, v)
		}
	}
	return out
}
