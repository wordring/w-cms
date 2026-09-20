package cms

import (
	"database/sql"
	"net/http"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ページ権限の管理API（フェーズ3）。サイドカー（正本）を書き換えてから
// page_perms を更新します。これらのAPIのみがサイドカーを書き換えます。

// PagePermsHandler は GET で現在の権限を返し、POST で mode / group を変更します（chmod / chgrp）。
// GET: read 権限。POST: 対象ページの owner または admin（認証認可設計.md 3.5節）。
func PagePermsHandler(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	if u == nil {
		http.Error(w, "認証が必要です", http.StatusUnauthorized)
		return
	}
	// サイドカーのパスに使うためゼロ詰め6桁へ正規化する（page.NormalizeID 参照）。
	id, pageID, ok := queryPageID(w, r)
	if !ok {
		return
	}

	cur := page.GetPerms(pageID)

	// GET: 現在の権限を返す（read権限が必要）
	if r.Method == http.MethodGet {
		if !cur.CanRead(u) {
			http.Error(w, "このページの権限を参照できません", http.StatusForbidden)
			return
		}
		canPublish := u.IsAdmin || u.Username == cur.Owner
		WriteJSON(w, map[string]any{
			"owner": cur.Owner, "group": cur.Group, "mode": cur.Mode,
			"can_chmod": canPublish, "can_chown": u.IsAdmin,
			// 匿名公開（認証認可設計.md 10章）。public はこのページ自身のフラグ、
			// effective_public は親チェーンとの AND による実際の公開状態。
			"public": cur.Public, "effective_public": page.EffectivePublic(pageID),
			"can_publish": canPublish, "can_write": cur.CanWrite(u),
		})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !u.IsAdmin && u.Username != cur.Owner {
		http.Error(w, "権限を変更できるのは所有者または管理者のみです", http.StatusForbidden)
		return
	}
	// エディタ内の変更操作は本文編集と同じ編集ロックで直列化する（他者保持中なら409）。
	if !editlock.RequireEditLock(w, r, id) {
		return
	}

	var req struct {
		Mode   *string `json:"mode"`
		Group  *string `json:"group"`
		Public *bool   `json:"public"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}

	if req.Mode != nil && !page.ValidMode(*req.Mode) {
		http.Error(w, "mode は3桁・各桁0〜3で指定してください（例: 330）", http.StatusBadRequest)
		return
	}
	// 匿名公開フラグの変更（認証認可設計.md 10章）。公開（true）にするときだけ
	// パスゲートを検証する: 親が実効公開でなければ公開できない（ルートは親なしで可）。
	if req.Public != nil && *req.Public && !parentIsPublishable(pageID) {
		http.Error(w, "親ページが公開されていないため公開できません。先に親ページを公開するか、公開可能な親へ移動してください。", http.StatusForbidden)
		return
	}
	// 監査の動詞は**最初に指定された欄**で決める（mode → group → public の順）。
	action := ""
	switch {
	case req.Mode != nil:
		action = "chmod"
	case req.Group != nil:
		action = "chgrp"
	case req.Public != nil && *req.Public:
		action = "publish"
	case req.Public != nil:
		action = "unpublish"
	default:
		http.Error(w, "mode・group・public のいずれかを指定してください", http.StatusBadRequest)
		return
	}

	p, ok := rewriteSidecarPerms(w, id, "権限", func(p *page.PageMeta) {
		if req.Mode != nil {
			p.Mode = *req.Mode
		}
		if req.Group != nil {
			p.Group = *req.Group
		}
		if req.Public != nil {
			p.Public = *req.Public
		}
	})
	if !ok {
		return
	}
	auth.Audit(u.Username, action, id)

	WriteJSON(w, map[string]any{
		"success": true, "owner": p.Owner, "group": p.Group, "mode": p.Mode,
		"public": p.Public, "effective_public": page.EffectivePublic(pageID),
	})
}

// rewriteSidecarPerms は権限系の書き換えの作法です: サイドカー（正本）を読み、change で
// 変え、書き戻し、派生（page_perms）を更新する。失敗は応答に書いて ok=false。
// chmod／chgrp／publish（PagePermsHandler）と chown（PageChownHandler）が共有します。
//
// 現在のサイドカーを起点に変更します。読めなければ**書かずに止めます**
// ——派生（page_perms）から組み立て直して書き戻すと、親も作成情報も失ったまま
// 「見た目は健全なサイドカー」を新造してしまう（2026-08-21 決定）。what は断り文に入る
// 対象の呼び名（「権限」「所有者」）。
func rewriteSidecarPerms(w http.ResponseWriter, id, what string, change func(*page.PageMeta)) (page.PageMeta, bool) {
	p, ok := page.ReadSidecar(id)
	if !ok {
		http.Error(w, "ページ属性ファイルを読めないため"+what+"を変更できません。管理者が手作業で修復してください。",
			http.StatusConflict)
		return page.PageMeta{}, false
	}
	change(&p)
	if err := page.WriteSidecar(id, p); err != nil {
		http.Error(w, "サイドカーの書き込みに失敗しました: "+err.Error(), http.StatusInternalServerError)
		return page.PageMeta{}, false
	}
	if err := page.RefreshPerms(id); err != nil {
		http.Error(w, "権限インデックスの更新に失敗しました: "+err.Error(), http.StatusInternalServerError)
		return page.PageMeta{}, false
	}
	return p, true
}

// parentIsPublishable は、ページ pageID を匿名公開してよいか（親チェーンが許すか）を返します。
// ルート（ID 0）は親なしで公開可。それ以外は親が実効公開（page.EffectivePublic）である必要があります
// （認証認可設計.md 10.2 のパスゲートを公開操作時に強制する）。
func parentIsPublishable(pageID int) bool {
	if pageID == 0 {
		return true // ルートは親なしで公開可（サイト全体のキルスイッチ側）
	}
	var parent sql.NullInt64
	if err := database.DB.QueryRow("SELECT parent_id FROM pages WHERE id = ?", pageID).Scan(&parent); err != nil {
		return false
	}
	if !parent.Valid {
		return false // ルート以外で親なしは異常。安全側に倒す
	}
	return page.EffectivePublic(int(parent.Int64))
}

// PageChownHandler は所有者を変更します（chown）。権限: admin のみ。
func PageChownHandler(w http.ResponseWriter, r *http.Request) {
	// **状態を変える口は POST 固定**（要件定義書 §4.1）。
	//
	// ⚠ **2026-09-14 まで、ここだけメソッドを見ていませんでした**——状態を変える
	// ハンドラの中で唯一の例外です。GET で状態が変わらないのは `DecodeJSONBody` が
	// 空ボディで落ちるという**偶然**に依っていました。CSRF の守り（CSRFProtect）は
	// GET を素通しするので、クエリパラメータの読み取りを1つ足した瞬間に
	// **admin を狙った `<img src>` の CSRF** になります。
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !page.RequireAdmin(w, r) {
		return
	}
	// サイドカーのパスに使うためゼロ詰め6桁へ正規化する（page.NormalizeID 参照）。
	id, _, ok := queryPageID(w, r)
	if !ok {
		return
	}
	// エディタ内の変更操作は本文編集と同じ編集ロックで直列化する（他者保持中なら409）。
	if !editlock.RequireEditLock(w, r, id) {
		return
	}

	var req struct {
		Owner string `json:"owner"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	if req.Owner == "" {
		http.Error(w, "owner は必須です", http.StatusBadRequest)
		return
	}

	p, ok := rewriteSidecarPerms(w, id, "所有者", func(p *page.PageMeta) { p.Owner = req.Owner })
	if !ok {
		return
	}
	auth.AuditRequest(r, "chown", id+"->"+req.Owner)

	WriteJSON(w, map[string]any{"success": true, "owner": p.Owner})
}
