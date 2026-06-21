package cms

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/database"
)

// ─────────────────────────────────────────────────────────────────────────
// ページの属性（サイドカー）— 認証認可設計.md 3〜5章
//
// 各ページの属性（所有権・権限・親ページ・作成/更新情報）は、本文HTMLとは別の
// 「サイドカー」ファイル data/master/xx/<id>/<id>.meta.json を正本として持ちます。
// UNIXに倣い「ファイルの内容＝HTML」「ファイルの属性＝サイドカー」と分離します。
// cms.db の pages / page_perms はそこから再生成される派生インデックスです
// （DB再構築でも属性が失われない。自己認可問題の回避にもなる）。
//
// mode は3桁文字列 "<owner><group><other>"。各桁は 0〜3（read=2, write=1, rw=3, なし=0）。
// ─────────────────────────────────────────────────────────────────────────

// DefaultMode は新規ページの既定権限（owner=rw, group=rw, other=なし）。認証認可設計.md 3.4節【確定】。
const DefaultMode = "330"

// defaultOwner はサイドカーが無いページのフォールバック所有者（admin相当の扱い）。
const defaultOwner = "admin"

// PageMeta はページの属性です。サイドカー <id>.meta.json の正本となります。
// 所有権・権限（Owner/Group/Mode）に加え、親ページID・作成日時・作成者・更新日時を持ちます。
// ParentID はゼロ詰めのページID文字列（空＝トップレベル）。
type PageMeta struct {
	Owner     string `json:"owner"`
	Group     string `json:"group"`
	Mode      string `json:"mode"`
	ParentID  string `json:"parent_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// sidecarPath は <id>.meta.json の物理パスを返します。
func sidecarPath(id string) string {
	return filepath.Join(GetPageDir(id), id+".meta.json")
}

// ReadSidecar はサイドカーを読み込みます。存在しなければ ok=false。
func ReadSidecar(id string) (PageMeta, bool) {
	data, err := os.ReadFile(sidecarPath(id))
	if err != nil {
		return PageMeta{}, false
	}
	var p PageMeta
	if err := json.Unmarshal(data, &p); err != nil {
		return PageMeta{}, false
	}
	if p.Mode == "" {
		p.Mode = DefaultMode
	}
	return p, true
}

// WriteSidecar はサイドカー（与えられた属性そのまま）を書き込みます。
// CreatedAt / UpdatedAt は空のときだけ「今」で補完します（呼び出し側が値を持って
// いればそれを尊重）。権限フィールド（Owner/Group/Mode）の変更は chmod/chown 経由のみ、
// 更新日時の前進は BumpUpdatedAt 経由のみ、という規律で自己認可を防ぎます。
func WriteSidecar(id string, p PageMeta) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if p.CreatedAt == "" {
		p.CreatedAt = now
	}
	if p.UpdatedAt == "" {
		p.UpdatedAt = now
	}
	if p.Mode == "" {
		p.Mode = DefaultMode
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(GetPageDir(id), 0755); err != nil {
		return err
	}
	return os.WriteFile(sidecarPath(id), data, 0644)
}

// EnsureSidecar はサイドカーが無ければ作成します（新規ページ作成時に呼ぶ）。
// 作成者(owner)を created_by に記録し、parent に親ページID（ゼロ詰め文字列、空可）を設定します。
func EnsureSidecar(id, owner, group, parent string) error {
	if _, ok := ReadSidecar(id); ok {
		return nil
	}
	return WriteSidecar(id, PageMeta{
		Owner: owner, Group: group, Mode: DefaultMode,
		CreatedBy: owner, ParentID: parent,
	})
}

// readMetaOrPerms はサイドカーを読み、無ければDBの実効権限で最低限を補います。
// 本文保存・親付け替えなど、既存ページの属性を一部だけ更新する操作の起点に使います
// （所有権を空で上書きしてしまわないため）。
func readMetaOrPerms(id string) PageMeta {
	if meta, ok := ReadSidecar(id); ok {
		return meta
	}
	if idInt, err := strconv.Atoi(id); err == nil {
		return GetPerms(idInt)
	}
	return PageMeta{Owner: defaultOwner, Mode: DefaultMode}
}

// BumpUpdatedAt は本文保存時に更新日時を「今」に進めます（権限・親などは保持）。
// 進めた更新日時(RFC3339)を返します。
func BumpUpdatedAt(id string) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	meta := readMetaOrPerms(id)
	meta.UpdatedAt = now
	if err := WriteSidecar(id, meta); err != nil {
		return "", err
	}
	return now, nil
}

// SetSidecarParent は親ページIDを変更し、更新日時を進めます（権限などは保持）。
// parent はゼロ詰めのページID文字列（空＝トップレベル）。進めた更新日時を返します。
func SetSidecarParent(id, parent string) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	meta := readMetaOrPerms(id)
	meta.ParentID = parent
	meta.UpdatedAt = now
	if err := WriteSidecar(id, meta); err != nil {
		return "", err
	}
	return now, nil
}

// syncPageMeta はサイドカー（正本）から cms.db の page_perms を更新します。
// SyncIndex から呼ばれます。サイドカーが無いページは admin 所有の既定として扱います。
func syncPageMeta(tx *sql.Tx, pageID int, id string) error {
	p, ok := ReadSidecar(id)
	if !ok {
		p = PageMeta{Owner: defaultOwner, Group: "", Mode: DefaultMode}
	}
	_, err := tx.Exec(`
		INSERT INTO page_perms (page_id, owner, grp, mode) VALUES (?, ?, ?, ?)
		ON CONFLICT(page_id) DO UPDATE SET owner=excluded.owner, grp=excluded.grp, mode=excluded.mode
	`, pageID, p.Owner, p.Group, p.Mode)
	return err
}

// RefreshPerms はサイドカー（正本）から cms.db の page_perms を更新します（トランザクション外）。
// chmod/chown など、本文の再同期を伴わない権限変更のあとに呼びます。
func RefreshPerms(id string) error {
	pageID, err := strconv.Atoi(id)
	if err != nil {
		return err
	}
	p, ok := ReadSidecar(id)
	if !ok {
		p = PageMeta{Owner: defaultOwner, Group: "", Mode: DefaultMode}
	}
	_, err = database.DB.Exec(`
		INSERT INTO page_perms (page_id, owner, grp, mode) VALUES (?, ?, ?, ?)
		ON CONFLICT(page_id) DO UPDATE SET owner=excluded.owner, grp=excluded.grp, mode=excluded.mode
	`, pageID, p.Owner, p.Group, p.Mode)
	return err
}

// ValidMode は mode が3桁・各桁0〜3かを検証します。
func ValidMode(m string) bool {
	if len(m) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if m[i] < '0' || m[i] > '3' {
			return false
		}
	}
	return true
}

// GetPerms は cms.db からページ権限を取得します。レコードが無い場合は
// 「admin所有・既定mode」のフォールバックを返します（フェイルクローズ）。
func GetPerms(pageID int) PageMeta {
	var p PageMeta
	err := database.DB.QueryRow(
		`SELECT owner, grp, mode FROM page_perms WHERE page_id = ?`, pageID,
	).Scan(&p.Owner, &p.Group, &p.Mode)
	if err != nil {
		return PageMeta{Owner: defaultOwner, Group: "", Mode: DefaultMode}
	}
	if p.Mode == "" {
		p.Mode = DefaultMode
	}
	return p
}

// --- mode のビット判定 ---

// classDigit は mode の class（0=owner,1=group,2=other）の数字を返します。
func classDigit(mode string, class int) int {
	if len(mode) != 3 || class < 0 || class > 2 {
		return 0
	}
	d := mode[class]
	if d < '0' || d > '3' {
		return 0
	}
	return int(d - '0')
}

// effectiveClass はユーザー u がページ p に対してどのクラスで判定されるかを返します。
func effectiveClass(p PageMeta, u *auth.User) int {
	if u.Username == p.Owner {
		return 0 // owner
	}
	for _, g := range u.Groups {
		if g == p.Group && p.Group != "" {
			return 1 // group
		}
	}
	return 2 // other
}

// CanRead はユーザー u がページ p を閲覧できるかを返します（adminはバイパス）。
func (p PageMeta) CanRead(u *auth.User) bool {
	if u.IsAdmin {
		return true
	}
	return classDigit(p.Mode, effectiveClass(p, u))&2 != 0
}

// CanWrite はユーザー u がページ p を編集できるかを返します（adminはバイパス）。
func (p PageMeta) CanWrite(u *auth.User) bool {
	if u.IsAdmin {
		return true
	}
	return classDigit(p.Mode, effectiveClass(p, u))&1 != 0
}

// --- HTTPハンドラ向けの強制チェック ---

// requireUser はリクエストから認証済みユーザーを取得します。未認証なら401を書いて nil を返します。
func requireUser(w http.ResponseWriter, r *http.Request) *auth.User {
	u := auth.CurrentUser(r)
	if u == nil {
		http.Error(w, "認証が必要です", http.StatusUnauthorized)
	}
	return u
}

// RequirePageRead は指定ページIDのread権限を要求します。許可ならtrue、不許可なら403等を書いてfalse。
func RequirePageRead(w http.ResponseWriter, r *http.Request, idStr string) bool {
	u := requireUser(w, r)
	if u == nil {
		return false
	}
	pageID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return false
	}
	if !GetPerms(pageID).CanRead(u) {
		http.Error(w, "このページを閲覧する権限がありません", http.StatusForbidden)
		return false
	}
	return true
}

// RequirePageWrite は指定ページIDのwrite権限を要求します。
func RequirePageWrite(w http.ResponseWriter, r *http.Request, idStr string) bool {
	u := requireUser(w, r)
	if u == nil {
		return false
	}
	pageID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return false
	}
	if !GetPerms(pageID).CanWrite(u) {
		http.Error(w, "このページを編集する権限がありません", http.StatusForbidden)
		return false
	}
	return true
}

// RequireAdmin はadmin権限を要求します。
func RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	u := requireUser(w, r)
	if u == nil {
		return false
	}
	if !u.IsAdmin {
		http.Error(w, "管理者権限が必要です", http.StatusForbidden)
		return false
	}
	return true
}

// DataFileHandler は /data/master/<prefix>/<id>/<file> を、対象ページの read 権限を
// 確認した上で配信します（PDF原本などの機微ファイルをファイルサーバ直結にしないため）。
func DataFileHandler(w http.ResponseWriter, r *http.Request) {
	// path.Clean で ".." を解決し、ディレクトリトラバーサルを防ぐ。
	clean := path.Clean(r.URL.Path) // 例: /data/master/00/000001/foo.pdf
	parts := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	// 期待する形: [data master <prefix> <id> <file...>]
	if len(parts) < 5 || parts[0] != "data" || parts[1] != "master" {
		http.NotFound(w, r)
		return
	}
	pageID := parts[3]
	if !RequirePageRead(w, r, pageID) {
		return
	}
	http.ServeFile(w, r, filepath.Join(".", filepath.FromSlash(clean)))
}
