package cms

// ─────────────────────────────────────────────────────────────────────────
// 添付の目録——`files/meta.json`（2026-09-17）
//
// ユーザー:「各ページのFilesフォルダには、meta.jsonファイルがあって、元のファイル名や
// 保存日時等が書き込まれていると良いと思います」。
//
// 添付の保存名はサーバー採番のID（`kokl.pdf`）で、**元の名前を知っているのは本文の
// リンク文字だけ**でした。`files/` を見ても、WebDAV の一覧を見ても、何のファイルかは
// 分かりません。ここに**保存した時点の事実**を書き留めます。
//
// ── 本文との棲み分け（正本は2つにしない）──
//
//   - 本文のリンク文字は**表示名**——人が書き換えてよい
//   - `meta.json` は**届いた事実**——機械が保存時に書き、人は触らない（監査記録と同じ性格。
//     違いは、ページと一緒に動くこと：ゴミ箱・控え・WebDAV）
//
// 同じ名前が2か所にあっても競合しません。片方は「いま何と呼ぶか」、もう片方は
// 「何が届いたか」で、問いが違います。
//
// ── 形 ──
//
//	{
//	  "kokl.pdf": {
//	    "name": "R310-T040-03-03-03B_支持金具.PDF",
//	    "saved_at": "2026-09-17T12:57:03+09:00",
//	    "by": "a",
//	    "size": 178219,
//	    "sha256": "…",
//	    "source": "zip:gjsh.zip/Q055-…図面/R310-T040-03-03-03B_支持金具.PDF"
//	  }
//	}
//
// 鍵は**保存名**（ファイル名＝URL＝`data-id` の3役）。`source` は由来——人が落とした
// `upload`、メールの添付 `mail:<受信原本の保存名>`、ZIP の中身 `zip:<ZIPの保存名>/<中のパス>`、
// WebDAV の上書き `dav`。`sha256` は「同じファイルか」を後で確かめる手掛かりです。
//
// ── 守り ──
//
//   - **配信しません**——`.json` は添付にならない拡張子で（設定の検査）、配信の口
//     （`ServeCleanAttachment`・`DataFileHandler`）と WebDAV の一覧はこの名前を素通しします。
//   - **書き手は1か所**（`SaveAttachmentFrom`）＋ WebDAV の上書き。読み書きは1プロセス内の
//     ロックで直列化し、書くのは原子的（`WriteFileAtomic`）。
//   - 書けなくても添付の保存は失敗にしません（監査に `attach.meta-failed` を残す）——
//     失敗を返すと利用者が押し直し、同じファイルが2つになるほうが害が大きい。
//     読み手は目録が無ければ保存名で振る舞います。
//
// DB には載せません（ドメイン表ゼロの原則）。検索が要るときに索引として派生させれば足ります。
// ─────────────────────────────────────────────────────────────────────────

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"w-cms/internal/cms/page"
)

// AttachmentMetaFile は目録のファイル名です（`files/` の直下）。
const AttachmentMetaFile = "meta.json"

// AttachmentSourceUpload 等は `source` の書き方です。
const (
	AttachmentSourceUpload = "upload" // 人がページへ落とした
	AttachmentSourceDav    = "dav"    // WebDAV で上書きした
	// メールの添付は "mail:<受信原本の保存名>"、ZIP の中身は "zip:<ZIPの保存名>/<中のパス>"
	// （書くのは ext/comm の取り込み）。
)

// AttachmentMeta は添付1つの、保存した時点の事実です。
type AttachmentMeta struct {
	Name    string `json:"name"`             // 届いたときの名前（ファイル名だけ。フォルダは source に）
	SavedAt string `json:"saved_at"`         // RFC 3339・運用者のローカル時刻＋オフセット
	By      string `json:"by"`               // 保存した利用者
	Size    int64  `json:"size"`             // バイト
	SHA256  string `json:"sha256"`           // 中身のハッシュ（同じファイルかを後で確かめる）
	Source  string `json:"source,omitempty"` // 由来
}

// NewAttachmentMeta は中身から事実を組み立てます（保存時刻は今）。
func NewAttachmentMeta(name, by, source string, content []byte) AttachmentMeta {
	sum := sha256.Sum256(content)
	return AttachmentMeta{
		Name:    name,
		SavedAt: time.Now().In(time.Local).Format(time.RFC3339),
		By:      by,
		Size:    int64(len(content)),
		SHA256:  hex.EncodeToString(sum[:]),
		Source:  source,
	}
}

// attachmentMetaMu は目録の読み書きを直列化します（1プロセス・全ページ共通で足りる）。
var attachmentMetaMu sync.Mutex

func attachmentMetaPath(pageID string) string {
	return filepath.Join(page.AttachmentDir(pageID), AttachmentMetaFile)
}

// readAttachmentMetasLocked は目録を読みます（無ければ空。壊れていれば error）。
func readAttachmentMetasLocked(pageID string) (map[string]AttachmentMeta, error) {
	out := map[string]AttachmentMeta{}
	b, err := os.ReadFile(attachmentMetaPath(pageID))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func writeAttachmentMetasLocked(pageID string, m map[string]AttachmentMeta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(page.AttachmentDir(pageID), 0o755); err != nil {
		return err
	}
	return page.WriteFileAtomic(attachmentMetaPath(pageID), append(b, '\n'), 0o644)
}

// ReadAttachmentMetas はページの目録を返します（無ければ空。壊れていても空——読み手を止めない）。
func ReadAttachmentMetas(pageID string) map[string]AttachmentMeta {
	attachmentMetaMu.Lock()
	defer attachmentMetaMu.Unlock()
	m, err := readAttachmentMetasLocked(pageID)
	if err != nil {
		return map[string]AttachmentMeta{}
	}
	return m
}

// AttachmentMetaOf は保存名1つぶんの事実を返します。
func AttachmentMetaOf(pageID, storedName string) (AttachmentMeta, bool) {
	m, ok := ReadAttachmentMetas(pageID)[storedName]
	return m, ok
}

// RecordAttachmentMeta は保存名1つぶんを書き留めます（同じ保存名があれば置き換え）。
func RecordAttachmentMeta(pageID, storedName string, meta AttachmentMeta) error {
	attachmentMetaMu.Lock()
	defer attachmentMetaMu.Unlock()
	m, err := readAttachmentMetasLocked(pageID)
	if err != nil {
		// 壊れた目録は**上書きしません**——中身を失うより、書けない事実を返すほうがよい。
		return err
	}
	m[storedName] = meta
	return writeAttachmentMetasLocked(pageID, m)
}

// UpdateAttachmentMeta は既にある1件を書き換えます（無ければ fn に空を渡して新規に作る）。
func UpdateAttachmentMeta(pageID, storedName string, fn func(*AttachmentMeta)) error {
	attachmentMetaMu.Lock()
	defer attachmentMetaMu.Unlock()
	m, err := readAttachmentMetasLocked(pageID)
	if err != nil {
		return err
	}
	cur := m[storedName]
	fn(&cur)
	m[storedName] = cur
	return writeAttachmentMetasLocked(pageID, m)
}

// AttachmentDisplayName は、目録にあれば届いたときの名前、無ければ保存名を返します。
//
// 読み手（ファイル表示・WebDAV）はこれを通します——目録が無い古い添付でも壊れません。
func AttachmentDisplayName(pageID, storedName string) string {
	if m, ok := AttachmentMetaOf(pageID, storedName); ok && strings.TrimSpace(m.Name) != "" {
		return m.Name
	}
	return storedName
}

// IsAttachmentMetaFile は、その名前が目録（添付ではないもの）かを返します。
// 配信と一覧はこれで素通しします。
func IsAttachmentMetaFile(name string) bool {
	return strings.EqualFold(name, AttachmentMetaFile) ||
		strings.EqualFold(filepath.Ext(name), ".json")
}
