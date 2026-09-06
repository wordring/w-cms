package sheetmetal

// ─────────────────────────────────────────────────────────────────────────
// 解析済みの印（2026-09-06）
//
// ユーザー:「一度解析したファイルには解析済みの印と『図面』などの解析結果を
// 付けては？」「添付ファイルのボタンの横あたりで良いのでは？」
//
// **新しく持つデータはありません。** 解析が作るページには由来の参照タグ
// `受信元：<元ページID>-<添付ID>` が既に入っているので、**逆に引けば
// 「この添付から何が生まれたか」が分かります**（アドレス帳・社名の揺れと同じ形
// ——材料はもう索引にある）。
//
// 印を別に持たない利点が1つあります。**生まれたページを消せば印も消えます**
// ——間違った解析をゴミ箱へ入れたのに「解析済み」が残ると、二度と解析できません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// SourceRefTag は解析が書く由来の参照タグの名前です（`受信元`）。
// analyze_pdf.go が書く名前と揃えること——ここがずれると印が出なくなります。
const SourceRefTag = "受信元"

// analyzedResult は添付1件から生まれたページです。
type analyzedResult struct {
	Kind   string `json:"kind"`    // 図面 / 受注
	PageID string `json:"page_id"` // 生まれたページ
	Title  string `json:"title"`
}

// AnalyzedAPIHandler は GET /api/analyzed?page_id=X です。
// そのページの添付のうち、**解析済みのもの**を「添付ID → 結果」で返します。
func AnalyzedAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	pageID, ok := page.NormalizeID(r.URL.Query().Get("page_id"))
	if !ok {
		cms.JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	user := auth.CurrentUser(r)
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !page.CanView(user, idInt) {
		cms.JSONFail(w, http.StatusNotFound, "ページが見つかりません")
		return
	}
	out, err := analyzedAttachments(user, pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "調べられません: "+err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "analyzed": out})
}

// analyzedAttachments は「添付ID → 生まれたページ」を返します。
func analyzedAttachments(user *auth.User, pageID string) (map[string]analyzedResult, error) {
	rows, err := database.DB.Query(
		`SELECT page_id, value FROM vocab_index WHERE field = ? AND value LIKE ?`,
		SourceRefTag, pageID+"-%")
	if err != nil {
		return nil, err
	}
	type hit struct {
		id  int
		val string
	}
	// **先に読み切ってから解釈します**（CanView と VocabBlocksOf が別のクエリを投げる）。
	var found []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.val); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	out := map[string]analyzedResult{}
	for _, h := range found {
		attachID := strings.TrimPrefix(h.val, pageID+"-")
		if attachID == "" || strings.Contains(attachID, "-") {
			continue // 形が違うものは触らない
		}
		if !page.CanView(user, h.id) {
			continue // 見せ分け（C案）——読めないものは黙って落ちる
		}
		kind := kindOfPage(h.id)
		if kind == "" {
			continue
		}
		// 同じ添付から2枚あることは普通は無いが、あれば**先に見つかったほう**を出す
		// （どちらも本物なので、どちらを見せても行き止まりにならない）。
		if _, dup := out[attachID]; dup {
			continue
		}
		out[attachID] = analyzedResult{
			Kind: kind, PageID: formatID(h.id), Title: pageTitleOf(formatID(h.id)),
		}
	}
	return out, nil
}

// kindOfPage はそのページが図面ページか受注ページかを返します（どちらでもなければ空）。
func kindOfPage(pageIDInt int) string {
	if blocks, err := cms.VocabBlocksOf(database.DB, pageIDInt, "drawing"); err == nil && len(blocks) > 0 {
		return "図面"
	}
	if blocks, err := cms.VocabBlocksOf(database.DB, pageIDInt, "client-order"); err == nil && len(blocks) > 0 {
		return "受注"
	}
	return ""
}
