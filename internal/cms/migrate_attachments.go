package cms

// ─────────────────────────────────────────────────────────────────────────
// 添付の置き場の一度きり移行（2026-08-31 ユーザー指示「既存の添付も動かして下さい。
// エディタが編集するHTMLとJSONは、フォルダに二つのみです」）
//
// ページフォルダ直下に居る添付（旧置き場）を files/ サブフォルダへ移し、
// 本文中の配信アドレス（/data/master/<xx>/<id>/<名前>）を files/ 配下へ書き換えます。
//
// これで**ページフォルダ直下の不変条件**が立つ:
//
//	<id>.html（内容）・<id>.meta.json（属性）の2ファイルと、
//	versions/（版）・files/（添付）の2フォルダだけ。
//
// 正本と添付の完全分離により、「添付の名前が正本を上書きする」類の穴は
// 名前の検査に頼らず構造で消えます（storage.go の AttachmentsDirName を参照）。
//
// 方式は他の一度きり移行と同じ: data/master 全体をバックアップ → 移動 →
// 本文のリンク書き換え → SyncIndex 再同期。冪等です（直下に添付が無ければ何もしない）。
// 役目を終えたらこのファイルごと撤去します。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// migrateAttachmentsInDir は1ページ分の添付を files/ へ移し、動かした名前を返します。
func migrateAttachmentsInDir(pageDir, id string) (moved []string, err error) {
	entries, err := os.ReadDir(pageDir)
	if err != nil {
		return nil, err
	}
	keepFiles := map[string]bool{
		id + ".html":      true,
		id + ".meta.json": true,
	}
	for _, e := range entries {
		if e.IsDir() {
			continue // versions/ と files/ はそのまま
		}
		if keepFiles[e.Name()] {
			continue
		}
		src := filepath.Join(pageDir, e.Name())
		dstDir := filepath.Join(pageDir, page.AttachmentsDirName)
		dst := filepath.Join(dstDir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			// 同名が既に files/ に居る（移行後にアップロードされた等）。
			// 上書きすると新しい方が消えるので動かさない——直下の残置は無害
			// （配信は両方の場所を受ける）。件数には数えず報告だけに出る。
			log.Printf("添付の移行: %s/%s は files/ に同名があるため残置します", id, e.Name())
			continue
		}
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			return moved, err
		}
		if err := os.Rename(src, dst); err != nil {
			return moved, err
		}
		moved = append(moved, e.Name())
	}
	return moved, nil
}

// rewriteAttachmentURLs は本文中の旧置き場のアドレスを files/ 配下へ書き換えます。
// 日本語ファイル名はエディタ経由なら生のUTF-8で書かれるが、手書き・外部ツール由来の
// パーセント符号化にも備えて両方の形を置き換える。
func rewriteAttachmentURLs(body, pageID string, names []string) string {
	oldBase := "/data/master/" + pageID[:2] + "/" + pageID + "/"
	newBase := oldBase + page.AttachmentsDirName + "/"
	for _, name := range names {
		body = strings.ReplaceAll(body, oldBase+name, newBase+name)
		if esc := url.PathEscape(name); esc != name {
			body = strings.ReplaceAll(body, oldBase+esc, newBase+esc)
		}
	}
	return body
}

// MigrateAttachments は data/master の全ページの添付を files/ へ移します。
func MigrateAttachments() (movedTotal, pages int, backupDir string, err error) {
	backupDir = filepath.Join("data", "attach-migrate-backup-"+time.Now().Format("20060102-150405"))
	if err = copyDir(page.MasterDir, backupDir); err != nil {
		return 0, 0, "", err
	}

	// 収集と処理を分ける——Walk の最中にファイルを動かすと、Walk が「さっき見た
	// はずのファイル」を踏んで消えている、という形で自爆する（実測）。
	type target struct{ htmlPath, id string }
	var targets []target
	err = filepath.Walk(page.MasterDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".html") {
			return walkErr
		}
		id := strings.TrimSuffix(info.Name(), ".html")
		// versions/ の中の .html.gz などは来ない（.html で終わらない）が、
		// 念のためページフォルダ直下の本文だけを対象にする。
		if filepath.Base(filepath.Dir(path)) != id {
			return nil
		}
		targets = append(targets, target{path, id})
		return nil
	})
	if err != nil {
		return 0, 0, backupDir, err
	}

	for _, tg := range targets {
		moved, err := migrateAttachmentsInDir(filepath.Dir(tg.htmlPath), tg.id)
		if err != nil {
			return movedTotal, pages, backupDir, err
		}
		if len(moved) == 0 {
			continue
		}
		content, err := os.ReadFile(tg.htmlPath)
		if err != nil {
			return movedTotal, pages, backupDir, err
		}
		out := rewriteAttachmentURLs(string(content), tg.id, moved)
		if out != string(content) {
			if err := page.WriteFileAtomic(tg.htmlPath, []byte(out), 0644); err != nil {
				return movedTotal, pages, backupDir, err
			}
			if err := SyncIndex(tg.id, out); err != nil {
				return movedTotal, pages, backupDir, err
			}
		}
		movedTotal += len(moved)
		pages++
	}
	return movedTotal, pages, backupDir, nil
}

// MigrateAttachmentsAPIHandler は POST /api/migrate-attachments（admin のみ）です。
func MigrateAttachmentsAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !page.RequireAdmin(w, r) {
		return
	}
	moved, pages, backupDir, err := MigrateAttachments()
	if err != nil {
		http.Error(w, "移行エラー: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if u := auth.CurrentUser(r); u != nil {
		auth.Audit(u.Username, "migrate-attachments",
			strconv.Itoa(moved)+"ファイル / "+strconv.Itoa(pages)+"ページ")
	}
	log.Printf("添付を files/ へ移行しました: %dファイル / %dページ バックアップ: %s", moved, pages, backupDir)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "moved": moved, "pages": pages, "backup": backupDir,
	})
}
