package page

import (
	"database/sql"
	"encoding/json"
	"fmt"
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

// DefaultOwner はサイドカーが無いページのフォールバック所有者（admin相当の扱い）。
const DefaultOwner = "admin"

// PageMeta はページの属性です。サイドカー <id>.meta.json の正本となります。
// 所有権・権限（Owner/Group/Mode）に加え、親ページID・作成日時・作成者・更新日時を持ちます。
// ParentID はゼロ詰めのページID文字列（空＝トップレベル）。
type PageMeta struct {
	Owner     string `json:"owner"`
	Group     string `json:"group"`
	Mode      string `json:"mode"`
	Public    bool   `json:"public,omitempty"` // 匿名公開フラグ（認証認可設計.md 10章）。実効公開は親チェーンとの AND。
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
	// 一時ファイル＋rename。サイドカーは読めなくなっても自動修復しない決定
	// （運用者が手で直す）なので、書き込み自身が切り詰め破損を作ってはいけない。
	return WriteFileAtomic(sidecarPath(id), data, 0644)
}

// EnsureSidecar はサイドカーが無ければ作成します（新規ページ作成時に呼ぶ）。
// 作成者(owner)を created_by に記録し、parent に親ページID（ゼロ詰め文字列、空可）を設定します。
// mode が空のときは既定（DefaultMode）になります。子ページ作成では group/mode を親から
// 継承して渡します（認証認可設計.md 10.4。public は常に false ＝ここでは設定しない）。
func EnsureSidecar(id, owner, group, mode, parent string) error {
	if _, ok := ReadSidecar(id); ok {
		return nil
	}
	if mode == "" {
		mode = DefaultMode
	}
	return WriteSidecar(id, PageMeta{
		Owner: owner, Group: group, Mode: mode,
		CreatedBy: owner, ParentID: parent,
	})
}

// requireSidecar はサイドカー（属性の正本）を読みます。読めなければエラーです。
//
// **読めないときに派生（cms.db）から組み立て直して書き戻すことはしません**（2026-08-21 決定）。
// 派生から正本を書くと、正本の欠損が派生の内容で上書きされ「見た目は健全なのに中身が
// 変わっている」状態を作ります。壊れたときは壊れたままにしておくほうが気づけるので、
// ここでは**運用者に知らせて止めます**——修復は手作業で、というのが決定です
// （[アーキテクチャとDBスキーマ.md] の決定ログ「派生（DB）から正本（ファイル）へ書き戻さない」）。
//
// 本文保存・親付け替えなど、既存ページの属性を一部だけ更新する操作の起点に使います。
func requireSidecar(id string) (PageMeta, error) {
	if meta, ok := ReadSidecar(id); ok {
		return meta, nil
	}
	return PageMeta{}, fmt.Errorf(
		"ページ属性ファイルを読めません: %s（管理者が手作業で修復してください。"+
			"内容＝%s.html は無事です）", sidecarPath(id), id)
}

// BumpUpdatedAt は本文保存時に更新日時を「今」に進めます（権限・親などは保持）。
// 進めた更新日時(RFC3339)を返します。
func BumpUpdatedAt(id string) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	meta, err := requireSidecar(id)
	if err != nil {
		return "", err // 読めないなら書かない（保存は失敗させて運用者に知らせる）
	}
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
	meta, err := requireSidecar(id)
	if err != nil {
		return "", err // 読めないなら書かない
	}
	meta.ParentID = parent
	meta.UpdatedAt = now
	if err := WriteSidecar(id, meta); err != nil {
		return "", err
	}
	return now, nil
}

// SyncPageMeta はサイドカー（正本）から cms.db の page_perms を更新します。
// SyncIndex から呼ばれます。サイドカーが無いページは admin 所有の既定として扱います。
func SyncPageMeta(tx *sql.Tx, pageID int, id string) error {
	p, ok := ReadSidecar(id)
	if !ok {
		p = PageMeta{Owner: DefaultOwner, Group: "", Mode: DefaultMode}
	}
	_, err := tx.Exec(`
		INSERT INTO page_perms (page_id, owner, grp, mode, public) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(page_id) DO UPDATE SET owner=excluded.owner, grp=excluded.grp, mode=excluded.mode, public=excluded.public
	`, pageID, p.Owner, p.Group, p.Mode, boolToInt(p.Public))
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
		p = PageMeta{Owner: DefaultOwner, Group: "", Mode: DefaultMode}
	}
	_, err = database.DB.Exec(`
		INSERT INTO page_perms (page_id, owner, grp, mode, public) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(page_id) DO UPDATE SET owner=excluded.owner, grp=excluded.grp, mode=excluded.mode, public=excluded.public
	`, pageID, p.Owner, p.Group, p.Mode, boolToInt(p.Public))
	return err
}

// boolToInt は SQLite の 0/1 列へ書き込むためのヘルパです。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
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
	var public int
	err := database.DB.QueryRow(
		`SELECT owner, grp, mode, public FROM page_perms WHERE page_id = ?`, pageID,
	).Scan(&p.Owner, &p.Group, &p.Mode, &public)
	if err != nil {
		return PageMeta{Owner: DefaultOwner, Group: "", Mode: DefaultMode}
	}
	p.Public = public != 0
	if p.Mode == "" {
		p.Mode = DefaultMode
	}
	return p
}

// EffectivePublic は、ページが匿名（未ログイン）に公開されているかを返します。
// 認証認可設計.md 10.2: 実効公開 = 自分の public ∧ 先祖すべての public ∧ root の public。
// ルート（000000）も鎖に含む（＝サイト全体のキルスイッチ）。親チェーンを上に辿り、
// 途中で public でないページがあれば false（パスゲート）。フェイルクローズ。
func EffectivePublic(pageID int) bool {
	seen := map[int]bool{}
	cur := pageID
	for i := 0; i < 10000; i++ {
		if seen[cur] {
			return false // 万一の循環は安全側に倒す
		}
		seen[cur] = true
		if !GetPerms(cur).Public {
			return false
		}
		var parent sql.NullInt64
		if err := database.DB.QueryRow("SELECT parent_id FROM pages WHERE id = ?", cur).Scan(&parent); err != nil {
			return false // ページが見つからない等はフェイルクローズ
		}
		if !parent.Valid {
			return true // トップレベル（ルート）まで公開の道が繋がっていた
		}
		cur = int(parent.Int64)
	}
	return false
}

// CanView は「この閲覧者にそのページの内容を見せてよいか」を1箇所で判定します。
// 認証済みなら read 権限（admin は無条件）、匿名なら実効公開。どちらもフェイルクローズ。
//
// 一覧の絞り込み（visibleChildren）と派生索引の集計（RequiredMaterials）が同じ
// 判定を別々に持っていたため、派生索引側だけが判定を持たず越境していた
// （設計総点検の「派生索引にページ単位認可が及んでいない」）。判定を増やすときは
// ここへ足し、呼び手を増やさないこと。
func CanView(user *auth.User, pageID int) bool {
	if user != nil {
		return GetPerms(pageID).CanRead(user)
	}
	return EffectivePublic(pageID)
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

// RequirePageReadOrPublic は read 権限を要求しますが、未認証（匿名）でも対象ページが
// 実効公開（EffectivePublic）なら閲覧を許可します（認証認可設計.md 10.5）。
// 公開ページの本文・添付ファイル配信に使います。書き込み系には使いません。
//   - 認証済み: 通常の read 判定（不可なら403）。
//   - 匿名: 実効公開なら許可、そうでなければ401（ログインを促す）。
func RequirePageReadOrPublic(w http.ResponseWriter, r *http.Request, idStr string) bool {
	pageID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return false
	}
	u := auth.CurrentUser(r)
	if u != nil {
		if !GetPerms(pageID).CanRead(u) {
			http.Error(w, "このページを閲覧する権限がありません", http.StatusForbidden)
			return false
		}
		return true
	}
	// 匿名アクセス: 実効公開なら許可。そうでなければ**不存在と同じ404**を返す
	// （2026-08-16 決定・要件定義書 §2.1）。画面側だけ404へ揃えても、APIが401を
	// 返せば「無いのか読めないのか」が分かってしまうため、ここも揃える。
	if EffectivePublic(pageID) {
		return true
	}
	http.Error(w, "ページがありません", http.StatusNotFound)
	return false
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
//
// ここは**ブラウザに解釈させない**ことも仕事です。ページのディレクトリにHTMLやSVGが
// あると、同一オリジンの文書として配信された時点で保存型XSSになります（本文の
// サニタイズを通らない経路）。添付は .pdf のみ受け付ける（attachmentFileName）ように
// してありますが、過去に置かれたファイルやバックアップ復元に備えて配信側でも守ります。
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
	// 公開ページの添付（PDF原本など）は匿名でも配信する（実効公開のときのみ）。
	if !RequirePageReadOrPublic(w, r, pageID) {
		return
	}

	// 本文と属性サイドカーは「添付ファイル」ではない。本文は画面／API が、属性は
	// /api/page-meta と権限APIが、それぞれの認可のもとで返す。ここからは配らない。
	name := parts[len(parts)-1]
	if strings.EqualFold(name, pageID+".html") || strings.EqualFold(name, pageID+".meta.json") {
		http.NotFound(w, r)
		return
	}

	// 宣言した型以外に解釈させない。http.ServeFile は Content-Type が未設定のときだけ
	// 推定するので、先に設定しておけばそれが使われる。
	w.Header().Set("X-Content-Type-Options", "nosniff")
	ext := strings.ToLower(path.Ext(name))
	switch {
	case ext == ".pdf":
		// PDFは <embed> でページ内に表示するのでインラインのまま。
		w.Header().Set("Content-Type", "application/pdf")
	case ext == ".svg":
		// SVG は「不活性な画像」として配る（要件定義書 §2.6）。
		// 本文からの表示は <img src> 経由だけで、画像コンテキストでは
		// ブラウザ仕様としてスクリプトが動かない。危ないのは**直接開かれた**とき
		// なので、この応答に限って次の2枚を掛ける:
		//   - Content-Disposition: attachment → 直接開くと描画せずダウンロード
		//   - CSP sandbox + default-src 'none' → 万一描画されても別オリジン扱い・全実行禁止
		// <img> のサブリソース読み込みはどちらのヘッダも無視するので、本文での表示は
		// 影響を受けない。
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	case inlineImageTypes[ext] != "":
		// ラスタ画像は実際の種別を明示してインライン配信する（本文の <img> から見える）。
		// 入口（UploadImageHandler）で中身のマジックナンバーを検証済み。
		w.Header().Set("Content-Type", inlineImageTypes[ext])
	default:
		// 由来の分からないファイルは、表示させずダウンロードさせる。
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment")
	}
	http.ServeFile(w, r, filepath.Join(".", filepath.FromSlash(clean)))
}

// inlineImageTypes はインライン配信を許すラスタ画像の拡張子と Content-Type です。
// SVG はここに入れません（上の分岐で不活性化して配るため）。
var inlineImageTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
}

// DataURLFor はページの添付ファイルを配信するアドレスを返します。
// 本文の `<img src>` や `<embed src>` へ入れる形（絶対パス）です。
//
// **絶対パスなのは、ページのアドレス（`/000012`）からの相対名がページの隣ではなく
// サイトのルートを指してしまうため**です。サニタイザは埋め込みの絶対パスを
// `/data/` 配下に限って許可しており、この形はその許可範囲そのものです。
func DataURLFor(pageID, fileName string) string {
	if len(pageID) < 2 {
		return ""
	}
	return "/data/master/" + pageID[:2] + "/" + pageID + "/" + fileName
}
