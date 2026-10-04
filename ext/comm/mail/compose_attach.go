package mail

// ─────────────────────────────────────────────────────────────────────────
// パソコンのファイルを添える（2026-10-04）
//
// 利用者:「新しいメールを作成するときに、ローカルコンピュータから添付したいです。返信などの時も同様です」。
//
// それまで添えられたのは **w-cms の中にあるファイルだけ**でした（attach.go——「手元のディスクから選び直す必要が
// ありません」）。w-cms に無いファイルを送るには、どこかのページへ置いてから ID を写す回り道が要りました。
//
// **置き場は下書きのページです**——送る欄でファイルを選ぶと、画面は（まだなら）下書きを保存してから
// `POST /api/mail/attach` でその下書きへ添付として置き、添付の候補に印つきで並べます。下書きを開き直しても残ります。
//
// ⚠ **送ると下書きはごみ箱へ行きます**（reply.go）。送信の控えは添付を**元のページのファイルへのリンク**で
// 残すので（fillMailBody）、そのままでは控えのリンクがごみ箱のページを指します。だから送れたら、**下書きに置いた
// ファイルを控えのページへ写し**、控えのリンクを写した先へ付け替えます（`moveDraftAttachments`）。写せなかったら
// 下書きは片付けずに残します（ファイルの居場所を失わない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// draftAttachSource は、送る欄から下書きへ置いたファイルの由来（目録 `files/meta.json` の source）です。
const draftAttachSource = "mail-compose"

// MailAttachAPIHandler は POST /api/mail/attach です（multipart: draft_id・file）。
// 応答は {success, page_id, file, name}——送る欄の添付の候補と同じ組（AttachRef）。
//
// 守りは添付の口と同じ: 本文の上限（max_upload_mib）・下書きを書けること（下書きのページだけ・エディタで開いて
// いれば断る——checkDraftWritable）・送れる種類だけ（sendableExts）・名前の検査（SafeAttachmentName）・
// 中身の検査（PDF の先頭・画像の種別と EXIF——GuardUploadContent）。
func MailAttachAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	limit := cms.MaxUploadBytes()
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	draftID, ok := checkDraftWritable(w, r, user, r.FormValue("draft_id"))
	if !ok {
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, "ファイルを受け取れませんでした（1つ "+strconv.FormatInt(limit>>20, 10)+"MiB まで）")
		return
	}
	defer file.Close()
	name, err := cms.SafeAttachmentName(draftID, header.Filename, sendableExts(),
		"この種類のファイルはメールに添えられません（"+header.Filename+"）")
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}
	content, err := io.ReadAll(file)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, "ファイルを読めませんでした（1つ "+strconv.FormatInt(limit>>20, 10)+"MiB まで）")
		return
	}
	if len(content) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "中身が空のファイルです（"+name+"）")
		return
	}
	if content, err = cms.GuardUploadContent(name, content); err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}
	_, stored, err := cms.SaveAttachmentFrom(draftID, user.Username, name, draftAttachSource, content)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "ファイルを置けませんでした: "+err.Error())
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": draftID, "file": stored, "name": name})
}

// usesPage は、添付のどれかがページ pageID のファイルかを返します。
func usesPage(refs []AttachRef, pageID string) bool {
	for _, ref := range refs {
		if pid, ok := page.NormalizeID(strings.TrimSpace(ref.PageID)); ok && pid == pageID {
			return true
		}
	}
	return false
}

// moveDraftAttachments は、送った1通の添付のうち下書きに置いたものを控えのページへ写し、控えのリンクを付け替えます。
// 写したファイルの数を返します（下書きのファイルが無ければ 0・何もしない）。
func moveDraftAttachments(user *auth.User, draftID, recordID string, refs []AttachRef) (int, error) {
	repl := map[string]string{} // 控えの中の古い住所 → 新しい住所
	for _, ref := range refs {
		pid, ok := page.NormalizeID(strings.TrimSpace(ref.PageID))
		if !ok || pid != draftID {
			continue
		}
		path, ok := page.AttachmentPath(draftID, ref.File)
		if !ok {
			return 0, errors.New("下書きのファイルが見つかりません: " + ref.File)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return 0, errors.New("下書きのファイルを読めません: " + ref.File)
		}
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			name = cms.AttachmentDisplayName(draftID, ref.File)
		}
		_, stored, err := cms.SaveAttachmentFrom(recordID, user.Username, sendName(name, ref.File),
			draftAttachSource+":"+draftID+"/"+ref.File, content)
		if err != nil {
			return 0, err
		}
		repl[page.AttachmentURLFor(draftID, ref.File)] = page.AttachmentURLFor(recordID, stored)
	}
	if len(repl) == 0 {
		return 0, nil
	}
	err := cms.RewriteBody(recordID, user.Username, func(body string) string {
		for from, to := range repl {
			body = strings.ReplaceAll(body, `href="`+from+`"`, `href="`+to+`"`)
		}
		return body
	})
	return len(repl), err
}
