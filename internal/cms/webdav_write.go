package cms

// ─────────────────────────────────────────────────────────────────────────
// WebDAV の書き込み——Ctrl+S を通す（2026-09-07）
//
// 読み取りまでは実測で通りました（`Z:` からCADが図面を開けた）。残っていたのは
// ③Ctrl+S と④保存で、その前に決めることが2つありました。**両方この形で決めます。**
//
// ── 決定1: 上書きするが、版を積む ──
//
// ページ本文には版があるのに（`versions/`・リバートできる）、**添付には版が
// ありません**。上書きを許すだけだと、編集ミスも「間違ったファイルを開いた」も
// 取り返せません——CADの図面でそれは重すぎます。
//
// **本文と同じ形にします**——上書きし、前の中身は `files/.versions/<名前>/` へ
// 時刻の名前で残す。別ファイル案（`_v2` `_v3`）を採らないのは、**どれが最新か
// 分からなくなる**問題を添付の側で再発させるからです（旧版を子ページへ移した理由と同じ）。
//
// 置き場を `.versions` にしたのは、**WebDAV の一覧に出さない**ためです
// （フォルダは Readdir が返しません。素の添付配信も `.` 始まりを拒みます）。
//
// ── 決定2: 同時編集は止めない。取り返せるようにする ──
//
// 2人が同じ図面を開いて両方が Ctrl+S すると、後の保存が前を消します。**止めません**
// ——止める仕掛け（ロック）は、CADアプリが `LOCK` を送るかどうかに懸かっていて、
// 送らないアプリでは効かないのに「守られている」と思わせるからです。
//
// 代わりに**版を積むこと自体が答え**になります: 消えた前の中身は `.versions/` に
// 在り、誰がいつ上書きしたかは監査記録（`dav.write`）に残ります。
// **`LOCK` は通します**——送ってくるアプリ（Officeなど）には本来の守りが効きます。
//
// ── 書ける範囲 ──
//
// **設定が決めます**（`webdav_readonly`）。ユーザー:「編集するCADファイルは弊社の
// 物です。メール由来のものではありません」——届いた添付は**届いた事実の証拠**なので
// 書き換えさせません。ただしコアが `通信箱` を名前で知ってはいけないので
// （2026-09-07 の層の指摘）、**題を設定に書いてもらう**形にしました。
//
// **新しいファイルは作れません**（既存の上書きだけ）。作れるようにすると
// 拡張子の許可リストと大きさの上限を素通りするので、**別に決めることになります**。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// davVersionsDir は添付の版の置き場です（`files/.versions/`）。
//
// **`.` 始まり**なので WebDAV の一覧にも素の添付配信にも出ません。
const davVersionsDir = ".versions"

// errDavReadOnlyArea は「ここは読み取り専用」の印です。
var errDavReadOnlyArea = errors.New("この範囲は読み取り専用です（設定 webdav_readonly）")

// errDavNoCreate は「新しいファイルは作れない」の印です。
var errDavNoCreate = errors.New("新しいファイルは作れません（既存の添付の上書きだけ）")

// canWriteAttachment は、そのページの添付を書き換えてよいかを返します。
//
// 3つ揃って初めて書けます:
//
//  1. ページへの**書き込み権限**（見えるだけでは書けない）
//  2. **読み取り専用の範囲に入っていない**（設定 `webdav_readonly`。祖先まで見る）
//  3. 添付が**既にある**（新しいファイルは作らない。呼ぶ側が確かめます）
func (f davFS) canWriteAttachment(pageID string) error {
	idInt, err := strconv.Atoi(pageID)
	if err != nil {
		return os.ErrNotExist
	}
	if !page.GetPerms(idInt).CanWrite(f.user) {
		return os.ErrPermission
	}
	if f.inReadOnlyArea(pageID) {
		return errDavReadOnlyArea
	}
	return nil
}

// inReadOnlyArea は、そのページ（または祖先）が読み取り専用に指定されているかを返します。
//
// **祖先まで見ます**——`通信箱` を指定したら、その下の年フォルダも記録ページも
// まとめて読み取り専用になるべきだからです。壊れたデータで無限に辿らないよう
// 回数に上限を置きます（`isDescendantOf` と同じ用心）。
func (f davFS) inReadOnlyArea(pageID string) bool {
	titles := readOnlyWebDAVTitles()
	if len(titles) == 0 {
		return false
	}
	cur := pageID
	for i := 0; i < 100; i++ {
		idInt, err := strconv.Atoi(cur)
		if err != nil {
			return false
		}
		if titles[strings.TrimSpace(pageTitleByID(idInt))] {
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

// pageTitleByID は索引からページの題を引きます（引けなければ空）。
func pageTitleByID(idInt int) string {
	var t string
	database.DB.QueryRow(`SELECT COALESCE(title, '') FROM pages WHERE id = ?`, idInt).Scan(&t)
	return t
}

// davWriteFile は上書き中の添付です。
//
// **書き終わってから差し替えます**（Close で rename）。途中で切れたファイルを正本の
// 位置に置かないためで、本文の保存（原子的書き込み）と同じ考えです。
type davWriteFile struct {
	*os.File
	tmpPath  string
	realPath string
	pageID   string
	name     string
	user     string
	done     bool
}

func (w *davWriteFile) Close() error {
	if w.done {
		return nil
	}
	w.done = true
	if err := w.File.Close(); err != nil {
		os.Remove(w.tmpPath)
		return err
	}
	// 1. いまの中身を版として残す（**差し替える前に**。ここで失敗したら上書きしない）。
	if err := saveAttachmentVersion(w.pageID, w.name); err != nil {
		os.Remove(w.tmpPath)
		return err
	}
	// 2. 差し替える。
	if err := os.Rename(w.tmpPath, w.realPath); err != nil {
		os.Remove(w.tmpPath)
		return err
	}
	// 3. **ページを触ったことにする**——添付を直しても本文は変わらないので、
	//    これが無いと「いつ直したか」がどこにも出ません。
	if _, err := page.BumpUpdatedAt(w.pageID); err != nil {
		// 更新時刻を進められなくても、**ファイルは既に差し替わっています**。
		// ここで失敗を返すとCADが「保存できなかった」と誤解するので、記録に留めます。
		auth.Audit(w.user, "dav.write.touch-failed", w.pageID+"/"+w.name+": "+err.Error())
	}
	auth.Audit(w.user, "dav.write", w.pageID+"/"+w.name)
	return nil
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
	dir := filepath.Join(page.AttachmentDir(pageID), davVersionsDir, name)
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
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// AttachmentVersions は添付の版を新しい順に返します（画面や復元の口が使えます）。
func AttachmentVersions(pageID, name string) []string {
	dir := filepath.Join(page.AttachmentDir(pageID), davVersionsDir, name)
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
