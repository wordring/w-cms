package cms

// ─────────────────────────────────────────────────────────────────────────
// WebDAV のファイルシステム——ページの木をそのままフォルダに見せる（2026-09-07）
//
// ユーザー:「通信箱も取引先もプラグインの領域ですが、クリックして開くは w-cms 本体の
// 機能なので、設定で見せないとするもの以外は見せて良いのでは？」
//
// **その通りで、前の作りは層を跨いでいました。** 最初は `/dav/<ページID>/` の1ページ
// ずつで、次に「`取引先` の下だけ見せる」を提案しましたが、**それはコアの機能に業務の
// 言葉を焼き込むこと**です。`取引先` も `通信箱` も**語彙の側の持ち物**で、コアが
// 名前で特別扱いしてはいけません。
//
// いまの形:
//
//	Z:\取引先\トーアスポーツマシーン\現行\φ320 三輪共通\補強ストッパー\fglh.dxf
//	  └ ページの木そのまま。**見せないものは設定が決めます**（webdav_hidden）
//
// **見えるのは読める範囲だけ**です（`visibleChildren` と同じ絞り方）。ブラウザの
// 子ページ一覧が既に権限で絞った木を見せているので、ここで新しく漏れるものはありません
// ——最初「木を歩かせると読めないページが漏れる」を理由に避けましたが、**その理由は
// 成り立ちませんでした**。
//
// ── 名前の付け替えについて ──
//
// ページの題は**そのままではフォルダ名にできません**。実データで
// **50件がWindowsの使えない文字を含み**（`RE: …` のコロン。メールの件名由来）、
// **14件が同じ親の下で重複**しています。そこで:
//
//   - 使えない文字は**全角へ寄せます**（`:`→`：`）——読めるまま、衝突しない
//   - 重複したら**ページIDを添えます**（`RE- 見積り (010163)`）
//
// **付け替えた名前は表示だけのもの**で、正本は題です。逆引き（パス→ページ）は
// 「子の題を同じ規則で付け替えて突き合わせる」ので、規則を変えても壊れません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/webdav"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// errReadOnly は書き込みを断る印です（読み取り専用の期間）。
var errReadOnly = errors.New("いまは読み取り専用です")

// davFS はページの木を見せる webdav.FileSystem です。
//
// **利用者ごとに作ります**——見える範囲が人によって違うので、共有できません。
type davFS struct {
	user *auth.User
}

// resolve はパス（`/取引先/A社/…`）を、ページIDと「残りのファイル名」へ分解します。
//
// 返り値の fileName が空ならフォルダ（ページ）そのものを指します。
func (f davFS) resolve(name string) (pageID string, fileName string, err error) {
	segs := splitPath(name)
	cur := TopPageID

	for i, seg := range segs {
		if child, ok := f.childByName(cur, seg); ok {
			cur = child
			continue
		}
		// 子ページに無ければ、**最後の1つ**は添付ファイルかもしれない。
		if i == len(segs)-1 {
			if _, ok := page.AttachmentPath(cur, seg); ok {
				return cur, seg, nil
			}
		}
		return "", "", os.ErrNotExist
	}
	return cur, "", nil
}

// childByName は付け替えた名前から子ページを引きます。
func (f davFS) childByName(parentID, name string) (string, bool) {
	for _, e := range f.childEntries(parentID) {
		if e.name == name {
			return e.pageID, true
		}
	}
	return "", false
}

// davChild は子ページ1件（付け替え後の名前つき）です。
type davChild struct {
	pageID string
	name   string
}

// childEntries は読める子ページを、フォルダ名に使える名前で返します。
func (f davFS) childEntries(parentID string) []davChild {
	idInt, err := strconv.Atoi(parentID)
	if err != nil {
		return nil
	}
	kids, err := visibleChildren(f.user, idInt)
	if err != nil {
		return nil
	}
	hidden := hiddenWebDAVTitles()
	used := map[string]bool{}
	out := make([]davChild, 0, len(kids))
	for _, k := range kids {
		if hidden[strings.TrimSpace(k.Title)] {
			continue // **設定が見せないと言ったもの**
		}
		name := safeFolderName(k.Title)
		if name == "" {
			name = k.ID
		}
		if used[name] {
			// **重複はページIDで解きます**（実データに14件ある）。
			name = name + " (" + k.ID + ")"
		}
		used[name] = true
		out = append(out, davChild{pageID: k.ID, name: name})
	}
	return out
}

// ── webdav.FileSystem ──

func (f davFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	return errReadOnly
}

func (f davFS) RemoveAll(ctx context.Context, name string) error { return errReadOnly }

func (f davFS) Rename(ctx context.Context, oldName, newName string) error { return errReadOnly }

func (f davFS) Stat(ctx context.Context, name string) (fs.FileInfo, error) {
	pageID, fileName, err := f.resolve(name)
	if err != nil {
		return nil, err
	}
	if fileName == "" {
		return davDirInfo{name: path.Base(name)}, nil
	}
	fp, ok := page.AttachmentPath(pageID, fileName)
	if !ok {
		return nil, os.ErrNotExist
	}
	return os.Stat(fp)
}

func (f davFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	// **書き込みで開こうとしたら断ります**（読み取り専用の期間）。
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0 {
		return nil, errReadOnly
	}
	pageID, fileName, err := f.resolve(name)
	if err != nil {
		return nil, err
	}
	if fileName == "" {
		return &davDir{fs: f, pageID: pageID, name: path.Base(name)}, nil
	}
	fp, ok := page.AttachmentPath(pageID, fileName)
	if !ok {
		return nil, os.ErrNotExist
	}
	file, err := os.Open(fp)
	if err != nil {
		return nil, err
	}
	return davFile{File: file}, nil
}

// ── フォルダ（ページ）──

// davDir はページ1枚をフォルダとして見せます。
//
// 中身は**子ページ（フォルダ）＋そのページの添付（ファイル）**です。
type davDir struct {
	fs     davFS
	pageID string
	name   string
	read   bool // Readdir を一度返したか（2度目は空＝終わり）
}

func (d *davDir) Close() error                   { return nil }
func (d *davDir) Read(p []byte) (int, error)     { return 0, os.ErrInvalid }
func (d *davDir) Write(p []byte) (int, error)    { return 0, errReadOnly }
func (d *davDir) Seek(int64, int) (int64, error) { return 0, os.ErrInvalid }
func (d *davDir) Stat() (fs.FileInfo, error)     { return davDirInfo{name: d.name}, nil }

func (d *davDir) Readdir(count int) ([]fs.FileInfo, error) {
	if d.read {
		return nil, nil
	}
	d.read = true

	var out []fs.FileInfo
	for _, c := range d.fs.childEntries(d.pageID) {
		out = append(out, davDirInfo{name: c.name})
	}
	// 添付は実ファイルなので、そのまま情報を渡します（大きさ・更新時刻が正しく出ます）。
	entries, err := os.ReadDir(page.AttachmentDir(d.pageID))
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if info, err := e.Info(); err == nil {
				out = append(out, info)
			}
		}
	}
	return out, nil
}

// davDirInfo はフォルダ（＝ページ）の情報です。**ページの更新時刻は使いません**
// ——添付を差し替えても本文は変わらないので、当てにならないためです。
type davDirInfo struct {
	name string
}

func (i davDirInfo) Name() string       { return i.name }
func (i davDirInfo) Size() int64        { return 0 }
func (i davDirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (i davDirInfo) ModTime() time.Time { return time.Time{} }
func (i davDirInfo) IsDir() bool        { return true }
func (i davDirInfo) Sys() any           { return nil }

// davFile は添付ファイルです。**書き込みだけ断ります**（読みは素の os.File）。
type davFile struct {
	*os.File
}

func (f davFile) Write(p []byte) (int, error) { return 0, errReadOnly }

// ── 名前の付け替え ──

// winReserved はWindowsが使えない文字と、その寄せ先です。
//
// **全角へ寄せます**——`_` に潰すと `RE_ …` が並んで見分けが付かなくなるので、
// 読める形を保ちます。
var winReserved = strings.NewReplacer(
	`\`, "＼", "/", "／", ":", "：", "*", "＊",
	"?", "？", `"`, "”", "<", "＜", ">", "＞", "|", "｜",
)

// safeFolderName はページの題をフォルダ名に使える形へ整えます。
func safeFolderName(title string) string {
	s := winReserved.Replace(strings.TrimSpace(title))
	// 制御文字は落とす（題に入ることは無いはずだが、入れば名前が壊れる）。
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	// **末尾のドットと空白はWindowsが黙って落とします**。落とされると逆引きで
	// 見つからなくなるので、こちらで先に落としておきます。
	s = strings.TrimRight(s, " .")
	if s == "" {
		return ""
	}
	// 予約語（CON・PRN・AUX・NUL・COM1…）はそのままだと使えません。
	if davReservedNames[strings.ToUpper(s)] {
		s += "_"
	}
	return s
}

// davReservedNames はWindowsの予約されたファイル名です。
var davReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// splitPath はURLのパスを区画へ割ります（空の区画は落とす）。
func splitPath(name string) []string {
	var out []string
	for _, s := range strings.Split(path.Clean("/"+name), "/") {
		if s != "" && s != "." {
			out = append(out, s)
		}
	}
	return out
}
