package cms

// ─────────────────────────────────────────────────────────────────────────
// 添付の上書き——書いてよいか・版を積む（2026-10-15 に webdav_write.go から移した）
//
// 添付を上書きするのは「📝 ローカル編集」（local_edit.go・cmd/w-cms-edit）です。
// 2026-09-07 に WebDAV の Ctrl+S のために作った決まりを、そのまま使っています。
// 2026-10-15 利用者:「WevDAVは続けません。コードも文書も削除します」——WebDAV の口は外し、
// 上書きの決まりだけをここへ残しました。
//
// ── 上書きするが、版を積む ──
//
// ページ本文には版があるのに（`versions/`・リバートできる）、**添付には版が
// ありません**。上書きを許すだけだと、編集ミスも「間違ったファイルを開いた」も
// 取り返せません——CADの図面でそれは重すぎます。
//
// **本文と同じ形にします**——上書きし、前の中身は `files/.versions/<名前>/` へ
// 時刻の名前で残す。別ファイル案（`_v2` `_v3`）を採らないのは、**どれが最新か
// 分からなくなる**問題を添付の側で再発させるからです（旧版を子ページへ移した理由と同じ）。
// `.` 始まりなので、素の添付配信は拒みます。
//
// ── 書ける範囲 ──
//
// **設定が決めます**（`attachment_readonly`・2026-10-15 までの名前は `webdav_readonly`）。
// ユーザー:「編集するCADファイルは弊社の物です。メール由来のものではありません」（2026-09-07）
// ——届いた添付は**届いた事実の証拠**なので書き換えさせません。ただしコアが `通信箱` を
// 名前で知ってはいけないので、**題を設定に書いてもらう**形にしました。
//
// **新しいファイルは作れません**（既存の上書きだけ）。作れるようにすると
// 拡張子の許可リストと大きさの上限を素通りするので、**別に決めることになります**。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// attachmentVersionsDir は添付の版の置き場です（`files/.versions/`）。
//
// **`.` 始まり**なので、素の添付配信に出ません。
const attachmentVersionsDir = ".versions"

// errReadOnlyArea は「ここは読み取り専用」の印です。
var errReadOnlyArea = errors.New("この範囲は読み取り専用です（設定 attachment_readonly）")

// canWriteAttachment は、そのページの添付を user が書き換えてよいかを返します。
//
// 3つ揃って初めて書けます:
//
//  1. ページへの**書き込み権限**（見えるだけでは書けない）
//  2. **読み取り専用の範囲に入っていない**（設定 `attachment_readonly`。祖先まで見る）
//  3. 添付が**既にある**（新しいファイルは作らない。呼ぶ側が確かめます）
func canWriteAttachment(user *auth.User, pageID string) error {
	idInt, err := strconv.Atoi(pageID)
	if err != nil {
		return os.ErrNotExist
	}
	if !page.GetPerms(idInt).CanWrite(user) {
		return os.ErrPermission
	}
	if inReadOnlyArea(pageID) {
		return errReadOnlyArea
	}
	return nil
}

// inReadOnlyArea は、そのページ（または祖先）が読み取り専用に指定されているかを返します。
//
// **祖先まで見ます**——`通信箱` を指定したら、その下の年フォルダも記録ページも
// まとめて読み取り専用になるべきだからです。壊れたデータで無限に辿らないよう
// 回数に上限を置きます（`isDescendantOf` と同じ用心）。
func inReadOnlyArea(pageID string) bool {
	titles := readOnlyAttachmentTitles()
	if len(titles) == 0 {
		return false
	}
	cur := pageID
	for i := 0; i < 100; i++ {
		idInt, err := strconv.Atoi(cur)
		if err != nil {
			return false
		}
		if titles[strings.TrimSpace(PageTitleByID(idInt))] {
			return true
		}
		meta, ok := page.ReadSidecar(cur)
		if !ok || meta.ParentID == "" {
			return false
		}
		cur = meta.ParentID
	}
	return false
}

// saveAttachmentVersion はいまの添付を版として `files/.versions/<名前>/` へ残します。
//
// 名前は本文の版と同じ書式（`20060102T150405Z`）です。**まだ無いファイルなら
// 何もしません**（新規作成は別の話で、いまは通しません）。
func saveAttachmentVersion(pageID, name string) error {
	src, ok := page.AttachmentPath(pageID, name)
	if !ok {
		return nil
	}
	dir := filepath.Join(page.AttachmentDir(pageID), attachmentVersionsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	dst := filepath.Join(dir, stamp+filepath.Ext(name))
	// 同じ秒に2度保存されたら、後のほうを別名にする（版を落とさない）。
	for i := 1; ; i++ {
		if _, err := os.Stat(dst); err != nil {
			break
		}
		dst = filepath.Join(dir, stamp+"-"+strconv.Itoa(i)+filepath.Ext(name))
	}
	// 控えは上書きする前の中身の唯一の写しなので、書き切ってから置く（2026-10-30。写している途中で電源が落ちると、切れた控えが本物として残った）。
	content, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return page.WriteFileAtomic(dst, content, 0o644)
}

// attachmentVersions は添付の版を新しい順に返します（いまは試験だけが使う。控えを戻す口は無く、戻すのは手作業）。
func attachmentVersions(pageID, name string) []string {
	dir := filepath.Join(page.AttachmentDir(pageID), attachmentVersionsDir, name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	// 名前が時刻なので、逆順が新しい順。
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
