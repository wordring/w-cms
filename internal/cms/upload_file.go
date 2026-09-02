package cms

// ─────────────────────────────────────────────────────────────────────────
// 汎用の添付アップロード（2026-08-31 ユーザー決定3件を実装）
//
//   - 「.eml の扱いは添付から始めましょう」
//   - 「サイズ上限32MiBは設定で変えられるように」（settings.go の max_upload_mib）
//   - 「添付はドラッグアンドドロップに耐えられた方が良い」（本文へのドロップが入口）
//
// 受ける拡張子は設定 `attachment_extensions`（既定＝ワンノート実データの15種
// −.pdf の14種。【考察】ワンノート移行.md §3-4）。**中身は検査しません**——
// CAD・Office のマジックナンバー検証は現実的でなく、安全の本体は配信側にある:
// DataFileHandler が未知の種別を `Content-Disposition: attachment`＋nosniff で
// 返すので、ブラウザは解釈しない（SVG で確立した「入口は網・配信が本体」の一般化）。
//
// **画像と .pdf はこの口では受けません**——専用の口（中身検査・EXIF除去つき）を
// 迂回させないため。エディタ側も種類で振り分ける。
//
// 保存先は files/ サブフォルダ（正本と同居しない——構造で塞ぐ。storage.go）。
// 名前の検査は safeAttachmentName の1箇所を全アップロード口が共有する。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// UploadFileHandler は POST /api/upload-file（汎用の添付）です。
func UploadFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := MaxUploadBytes()
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	pageID := r.FormValue("page_id")
	if pageID == "" {
		http.Error(w, "page_id is required", http.StatusBadRequest)
		return
	}
	pageID, ok := page.NormalizeID(pageID)
	if !ok {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}

	// **受信箱への到着は取り込み係へ回覧する**（intake.go・2026-09-01）。
	// 受信箱の本文は変更しない（子ページが生まれるだけ）ので編集ロックは要らない。
	// 取り込み係が居ない拡張子は、通常の添付として下の経路へ流れる。
	if inboxID, ok := InboxPageID(); ok && inboxID == pageID {
		if served := serveIntake(w, r, inboxID); served {
			return
		}
	}

	// 添付は同名を無条件で上書きし、リビジョンも無い——本文編集と同じ編集ロックで直列化。
	if !editlock.RequireEditLock(w, r, pageID) {
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "ファイルを受け取れませんでした（サイズ上限は "+
			strconv.FormatInt(limit>>20, 10)+"MiB です）", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	// 専用の口があるものは迂回させない（画像＝マジックナンバー検証・EXIF除去、
	// PDF＝%PDF- 検証）。
	if ext == ".pdf" || allowedImageExts[ext] {
		http.Error(w, "この種類は専用のアップロード口を使ってください（画像・PDF）", http.StatusBadRequest)
		return
	}
	fileName, err := safeAttachmentName(pageID, header.Filename, GenericAttachmentExts(),
		"この拡張子は添付として受け付けていません（許可リストは data/settings.json の attachment_extensions）")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "File read error", http.StatusInternalServerError)
		return
	}

	attachDir := page.AttachmentDir(pageID)
	os.MkdirAll(attachDir, 0755)
	// 保存名はサーバーが生成する（元の名前はURLに出さない。表示は本文のリンク文字が担う）。
	// 生成IDはリンクブロックの data-id と一致させる（storage.go の3役）。
	attachID := page.GeneratedAttachmentID(pageID, strings.ToLower(filepath.Ext(fileName)))
	fileName = attachID + strings.ToLower(filepath.Ext(fileName))
	savePath := filepath.Join(attachDir, fileName)

	overwrote := false
	if _, err := os.Stat(savePath); err == nil {
		overwrote = true
	}
	if err := page.WriteFileAtomic(savePath, content, 0644); err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	action := "attach"
	if overwrote {
		action = "attach.overwrite"
	}
	if u := auth.CurrentUser(r); u != nil {
		auth.Audit(u.Username, action, pageID+"/"+fileName)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success":   true,
		"file_name": fileName,
		"id":        attachID, // リンクブロックの data-id に使う（ファイル名と一致）
		"href":      page.AttachmentURLFor(pageID, fileName),
	})
}

// serveIntake は受信箱へのアップロードを取り込み係に回します。
// 担当が居なければ false（通常の添付経路へ戻す）。
func serveIntake(w http.ResponseWriter, r *http.Request, inboxID string) bool {
	file, header, err := r.FormFile("file")
	if err != nil {
		return false
	}
	defer file.Close()
	h := intakeHandlerFor(strings.ToLower(filepath.Ext(header.Filename)))
	if h == nil {
		return false // 担当なし＝ただの添付
	}
	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "File read error", http.StatusInternalServerError)
		return true
	}
	uploader := "system"
	if u := auth.CurrentUser(r); u != nil {
		uploader = u.Username
	}
	// 重複検知は**取り込み係を呼ぶ前**に行う（【考察】通信記録処理.md §8）。
	// 同じメールの再ドロップは普通に起きる（送り直し・取りこぼしの確認）が、
	// 黙って2枚できるとどちらが正かを人が見分けられない——しかも参照タグ
	// `受信元` の指す先が2つに割れる。**作ってから消す**のではなく、
	// 作らずに既存を指すのが正しい（可逆性は §2.7 の決定）。
	if f, ok := h.(SourceRefFinder); ok {
		if name, value, found := f.SourceRef(header.Filename, content); found {
			if existing, dup := ExistingIntakePage(name, value); dup {
				auth.Audit(uploader, "intake.duplicate", existing+" ("+name+"="+value+")")
				w.Header().Set("Content-Type", "application/json")
				resp := map[string]any{
					"success": true, "intake": true, "duplicate": true,
					"title": header.Filename,
				}
				// 読めない相手へ存在を教えない（匿名の404統一と同じ規律）。
				// 取り込み済みという事実だけは返す——でないと「なぜ入らないのか」が
				// 分からなくなる。
				if n, err := strconv.Atoi(existing); err == nil &&
					page.CanView(auth.CurrentUser(r), n) {
					resp["page_id"] = existing
				}
				json.NewEncoder(w).Encode(resp)
				return true
			}
		}
	}

	ctx := &IntakeContext{InboxID: inboxID, Uploader: uploader}
	pageID, title, err := h.OnFile(ctx, header.Filename, content)
	if err != nil {
		http.Error(w, "取り込めませんでした: "+err.Error(), http.StatusBadRequest)
		return true
	}
	// 添付PDFの解釈（発注書なら受注ページ生成）はここでは**しない**——
	// 「自動ではなくボタンのclickなどで解析が始まると良い」（2026-09-01 ユーザー決定・
	// §3 人間ゲート型）。記録ページの📎に出る「🤖 解析」ボタン（analyze_pdf.go）が担う。
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "intake": true, "page_id": pageID, "title": title,
	})
	return true
}
