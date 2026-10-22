package cms

import (
	"log"
	"net/http"
	"os"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// ─────────────────────────────────────────────────────────────────────────
// POST /api/sync——ブロック単位で合わせる保存（2026-10-12）
//
// 利用者:「排他のために編集モードを設けたが、今後は編集モードの代わりに、閲覧に邪魔なものを表示しない目的で、
// 閲覧モードを設けることにします。そして、ブロック単位の保護に変更し、保護の内容は編集中の同ブロックを同時に
// 編集したものが居たら、警告してマージできるように両方表示します」。形は【考察】編集モードの再考 §6 の問い1〜8。
//
// 画面は**書き始めたときの並びと指紋**（base——サーバーが前の応答で渡したもの）と、**いまの並び**（order）と、
// **変えたブロック**（changes——元の指紋・元の書き出し・いまの書き出し）を送る。サーバーはページの錠の中で
// いまの本文へ当てる:
//
//   - 変えたブロック: いまの指紋が元の指紋のまま → 当てる。違う → 中身を揃えて比べ直し、それでも相手も変えて
//     いたら表のセルごとに3つを比べて合わせる（mergeBlock3）。合わせられなければ**ぶつかった**として相手の版を
//     返し、本文は相手の版のまま（画面が両方を並べる——問い4）。
//   - 相手が消したブロックを変えていた → ぶつかった（kind "deleted"——画面が「消す」「残す」を聞く——問い6）。
//   - 消したブロック: 相手が変えていなければ消す。変えていれば**残す**（kept——知らずに消させない）。
//   - 足したブロック: 画面の並びで前にあるブロックの後ろへ。
//   - 並べ替え: 画面が並べ替えていれば画面の並び（後から並べた人の並び）、そうでなければいまの並びを保つ。
//     そのあいだに相手が足したブロックは、相手の並びで前にあるブロックの後ろへ（問い6）。
//
// 応答は**仕上がりの並びと指紋の全部**と、画面が持っていない・古い中身のブロックの書き出し（閲覧と同じ鏡を
// 描いたもの）。何も変えていなければ書かない（知らせも出ない）——変わったところを読むだけにも使う。
// ─────────────────────────────────────────────────────────────────────────

// SyncBase は画面が持っている並びの1つ（ID とサーバーの指紋）です。
type SyncBase struct {
	ID string `json:"id"`
	H  string `json:"h"`
}

// SyncChange は画面が変えた（足した）ブロックです。足したブロックは BaseH が空。
type SyncChange struct {
	ID       string `json:"id"`
	BaseH    string `json:"base_h"`
	BaseHTML string `json:"base_html"`
	HTML     string `json:"html"`
}

// SyncRequest は /api/sync の要求です。
type SyncRequest struct {
	PageID  string       `json:"page_id"`
	Full    bool         `json:"full"` // 全部のブロックの書き出しを返す（開いたとき・書ける形に入ったとき）
	Base    []SyncBase   `json:"base"`
	Order   []string     `json:"order"`
	Changes []SyncChange `json:"changes"`
	Tab     string       `json:"tab"` // 書いたタブ（知らせを自分のタブで受けないため）
	// Checkpoint は、書いたあとに版の区切りを打つ印です（ページを去るときの最後の保存・2026-10-22 の見直し）。それまでは画面が
	// 保存とは別に /api/version-checkpoint を送っていて、ページを去るときは区切りが最後の保存より先に届き、最後の書きかけが
	// 自分の版に閉じなかった。同じ錠の中で、書く → 版 → 区切り、の順にします。
	Checkpoint bool `json:"checkpoint,omitempty"`
}

// SyncBlockOut は仕上がりの1ブロックです。HTML は画面が描き直す（足す）ときだけ。
type SyncBlockOut struct {
	ID   string `json:"id"`
	H    string `json:"h"`
	HTML string `json:"html,omitempty"`
	Why  string `json:"why,omitempty"` // other（相手が変えた）・new（相手が足した）・kept（消したが残した）・merged・sanitized・full
}

// SyncConflict はぶつかったブロックです。kind は changed（相手が変えた——HTML は相手の版）か deleted（相手が消した）。
type SyncConflict struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	HTML string `json:"html,omitempty"`
	H    string `json:"h,omitempty"`
	By   string `json:"by,omitempty"`
}

// syncPlan は当てた結果です（書く前・描く前）。
type syncPlan struct {
	final     []storedBlock
	why       map[string]string // 画面へ書き出しを渡すブロック → 理由
	conflicts []SyncConflict
	kept      []string
	sanitized map[string]bool // 画面の書き出しをサニタイズが変えたブロック
	dirty     bool            // 本文が変わる（書く）
}

// planSync はいまの本文（ID を揃えたもの）へ画面の変更を当てます。mine は変えたブロックの正本の形（サニタイズ済み・
// ID は変更の ID）。⚠ **純粋な関数**——錠・ファイル・描画には触らない（試験で全部の組み合わせを流すため）。
func planSync(current []storedBlock, req SyncRequest, mine map[string]storedBlock, sanitized map[string]bool) syncPlan {
	p := syncPlan{why: map[string]string{}, sanitized: sanitized}
	curIdx := map[string]int{}
	for i, b := range current {
		curIdx[b.ID] = i
	}
	baseH := map[string]string{}
	var baseOrder []string
	for _, b := range req.Base {
		baseH[b.ID] = b.H
		baseOrder = append(baseOrder, b.ID)
	}
	inClient := map[string]bool{}
	for _, id := range req.Order {
		inClient[id] = true
	}

	// 消したブロック——相手が変えていれば残す。
	deleted := map[string]bool{}
	for _, id := range baseOrder {
		if inClient[id] {
			continue
		}
		i, ok := curIdx[id]
		if !ok {
			continue // 相手も消していた
		}
		if current[i].Hash == baseH[id] {
			deleted[id] = true
			p.dirty = true
		} else {
			p.kept = append(p.kept, id)
			p.why[id] = "kept"
		}
	}

	// 変えた・足したブロック。
	replaced := map[string]storedBlock{}
	added := map[string]bool{}
	conflictDeleted := map[string]bool{}
	for _, c := range req.Changes {
		if !inClient[c.ID] {
			continue
		}
		m, ok := mine[c.ID]
		if !ok {
			continue // サニタイズで中身が無くなった——当てない
		}
		i, exists := curIdx[c.ID]
		if c.BaseH == "" {
			if !exists {
				added[c.ID] = true
				replaced[c.ID] = m
				p.dirty = true
				if sanitized[c.ID] {
					p.why[c.ID] = "sanitized"
				}
				continue
			}
			// 足したつもりのブロックが既にある（前の応答が届かなかった など）——同じなら何もしない。
			cur := current[i]
			if cur.Hash == m.Hash || canonBlock(cur.HTML) == canonBlock(m.HTML) {
				continue
			}
			p.conflicts = append(p.conflicts, SyncConflict{ID: c.ID, Kind: "changed", H: cur.Hash})
			continue
		}
		if !exists {
			conflictDeleted[c.ID] = true
			p.conflicts = append(p.conflicts, SyncConflict{ID: c.ID, Kind: "deleted"})
			continue
		}
		cur := current[i]
		switch {
		case cur.Hash == c.BaseH || canonBlock(cur.HTML) == canonBlock(c.BaseHTML):
			if cur.Hash != m.Hash {
				replaced[c.ID] = m
				p.dirty = true
			}
			if sanitized[c.ID] {
				p.why[c.ID] = "sanitized"
			}
		case canonBlock(cur.HTML) == canonBlock(m.HTML):
			// 相手と同じに変えた——相手の版のまま。
		default:
			if merged, ok := mergeBlock3(c.BaseHTML, m.HTML, cur.HTML); ok {
				if mb, ok := storedFormOf(merged, c.ID); ok {
					replaced[c.ID] = mb
					p.why[c.ID] = "merged"
					p.dirty = true
					continue
				}
			}
			p.conflicts = append(p.conflicts, SyncConflict{ID: c.ID, Kind: "changed", H: cur.Hash})
		}
	}

	// 並び。
	var curOrder []string
	for _, b := range current {
		if !deleted[b.ID] {
			curOrder = append(curOrder, b.ID)
		}
	}
	var result []string
	if clientReordered(baseOrder, req.Order) {
		inResult := map[string]bool{}
		for _, id := range req.Order {
			if conflictDeleted[id] {
				continue
			}
			if _, ok := curIdx[id]; (ok && !deleted[id]) || added[id] {
				result = append(result, id)
				inResult[id] = true
			}
		}
		for k, id := range curOrder {
			if inResult[id] {
				continue
			}
			result = insertAfterPred(result, inResult, curOrder[:k], id)
			inResult[id] = true
		}
	} else {
		result = append(result, curOrder...)
		inResult := map[string]bool{}
		for _, id := range result {
			inResult[id] = true
		}
		// 相手がそのあいだに同じ位置へ足したブロック（元の並びにも画面にも無いもの）は飛び越える——
		// 同じ位置に2人が足したら、先に保存した方が上（問い6）。
		unknown := func(id string) bool { _, inBase := baseH[id]; return !inBase && !inClient[id] }
		for k, id := range req.Order {
			if !added[id] {
				continue
			}
			result = insertAfterPredSkipping(result, inResult, req.Order[:k], id, unknown)
			inResult[id] = true
		}
	}
	if !p.dirty && !sameOrder(result, idsOf(current)) {
		p.dirty = true
	}

	for _, id := range result {
		if b, ok := replaced[id]; ok {
			p.final = append(p.final, b)
			continue
		}
		p.final = append(p.final, current[curIdx[id]])
		if !inClient[id] {
			if _, ok := p.why[id]; !ok {
				p.why[id] = "new"
			}
		}
	}
	return p
}

// insertAfterPred は id を、preds の中で後ろから見て最初に result にあるものの後ろへ入れます（無ければ先頭）。
func insertAfterPred(result []string, inResult map[string]bool, preds []string, id string) []string {
	return insertAfterPredSkipping(result, inResult, preds, id, nil)
}

// insertAfterPredSkipping は insertAfterPred と同じだが、入れる位置のすぐ後ろに skip が真のブロックが続けば、
// それらを飛び越えてから入れます。
func insertAfterPredSkipping(result []string, inResult map[string]bool, preds []string, id string, skip func(string) bool) []string {
	at := 0 // 入れる位置（この添字の前へ）
	found := false
	for j := len(preds) - 1; j >= 0 && !found; j-- {
		if !inResult[preds[j]] {
			continue
		}
		for k, r := range result {
			if r == preds[j] {
				at = k + 1
				found = true
				break
			}
		}
	}
	if skip != nil {
		for at < len(result) && skip(result[at]) {
			at++
		}
	}
	out := append([]string{}, result[:at]...)
	out = append(out, id)
	return append(out, result[at:]...)
}

// clientReordered は、画面が並べ替えたか（元の並びと画面の並びで、両方にあるブロックの順が違うか）を返します。
func clientReordered(baseOrder, order []string) bool {
	inBase := map[string]bool{}
	for _, id := range baseOrder {
		inBase[id] = true
	}
	inOrder := map[string]bool{}
	for _, id := range order {
		inOrder[id] = true
	}
	var a, b []string
	for _, id := range baseOrder {
		if inOrder[id] {
			a = append(a, id)
		}
	}
	for _, id := range order {
		if inBase[id] {
			b = append(b, id)
		}
	}
	return !sameOrder(a, b)
}

func sameOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func idsOf(blocks []storedBlock) []string {
	ids := make([]string, len(blocks))
	for i, b := range blocks {
		ids[i] = b.ID
	}
	return ids
}

// storedFormOf は1ブロックの書き出しを正本の形へ揃えます——サニタイズし、最初の要素だけを取り、ID を id にする。
func storedFormOf(s, id string) (storedBlock, bool) {
	blocks, err := splitStoredBlocks(Sanitize(s))
	if err != nil || len(blocks) == 0 {
		return storedBlock{}, false
	}
	b := blocks[0]
	if b.ID == id {
		return b, true
	}
	nodes, err := htmldoc.ParseFragment(b.HTML)
	if err != nil {
		return storedBlock{}, false
	}
	for _, n := range nodes {
		if n.Type != html.ElementNode {
			continue
		}
		setAttr(n, htmldoc.BlockIDAttr, id)
		h := renderNode(n)
		return storedBlock{ID: id, HTML: h, Hash: blockHash(h)}, true
	}
	return storedBlock{}, false
}

// syncLockedResult は、/api/sync の錠の中の仕事の結果です。
type syncLockedResult struct {
	current   []storedBlock // 合わせる前の本文のブロック
	plan      syncPlan
	finalBody string // 合わせたあとの本文（書いたときはサニタイズと名札のあと）
	updatedAt string
	written   bool
}

// syncLocked は /api/sync の、ページごとの書き込みの錠の中の仕事です（読む → 合わせる → 書く → 版 → 索引）。
// 錠は defer で外します——2026-10-20 まではここが SyncAPIHandler の中にあって、早く戻る5か所で手で外していた
// （戻る道を足したときに外し忘れると、そのページへの保存がずっと止まる）。断ったときは応答を書いて ok=false。
func syncLocked(w http.ResponseWriter, id string, user *auth.User, username string, req SyncRequest,
	mine map[string]storedBlock, sanitized map[string]bool) (res syncLockedResult, ok bool) {
	defer LockPageBody(id)()
	original, err := os.ReadFile(page.BodyPath(id))
	if err != nil {
		http.Error(w, "本文を読み込めませんでした", http.StatusNotFound)
		return res, false
	}
	current, err := splitStoredBlocks(string(original))
	if err != nil {
		http.Error(w, "本文を読めませんでした", http.StatusInternalServerError)
		return res, false
	}
	// ID の無いブロックへは中身から決まる ID を振る——振っただけでは書かない（block_sync.go の ensureStoredBlockIDs）。
	ensureStoredBlockIDs(current)
	plan := planSync(current, req, mine, sanitized)
	res = syncLockedResult{current: current, plan: plan, finalBody: joinStoredBlocks(plan.final)}
	if plan.dirty {
		res.finalBody = FillFileViewNames(user, Sanitize(res.finalBody))
		// writeBody が「トップ直下の置き場の題は1枚だけ」も見る（断れば 409 を書いて ok=false）。
		ua, ok := writeBody(w, id, res.finalBody)
		if !ok {
			return res, false
		}
		res.updatedAt = ua
		if err := RecordVersion(id, username, res.finalBody, false); err != nil {
			log.Printf("版の記録に失敗しました page=%s: %v", id, err)
		} else if req.Checkpoint {
			// 同じ中身で force を通すと、いまの版を閉じる（新しい版は作らない——RecordVersion）。
			if err := RecordVersion(id, username, res.finalBody, true); err != nil {
				log.Printf("版の区切りに失敗しました page=%s: %v", id, err)
			}
		}
		if !syncBody(w, id, res.finalBody) {
			return res, false
		}
		noteBlockWriters(id, username, string(original), res.finalBody)
		res.written = true
	} else {
		if meta, ok := page.ReadSidecar(id); ok {
			res.updatedAt = meta.UpdatedAt
		}
		if req.Checkpoint {
			if err := RecordVersion(id, username, string(original), true); err != nil {
				log.Printf("版の区切りに失敗しました page=%s: %v", id, err)
			}
		}
	}
	return res, true
}

// SyncAPIHandler は POST /api/sync です。
func SyncAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SyncRequest
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	id, idInt, ok := normalizedPageID(w, req.PageID)
	if !ok {
		return
	}
	// 書ける形で使う口なので、変えないとき（読むだけ）も write を要る——書ける人だけが書ける形に入る。
	if !page.RequirePageWrite(w, r, id) {
		return
	}
	user := auth.CurrentUser(r)
	username := auth.UsernameOf(r)

	// 変えたブロックを正本の形へ（錠の外——サニタイズは重い）。
	mine := map[string]storedBlock{}
	sanitized := map[string]bool{}
	var rawChanged, safeChanged string
	for _, c := range req.Changes {
		safe, changed := SanitizeReport(c.HTML)
		b, ok := storedFormOf(safe, c.ID)
		if !ok {
			continue
		}
		mine[c.ID] = b
		sanitized[c.ID] = changed
		rawChanged += c.HTML
		safeChanged += b.HTML
	}

	locked, ok := syncLocked(w, id, user, username, req, mine, sanitized)
	if !ok {
		return
	}
	current, plan, finalBody, updatedAt, written := locked.current, locked.plan, locked.finalBody, locked.updatedAt, locked.written

	if written {
		auth.AuditRequest(r, "save", id)
		notifyPageUpdatedFrom(id, username, req.Tab, updatedAt)
	}

	// 仕上がりの指紋は書いた本文から取り直す（サニタイズと名札で字面が変わりうる）。
	finalBlocks, _ := splitStoredBlocks(finalBody)
	if !written {
		finalBlocks = current
	}
	baseH := map[string]string{}
	for _, b := range req.Base {
		baseH[b.ID] = b.H
	}
	changedIDs := map[string]bool{}
	for _, c := range req.Changes {
		changedIDs[c.ID] = true
	}
	conflictIDs := map[string]bool{}
	for _, c := range plan.conflicts {
		conflictIDs[c.ID] = true
	}
	out := make([]SyncBlockOut, 0, len(finalBlocks))
	needRender := len(plan.conflicts) > 0
	for _, b := range finalBlocks {
		o := SyncBlockOut{ID: b.ID, H: b.Hash}
		switch {
		case req.Full:
			o.Why = "full"
		case conflictIDs[b.ID]:
			// 画面が両方を並べる（下の conflicts）。
		case plan.why[b.ID] != "":
			o.Why = plan.why[b.ID]
		case changedIDs[b.ID]:
			// 画面の書き出しのまま当たった——描き直さない。
		case baseH[b.ID] != b.Hash:
			o.Why = "other"
		}
		if o.Why != "" {
			needRender = true
		}
		out = append(out, o)
	}
	if needRender {
		rendered := map[string]string{}
		if rb, err := splitStoredBlocks(RenderComputedViews(r, idInt, finalBody)); err == nil {
			for _, b := range rb {
				rendered[b.ID] = b.HTML
			}
		}
		for i := range out {
			if out[i].Why != "" {
				out[i].HTML = rendered[out[i].ID]
			}
		}
		for i := range plan.conflicts {
			c := &plan.conflicts[i]
			if c.Kind == "changed" {
				c.HTML = rendered[c.ID]
				for _, b := range finalBlocks {
					if b.ID == c.ID {
						c.H = b.Hash
					}
				}
			}
			c.By = blockWriterOf(id, c.ID)
		}
	}

	resp := map[string]any{
		"success":           true,
		"page_id":           id,
		"updated_at":        updatedAt,
		"written":           written,
		"blocks":            out,
		"conflicts":         plan.conflicts,
		"kept":              plan.kept,
		"unknown_types":     UnknownVocabTypes(safeChanged),
		"unresolved_fields": UnresolvedVocabFields(safeChanged),
		"stripped_ids":      ShellPrefixedIDs(rawChanged),
	}
	if len(plan.kept) > 0 {
		names := map[string]string{}
		for _, k := range plan.kept {
			names[k] = blockWriterOf(id, k)
		}
		resp["kept_by"] = names
	}
	WriteJSON(w, resp)
}
