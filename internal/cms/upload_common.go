package cms

// ─────────────────────────────────────────────────────────────────────────
// 添付の口3本（PDF・画像・汎用）に共通の入口（2026-09-21）
//
// **同じ入口の十数行が3本に写されていました**——メソッド → 本文上限 → ページID →
// write 権限 →（受け口）→ 編集ロック → フォームのファイル → 名前の検査 → 読み込み。
// 保存の作法（attachment_save.go）は 09-14 に寄せてありましたが、入口はそのままでした。
//
// 中身の検査（PDF の先頭・画像のマジックナンバー）と**応答の組み立て**は、3本とも
// 違うので呼ぶ側に残します（PDFは `src`＋`href`、汎用は `href`、画像は `src`＋`kind`）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"io"
	"net/http"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// uploadIntake は入口を通った添付1件です。
type uploadIntake struct {
	pageID   string // ゼロ詰め6桁
	fileName string // 名前の検査を通った、届いたときの名前（保存名はサーバーが採番する）
	content  []byte
	username string
}

// openUpload は添付の口の共通の入口です。断ったときは応答を書き終えていて ok=false。
//
//   - 名前の検査（safeName）は読み込みより**先**——種類が許可されないなら読み込むまでもない。
//   - ⚠ **先に引き受ける口（受け口）は 2026-09-30 に無くしました**——使い手は通信箱の取り込み係だけで、
//     通信箱へ落とした `.eml` を記録にしていました（利用者:「通信箱ページのファイルをドロップすると子ページが
//     作られる機能はもはや必要ないでしょう」）。どのページへ上げても、添付になるだけです。
//   - 添付は同名を無条件で上書きし、リビジョンもゴミ箱も無い（＝復元できない）ので、
//     本文編集と同じ編集ロックで直列化します（editlock/handler.go の宣言どおり）。
func openUpload(w http.ResponseWriter, r *http.Request, formField string,
	safeName func(pageID, raw string) (string, error)) (up uploadIntake, ok bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return up, false
	}
	// フォームを読む前に本文サイズを制限する（FormValue が内部でパースするため）。
	limit := MaxUploadBytes()
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	// 保存先のパスに使う前にゼロ詰め6桁へ正規化する（page.NormalizeID 参照）。
	pageID, _, ok := normalizedPageID(w, r.FormValue("page_id"))
	if !ok {
		return up, false
	}
	// 添付の追加はページ内容の変更なので write 権限を要求する。
	if !page.RequirePageWrite(w, r, pageID) {
		return up, false
	}
	if !editlock.RequireEditLock(w, r, pageID) {
		return up, false
	}

	file, header, err := r.FormFile(formField)
	if err != nil {
		http.Error(w, "ファイルを受け取れませんでした（サイズ上限は "+
			strconv.FormatInt(limit>>20, 10)+"MiB です）", http.StatusBadRequest)
		return up, false
	}
	defer file.Close()

	fileName, err := safeName(pageID, header.Filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return up, false
	}
	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "File read error", http.StatusInternalServerError)
		return up, false
	}
	return uploadIntake{
		pageID: pageID, fileName: fileName, content: content, username: auth.UsernameOf(r),
	}, true
}
