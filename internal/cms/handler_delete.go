package cms

// ─────────────────────────────────────────────────────────────────────────
// ページ削除
//
// **物理削除ではなくゴミ箱（data/trash）への移動**です。削除を取り消せること自体が
// 要件だからで、経緯は docs/【考察】通信記録処理.md §2.7④「常に柔軟性」。
// DB再構築（RebuildDatabase）は data/master だけを走査するので、移すだけで索引からも消えます。
//
// 守りは3つ:
//   - トップページは削除できない（親を持てない唯一のページで、消すと復旧導線が無くなる）
//   - 子ページを持つページは削除できない（子を道連れにするより、先に移すか消すよう促す）
//   - 編集ロックが要る（他者が編集中のページを消せない。他の変更操作と同じ直列化）
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// TopPageID は削除できないトップページのIDです。
const TopPageID = "000000"

// DeletePageAPIHandler はページをゴミ箱へ移し、索引から取り除きます。
func DeletePageAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, okID := page.NormalizeID(r.URL.Query().Get("id"))
	if !okID {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	if id == TopPageID {
		http.Error(w, "トップページは削除できません", http.StatusBadRequest)
		return
	}
	if !page.RequirePageWrite(w, r, id) {
		return
	}
	// エディタ内の変更操作と同じ編集ロックで直列化する（他者保持中なら409）。
	if !editlock.RequireEditLock(w, r, id) {
		return
	}
	pageID, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}

	// 子ページがあるなら消さない。再帰削除は「取り消せる範囲」を一気に広げるので採らない。
	var children int
	if err := database.DB.QueryRow("SELECT COUNT(*) FROM pages WHERE parent_id = ?", pageID).Scan(&children); err != nil {
		http.Error(w, "子ページの確認に失敗しました: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if children > 0 {
		http.Error(w, fmt.Sprintf("子ページが %d 件あります。先に移動するか削除してください。", children),
			http.StatusConflict)
		return
	}

	trashPath, err := moveToTrash(id)
	if err != nil {
		http.Error(w, "ゴミ箱への移動に失敗しました: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := PurgePageIndex(id); err != nil {
		// 正本は既にゴミ箱にあるので、索引だけが残った状態。DB再構築で解消できる。
		http.Error(w, "索引の削除に失敗しました（管理コンソールのDB再構築で解消できます）: "+err.Error(),
			http.StatusInternalServerError)
		return
	}

	editlock.Locks.ForceRelease(pageID) // 消えたページのロックは残さない
	if u := auth.CurrentUser(r); u != nil {
		auth.Audit(u.Username, "delete-page", id+" -> "+trashPath)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"page_id": id,
		// 復旧に使えるよう移動先を返す（UIは表示しないが、監査ログと突き合わせられる）。
		"trash_path": trashPath,
	})
}

// moveToTrash はページのフォルダを data/trash へ移し、移動先パスを返します。
// 同じIDが既にゴミ箱にある（削除→復元→再削除）場合は連番を付けて上書きを避けます。
func moveToTrash(id string) (string, error) {
	src := page.GetPageDir(id)
	if _, err := os.Stat(src); os.IsNotExist(err) {
		// フォルダが失われている（手動削除・過去の中断など）。ここで止めると
		// DBに残ったページを画面から永久に消せなくなるので、索引の掃除だけ進める。
		return "", nil
	} else if err != nil {
		return "", err
	}
	dst := page.GetTrashDir(id)
	for i := 2; ; i++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = page.GetTrashDir(id) + "-" + strconv.Itoa(i)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return "", err
	}
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// DeletePageToTrash はページ削除の**芯**です（ゴミ箱へ移動・索引の掃除・ロック解放）。
//
// HTTPの口（DeletePageAPIHandler）と、部品ページの整理（ext/sheetmetal/filing.go）が
// 共有します——整理は改定図面を既存ページへ合流させたあと、空になった仮のページを
// 片付ける必要があり、**作法を2箇所に持つと必ず片方が古くなる**ため。
//
// **物理削除ではありません**（data/trash へ移すだけ）。復元UIは未実装なので、
// 戻すときは手で data/master へ返してDB再構築します。
// 権限・子ページの確認は呼ぶ側の責任です。
func DeletePageToTrash(id string) (string, error) {
	pageID, err := strconv.Atoi(id)
	if err != nil {
		return "", errors.New("ページIDが不正です")
	}
	trashPath, err := moveToTrash(id)
	if err != nil {
		return "", err
	}
	if err := PurgePageIndex(id); err != nil {
		// 正本は既にゴミ箱にあるので、索引だけが残った状態。DB再構築で解消できる。
		return trashPath, err
	}
	editlock.Locks.ForceRelease(pageID) // 消えたページのロックは残さない
	return trashPath, nil
}
