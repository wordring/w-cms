package mail

// ─────────────────────────────────────────────────────────────────────────
// 返信に添付を付ける（2026-09-05）
//
// **付けるのは w-cms の中にある添付だけ**です。図面PDFを業者へ回す、見積書を
// 顧客へ返す——実際に起きるのはこの形で、**手元のディスクから選び直す必要が
// ありません**（同じファイルが2つに増えるのも防げます）。
//
// **読める相手にしか付けさせません。** 添付は「ページに属するもの」なので、
// そのページを読めることが条件です（`page.CanView`）。ここを外すと、読めない
// ページの図面を自分宛てに送って持ち出せます。
//
// **上限は合計で見ます。** SMTP なら M365 で約35MB ですが、途中で切れると
// 「送ったつもりで届いていない」になるので、**送る前に断ります**。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// AttachRef は「どのページの、どのファイルか」です。
type AttachRef struct {
	PageID string `json:"page_id"`
	File   string `json:"file"` // 保存名（<生成ID>.<拡張子>）
	Name   string `json:"name"` // 送るときの名前（元のファイル名。空なら保存名）
}

// maxTotalAttachBytes は添付の合計上限です。M365 の SMTP は約35MB ですが、
// MIME の base64 で約1.37倍に膨らむので、**素の合計を25MBで止めます**。
const maxTotalAttachBytes = 25 << 20

// collectAttachments は指定された添付を読み、送信用に組み立てます。
func collectAttachments(user *auth.User, refs []AttachRef) ([]cms.MailAttachment, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	allowed := sendableExts()
	var out []cms.MailAttachment
	var total int64

	for _, ref := range refs {
		pageID, ok := page.NormalizeID(strings.TrimSpace(ref.PageID))
		if !ok {
			return nil, errors.New("添付のページIDが不正です")
		}
		idInt, err := strconv.Atoi(pageID)
		if err != nil || !page.CanView(user, idInt) {
			// **読めないページの添付は「無い」と同じ顔**（匿名の404統一と同じ規律）。
			return nil, errors.New("添付が見つかりません")
		}
		fileName, err := cms.SafeAttachmentName(pageID, ref.File, allowed,
			"この種類のファイルは添付できません")
		if err != nil {
			return nil, err
		}
		path, ok := page.AttachmentPath(pageID, fileName)
		if !ok {
			return nil, errors.New("添付が見つかりません: " + fileName)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, errors.New("添付を読めません: " + fileName)
		}
		total += info.Size()
		if total > maxTotalAttachBytes {
			return nil, errors.New("添付が大きすぎます（合計25MBまで）")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.New("添付を読めません: " + fileName)
		}
		out = append(out, cms.MailAttachment{
			Name:     sendName(ref.Name, fileName),
			MIMEType: mimeTypeOf(fileName),
			Content:  content,
		})
	}
	return out, nil
}

// sendName は送るときのファイル名を決めます。
//
// **元の名前を優先します**——保存名は生成IDなので、受け取った相手には
// `a7k2.pdf` としか見えません。名前はMIMEヘッダへ入るだけなので、
// パス要素と制御文字だけ落とせば足ります。
func sendName(want, fallback string) string {
	name := strings.TrimSpace(want)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." {
		return fallback
	}
	return name
}

// mimeTypeOf は拡張子から種別を引きます（分からなければ octet-stream）。
func mimeTypeOf(fileName string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName))); t != "" {
		return t
	}
	return "application/octet-stream"
}

// sendableExts は添付として送れる拡張子です。
//
// **保存できるものは送れます**——設定の汎用拡張子に、専用の口を持つ種類
// （PDF・画像）と受信原本（`.eml`）を足したもの。ここを絞る理由がありません
// （送るのは人の意図した操作で、中身は既に w-cms の中にあります）。
func sendableExts() map[string]bool {
	m := map[string]bool{
		".pdf": true, ".png": true, ".jpg": true, ".jpeg": true,
		".webp": true, ".gif": true, ".svg": true, ".eml": true,
	}
	for ext := range cms.GenericAttachmentExts() {
		m[ext] = true
	}
	return m
}
