package cms

// ─────────────────────────────────────────────────────────────────────────
// 添付を保存する作法（2026-09-14）
//
// **同じ12行が3本に写されていました**——汎用の添付・画像・PDF。
// 「`AttachmentDir` → `MkdirAll` → `GeneratedAttachmentID` → `WriteFileAtomic` →
// 上書き判定 → `Audit`」の並びで、**1本だけ直す事故**が起きうる形でした。
//
// ここが引き受けるのは**保存の作法だけ**です。中身の検査（PDFの先頭が `%PDF-` か、
// 画像のマジックナンバー、拡張子の許可リスト）と、**応答の組み立て**は呼ぶ側に
// 残します——3本とも返すJSONの鍵が違うためです（PDFは `src`＋`href`、
// 汎用は `href`、画像は `src`＋`kind`）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"os"
	"path/filepath"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// SaveAttachment は添付を `files/` へ保存し、生成した識別子とファイル名を返します。
//
// **保存名はサーバーが生成します**——元の名前はURLに出しません（表示は本文の
// リンク文字が担う）。生成IDは**リンクブロックの `data-id` と一致**させます
// （ファイル名＝URL＝`data-id` の3役・storage.go）。
//
// **監査は上書きかどうかを分けます**（要件定義書 §2.3）。添付には版もゴミ箱も
// 無く、上書きは復元できないので、「増えた」のか「消えた」のかを記録で
// 区別できないと後から辿れません。
//
// 呼ぶ側の責任: 認可・編集ロック・**中身の検査**（拡張子の許可／マジックナンバー）を
// 済ませてから呼ぶこと。ここは書くだけです。
//
// 由来は `upload`（人が落とした）。取り込みなど由来が違う口は `SaveAttachmentFrom` を使う。
func SaveAttachment(pageID, username, origName string, content []byte) (attachID, fileName string, err error) {
	return SaveAttachmentFrom(pageID, username, origName, AttachmentSourceUpload, content)
}

// SaveAttachmentFrom は由来（source）を指定して保存します。
//
// 保存したら **`files/meta.json` に事実を書き留めます**（元の名前・保存日時・利用者・大きさ・
// ハッシュ・由来。attachment_meta.go）。⚠ 目録が書けなくても保存は失敗にしません——
// 失敗を返すと利用者が押し直して同じファイルが2つになるので、監査に残して続けます。
func SaveAttachmentFrom(pageID, username, origName, source string, content []byte) (attachID, fileName string, err error) {
	attachDir := page.AttachmentDir(pageID)
	if err := os.MkdirAll(attachDir, 0755); err != nil {
		return "", "", err
	}
	ext := strings.ToLower(filepath.Ext(origName))
	attachID = page.GeneratedAttachmentID(pageID, ext)
	fileName = attachID + ext
	savePath := filepath.Join(attachDir, fileName)

	// **上書きかどうかは書く前にしか分かりません。**
	overwrote := false
	if _, statErr := os.Stat(savePath); statErr == nil {
		overwrote = true
	}
	if err := page.WriteFileAtomic(savePath, content, 0644); err != nil {
		return "", "", err
	}

	action := "attach"
	if overwrote {
		action = "attach.overwrite"
	}
	auth.Audit(username, action, pageID+"/"+fileName)
	// 目録へ。名前は**ファイル名だけ**（呼ぶ側がパスを渡してきても、フォルダは落とす）。
	meta := NewAttachmentMeta(filepath.Base(strings.ReplaceAll(origName, `\`, "/")), username, source, content)
	if err := RecordAttachmentMeta(pageID, fileName, meta); err != nil {
		auth.Audit(username, "attach.meta-failed", pageID+"/"+fileName+": "+err.Error())
	}
	return attachID, fileName, nil
}
