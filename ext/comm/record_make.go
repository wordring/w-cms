package comm

// ─────────────────────────────────────────────────────────────────────────
// 通信の記録から作るページ（2026-10-01）
//
// 利用者:「メールから作るボタンは「受注ページ作成」「加工製品ページ作成」「返信」ページのタイプは増える可能性があるので
// 「受注ページ」「加工製品ページ」をコンボボックスで選択して「作成」ボタンを押せばいいかも」。
//
// メールの記録のページに「作るページ [選ぶ] [作成]」の欄を出します。選べる種類は**拡張が登録します**
// （`RegisterRecordMaker`）——通信は種類の名前も作り方も知らず、選ぶ欄と口だけを持ちます。
//
//	GET  /api/record-makers?page_id=X … そのページで選べる種類と、もう作ったページ（印）
//	POST /api/record-make {page_id, kind} … 作る（できたページ・言うこと・開くページを返す）
//
//   - 押すのは人。作るのは記録の子（取り込みで生まれたページには家が要る——通信箱の下）。
//   - 子ページを作る操作なので write 権限（本文は変えないので編集ロックは要らない）。
//   - 下書きでは出さない（まだ送っていないメール）。種類ごとに受信だけ・送信だけを選べる。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// MadePage は作ったページ1枚です。
type MadePage struct {
	PageID string `json:"page_id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"` // 印に出す短い名前（「受注」「加工製品」）
}

// MakeResult は作った結果です。
type MakeResult struct {
	Pages []MadePage `json:"pages"`          // 作ったページ（0枚もある——作らないと決めたとき）
	Say   string     `json:"say,omitempty"`  // 人へ言うこと（作らなかった理由・結んだ行の数など）
	Open  string     `json:"open,omitempty"` // 作ったあと開くページ（人が続けて書くもの）
}

// MakeError は人へ返す断りです（HTTP の状態つき）。ほかのエラーは 500 で返します。
type MakeError struct {
	Status  int
	Message string
}

func (e *MakeError) Error() string { return e.Message }

// RecordMaker は記録から作れるページの種類です。
type RecordMaker struct {
	Name  string // 選ぶ欄に出す名前（「受注ページ」）
	Hint  string // 選ぶ欄の説明（何を読んで何を作るか）
	Order int    // 並び（小さいほど先）
	// Directions は出す向き（受信・送信）。空ならどちらにも出す。
	Directions []string
	// Make は作ります（pageID は記録のページ）。
	Make func(user *auth.User, pageID string) (MakeResult, error)
	// Made は、この記録から**もう作った**ページです（印に出す・読めるものだけ）。nil でよい。
	Made func(user *auth.User, pageID string) []MadePage
}

var recordMakers = map[string]RecordMaker{}

// RegisterRecordMaker は種類を登録します（拡張の init から）。二重登録はその場で落とします。
func RegisterRecordMaker(m RecordMaker) {
	if m.Name == "" || m.Make == nil {
		panic("comm.RegisterRecordMaker: 名前か作り方が空です")
	}
	if _, dup := recordMakers[m.Name]; dup {
		panic("comm.RegisterRecordMaker: 「" + m.Name + "」は登録済みです")
	}
	recordMakers[m.Name] = m
}

// makersFor は、その記録で選べる種類を並びの順に返します（下書き・記録でないページは空）。
func makersFor(pageIDInt int) []RecordMaker {
	tags, err := cms.TagsOfPage(database.DB, pageIDInt)
	if err != nil || cms.FirstTag(tags, ChannelTag) == "" || cms.FirstTag(tags, DraftTag) != "" {
		return nil
	}
	dir := cms.FirstTag(tags, DirectionTag)
	var out []RecordMaker
	for _, m := range recordMakers {
		if len(m.Directions) > 0 && !contains(m.Directions, dir) {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// RecordMakersAPIHandler は GET /api/record-makers?page_id=X です——選べる種類（名前・説明）と、もう作ったページ。
func RecordMakersAPIHandler(w http.ResponseWriter, r *http.Request) {
	pageID, idInt, user, ok := cms.GateJSONPageRead(w, r, r.URL.Query().Get("page_id"))
	if !ok {
		return
	}
	type kind struct {
		Name string `json:"name"`
		Hint string `json:"hint"`
	}
	kinds := []kind{}
	made := []MadePage{}
	// 書けない人には選ぶ欄を出さない（押しても 403 になるだけ）——印だけは見せる。
	canWrite := page.GetPerms(idInt).CanWrite(user)
	for _, m := range makersFor(idInt) {
		if canWrite {
			kinds = append(kinds, kind{Name: m.Name, Hint: m.Hint})
		}
		if m.Made != nil {
			made = append(made, m.Made(user, pageID)...)
		}
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "kinds": kinds, "made": made})
}

// RecordMakeAPIHandler は POST /api/record-make です。入力: {page_id, kind}。
func RecordMakeAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Kind   string `json:"kind"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, ok := cms.PageIDOrFail(w, req.PageID)
	if !ok {
		return
	}
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	idInt, _ := strconv.Atoi(pageID)
	var maker *RecordMaker
	for _, m := range makersFor(idInt) {
		if m.Name == req.Kind {
			m := m
			maker = &m
			break
		}
	}
	if maker == nil {
		cms.JSONFail(w, http.StatusBadRequest, "このページからは「"+req.Kind+"」を作れません")
		return
	}
	res, err := maker.Make(user, pageID)
	if err != nil {
		var me *MakeError
		if errors.As(err, &me) {
			cms.JSONFail(w, me.Status, me.Message)
			return
		}
		cms.JSONFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if res.Pages == nil {
		res.Pages = []MadePage{}
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "pages": res.Pages, "say": res.Say, "open": res.Open})
}
