package page

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

	// SortKey は兄弟ページの中での並び順です（空なら題で並ぶ）。
	//
	// **1つの欄が自動の順序と手動の順序を兼ねます**——取り込みは受信日時を入れ、
	// 人がドラッグで並べ替えたときは隣どうしの中間の値を入れます。前ページを指す
	// 連結リストにしないのは、ドラッグ1回で2ページ書く必要があり、途中で失敗すると
	// 以降の順序が全部失われ、ページを消すと鎖が切れるためです（2026-09-03 検討）。
	//
	// 空でも困りません——年・月フォルダは題（`2024年`・`01月`）で正しく並びます。
	SortKey string `json:"sort_key,omitempty"`
}

// sidecarPath は <id>.meta.json の物理パスを返します。
func sidecarPath(id string) string {
	return SidecarPath(id)
}

// SidecarPath はページの属性のファイル（<id>.meta.json）の場所です（BodyPath と対——場所を組むのはここだけ）。
func SidecarPath(id string) string {
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
	return writeSidecar(id, p, true)
}

// rewriteSidecar は、もうあるページのサイドカーを書き替えます。⚠ ページのフォルダを作らない（2026-10-26）——
// 読んでから書くまでのあいだにページがごみ箱へ移ると、フォルダを作り直して、属性のファイルだけの幽霊のページが
// できた（【考察】コードの矛盾 §2.1）。フォルダが無ければ書かずに断る。
func rewriteSidecar(id string, p PageMeta) error {
	return writeSidecar(id, p, false)
}

// ErrPageGone は、書き替えようとしたページのフォルダが無い（ごみ箱へ移った）ことです。
var ErrPageGone = errors.New("ページがありません（ごみ箱へ移ったかもしれません）")

func writeSidecar(id string, p PageMeta, create bool) error {
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
	if create {
		if err := os.MkdirAll(GetPageDir(id), 0755); err != nil {
			return err
		}
	} else if fi, err := os.Stat(GetPageDir(id)); err != nil || !fi.IsDir() {
		return ErrPageGone
	}
	// 一時ファイル＋rename（フォルダがあとでごみ箱へ移っても、一時ファイルはそのフォルダと一緒に移り、rename は落ちる）。サイドカーは読めなくなっても自動修復しない決定
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

// touchSidecar はサイドカーを読み、change で一部を変え、更新日時を「今」に進めて
// 書き戻します。進めた更新日時(RFC3339)を返します。
//
// **読めないなら書きません**（保存は失敗させて運用者に知らせる。requireSidecar）。
// 既存ページの属性を一部だけ更新する口（更新日時・親）はすべてここを通ります。
func touchSidecar(id string, change func(*PageMeta)) (string, error) {
	sidecarMu.Lock()
	defer sidecarMu.Unlock()
	meta, err := requireSidecar(id)
	if err != nil {
		return "", err
	}
	change(&meta)
	meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := rewriteSidecar(id, meta); err != nil {
		return "", err
	}
	return meta.UpdatedAt, nil
}

// sidecarMu は、サイドカーの「読んで・一部を変えて・書き戻す」を1つずつ通すロックです。
// ⚠ 保存（更新日時を進める）と移動（親を替える）が重なると、先に読んだほうが古い親を書き戻し、サイドカーと
// cms.db の親が食い違った（2026-10-22 の見直し）。読み書きは速いので、全部のページで1つにする。
var sidecarMu sync.Mutex

// UpdateSidecar は、サイドカーを読んで change で変えて書き戻します（sidecarMu の中で）。
// 読めなければ書かずに ok=false（派生から組み立て直さない——requireSidecar と同じ決まり）。
// change が false を返したら書きません。
func UpdateSidecar(id string, change func(*PageMeta) bool) (meta PageMeta, ok bool, err error) {
	sidecarMu.Lock()
	defer sidecarMu.Unlock()
	meta, ok = ReadSidecar(id)
	if !ok || !change(&meta) {
		return meta, ok, nil
	}
	return meta, true, rewriteSidecar(id, meta)
}

// BumpUpdatedAt は本文保存時に更新日時を「今」に進めます（権限・親などは保持）。
// 進めた更新日時(RFC3339)を返します。
func BumpUpdatedAt(id string) (string, error) {
	return touchSidecar(id, func(*PageMeta) {})
}

// SetSidecarParent は親ページIDを変更し、更新日時を進めます（権限などは保持）。
// parent はゼロ詰めのページID文字列（空＝トップレベル）。進めた更新日時を返します。
func SetSidecarParent(id, parent string) (string, error) {
	return touchSidecar(id, func(m *PageMeta) { m.ParentID = parent })
}

// defaultMeta はサイドカーの無いページ（や索引に無いページ）の既定です
// ——admin 所有・既定 mode・非公開（フェイルクローズ）。
func defaultMeta() PageMeta {
	return PageMeta{Owner: DefaultOwner, Group: "", Mode: DefaultMode}
}

// sidecarOrDefault はサイドカーを読み、無ければ既定を返します（派生の書き込み用）。
func sidecarOrDefault(id string) PageMeta {
	if p, ok := ReadSidecar(id); ok {
		return p
	}
	return defaultMeta()
}

// execer は `*sql.DB` と `*sql.Tx` の共通の口です（page_perms の upsert が両方から呼ばれる）。
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// upsertPerms はサイドカーの権限を cms.db の page_perms へ書きます（派生の更新）。
func upsertPerms(db execer, pageID int, p PageMeta) error {
	_, err := db.Exec(`
		INSERT INTO page_perms (page_id, owner, grp, mode, public) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(page_id) DO UPDATE SET owner=excluded.owner, grp=excluded.grp, mode=excluded.mode, public=excluded.public
	`, pageID, p.Owner, p.Group, p.Mode, boolToInt(p.Public))
	return err
}

// SyncPageMeta はサイドカー（正本）から cms.db の page_perms を更新します。
// SyncIndex から呼ばれます。サイドカーが無いページは admin 所有の既定として扱います。
func SyncPageMeta(tx *sql.Tx, pageID int, id string) error {
	return upsertPerms(tx, pageID, sidecarOrDefault(id))
}

// RefreshPerms はサイドカー（正本）から cms.db の page_perms を更新します（トランザクション外）。
// chmod/chown など、本文の再同期を伴わない権限変更のあとに呼びます。
func RefreshPerms(id string) error {
	pageID, err := strconv.Atoi(id)
	if err != nil {
		return err
	}
	return upsertPerms(database.DB, pageID, sidecarOrDefault(id))
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
		return defaultMeta()
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
	if p.OwnedBy(u) {
		return 0 // owner
	}
	for _, g := range u.Groups {
		if g == p.Group && p.Group != "" {
			return 1 // group
		}
	}
	return 2 // other
}

// OwnedBy は、利用者 u がページ p の持ち主かを返します。
//
// ⚠ 持ち主が DefaultOwner（"admin"——サイドカーの無いページとトップページの既定）のときは、だれも名前では持ち主に
// なりません（2026-10-26）。それまでは、管理者でない利用者「admin」を作る・管理者から外すと、トップページ（公開の
// 切り替え）とサイドカーの無いページの持ち主になった。管理者は持ち主でなくても全部できるので、困らない。
func (p PageMeta) OwnedBy(u *auth.User) bool {
	return u != nil && p.Owner != DefaultOwner && u.Username == p.Owner
}

// CanRead はユーザー u がページ p を閲覧できるかを返します（adminはバイパス）。
// u が nil（ログインしていない人）なら false——匿名の判定は CanView（実効公開）です。それまでは nil で落ちた（2026-10-26）。
func (p PageMeta) CanRead(u *auth.User) bool {
	if u == nil {
		return false
	}
	if u.IsAdmin {
		return true
	}
	return classDigit(p.Mode, effectiveClass(p, u))&2 != 0
}

// CanWrite はユーザー u がページ p を編集できるかを返します（adminはバイパス）。u が nil なら false（ログインしていない人は書けない）。
func (p PageMeta) CanWrite(u *auth.User) bool {
	if u == nil {
		return false
	}
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

// requirePage は認証済み利用者に対するページ権限の関門です（read／write 共通の骨組み）。
// 未認証は 401、IDが不正なら 400、allow が偽なら 403 に deny の文を書いて false を返します。
func requirePage(w http.ResponseWriter, r *http.Request, idStr string,
	allow func(PageMeta, *auth.User) bool, deny string) bool {
	u := requireUser(w, r)
	if u == nil {
		return false
	}
	pageID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return false
	}
	if !allow(GetPerms(pageID), u) {
		http.Error(w, deny, http.StatusForbidden)
		return false
	}
	return true
}

// RequirePageRead は指定ページIDのread権限を要求します。許可ならtrue、不許可なら403等を書いてfalse。
func RequirePageRead(w http.ResponseWriter, r *http.Request, idStr string) bool {
	return requirePage(w, r, idStr, PageMeta.CanRead, "このページを閲覧する権限がありません")
}

// RequirePageReadOrPublic は read 権限を要求しますが、未認証（匿名）でも対象ページが
// 実効公開（EffectivePublic）なら閲覧を許可します（認証認可設計.md 10.5）。
// 公開ページの本文・添付ファイル配信に使います。書き込み系には使いません。
//   - 認証済み: 通常の read 判定（不可なら403）。
//   - 匿名: 実効公開なら許可、そうでなければ404（「読めない」と「存在しない」を区別させない）。
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
	return requirePage(w, r, idStr, PageMeta.CanWrite, "このページを編集する権限がありません")
}

// RequireAdmin はadmin権限を要求します（auth.RequireAdmin と同じ関門。ページ系の
// ハンドラが認可の口をこのパッケージだけで揃えられるよう、ここからも呼べます）。
func RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	return auth.RequireAdmin(w, r)
}

// DataFileHandler は /data/master/<prefix>/<id>/<file> を、対象ページの read 権限を
// 確認した上で配信します（PDF原本などの機微ファイルをファイルサーバ直結にしないため）。
//
// ここは**ブラウザに解釈させない**ことも仕事です。ページのディレクトリにHTMLやSVGが
// あると、同一オリジンの文書として配信された時点で保存型XSSになります（本文の
// サニタイズを通らない経路）。入口は拡張子で絞ってあります（cms.SafeAttachmentName）が、
// 過去に置かれたファイルやバックアップ復元に備えて配信側でも守ります。
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
	// 配るのは、ページのフォルダの直下（旧い添付の置き場）と files/ の直下（添付）のファイルだけです。
	// ⚠ それより深い所・フォルダそのものは配らない——versions/ の版の控えが、フォルダの一覧（text/html）と中身ごと読めました。
	// 公開のページなら、ログインしていない人にも、公開する前に消した文まで読めていた（2026-10-27 の見直し）。
	rest := parts[4:]
	if parts[2] != filepath.Base(filepath.Dir(GetPageDir(pageID))) || !(len(rest) == 1 || (len(rest) == 2 && rest[0] == "files")) {
		http.NotFound(w, r)
		return
	}
	// 公開ページの添付（PDF原本など）は匿名でも配信する（実効公開のときのみ）。
	if !RequirePageReadOrPublic(w, r, pageID) {
		return
	}

	// 本文と属性サイドカーは「添付ファイル」ではない。本文は画面／API が、属性は
	// /api/page-meta と権限APIが、それぞれの認可のもとで返す。ここからは配らない。
	name := parts[len(parts)-1]
	// 「.」で始まる名前（書きかけの一時ファイル .<名前>.tmp-N など）も配らない（2026-10-27 の見直し・きれいな URL の口と揃える）。
	if aliasedFileName(name) || strings.HasPrefix(name, ".") || strings.EqualFold(name, pageID+".html") || strings.EqualFold(filepath.Ext(name), ".json") {
		// .json は本文の属性サイドカーか添付の目録（files/meta.json）で、どちらも配らない。
		http.NotFound(w, r)
		return
	}

	if !AnonymousMayFetch(r, pageID, name) {
		http.NotFound(w, r)
		return
	}
	setAttachmentHeaders(w, name)
	// URLの `/data/master` は名前で、置き場所ではありません——データの場所（WCMS_DATA_DIR・
	// 既定 data）の下の master へ読み替えます（2026-10-14）。parts は path.Clean 済みなので `..` を含みません。
	fp := filepath.Join(append([]string{MasterDir()}, parts[2:]...)...)
	fi, err := os.Stat(fp)
	if err != nil || !fi.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	setAttachmentCache(w, r, fi)
	http.ServeFile(w, r, fp)
}

// setAttachmentHeaders は添付の応答ヘッダを種別で決めます。
// **ブラウザに解釈させない**のが仕事の芯——ページのディレクトリにHTMLやSVGがあると、
// 同一オリジンの文書として配信された時点で保存型XSSになります（本文のサニタイズを
// 通らない経路）。旧配信口（DataFileHandler）ときれいなURL（ServeCleanAttachment）が共有します。
func setAttachmentHeaders(w http.ResponseWriter, name string) {
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
		// 由来の分からないファイルは、表示させずダウンロードさせる。保存名の既定は
		// リンクの download 属性が運ぶ（filename を付けない素の attachment なら
		// ブラウザは download 属性を優先できる）。
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment")
	}
}

// aliasedFileName は、Windows が別の名前のファイルとして開く名前かです（末尾の点・空白、ストリーム名の「:」）。
// ⚠ 「000001.meta.json.」や「meta.json::$DATA」は拡張子の検査を抜けて、Windows では本物の .json を開く
// （配らないはずの属性と目録が配られていた・2026-10-22 の見直し）。添付の名前にこれらは来ない。
func aliasedFileName(name string) bool {
	return strings.ContainsRune(name, ':') || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ")
}

// ServeCleanAttachment はきれいなURL（/<ページID>/<ファイル名>）の配信口です
// （2026-08-31 ユーザー決定「実際に保存される場所を推測されたくない」）。
// RootHandler がページURLの下の2区画パスをここへ回す。読むのは files/ だけ
// （旧置き場の直下は旧URLの互換配信 DataFileHandler が担う）。
// 認可は旧口と同じ——ページの read（実効公開なら匿名も可）。無ければ404で、
// 「読めない」と「存在しない」は匿名に区別させない（既存の流儀）。
func ServeCleanAttachment(w http.ResponseWriter, r *http.Request, pageID, name string, notFound http.HandlerFunc) {
	// `.json` は添付にならない拡張子（設定の検査が弾く）。files/ に在る .json は添付の
	// 目録（`meta.json`・cms.AttachmentMetaFile）なので配らない（2026-09-17）。
	if strings.ContainsAny(name, "/\\") || name == "" || strings.HasPrefix(name, ".") || aliasedFileName(name) ||
		strings.EqualFold(filepath.Ext(name), ".json") {
		notFound(w, r)
		return
	}
	if !RequirePageReadOrPublic(w, r, pageID) {
		return
	}
	if !AnonymousMayFetch(r, pageID, name) {
		notFound(w, r)
		return
	}
	fp := filepath.Join(AttachmentDir(pageID), name)
	fi, err := os.Stat(fp)
	if err != nil || !fi.Mode().IsRegular() {
		notFound(w, r)
		return
	}
	setAttachmentHeaders(w, name)
	setAttachmentCache(w, r, fi)
	http.ServeFile(w, r, fp)
}

// AnonymousMayFetch は、ログインしていない訪問者に、公開のページ pageID の添付 name を配ってよいかです（2026-10-27 の見直し）。
// ログインした人は、ページを読めれば（呼び手が確かめる）よい。ログインしていない人には、**いまの本文が指している添付だけ**を配る
// ——添付の名前は4文字（約168万通り）なので、公開のページの files/ に残った、本文から消した添付や公開する前に上げたものを、
// 総当たりで取れた。指し方は、きれいな URL（/ページ番号/名前）か、ファイル表示の参照（ページ番号-添付ID）。
func AnonymousMayFetch(r *http.Request, pageID, name string) bool {
	if auth.CurrentUser(r) != nil {
		return true
	}
	body, err := os.ReadFile(BodyPath(pageID))
	if err != nil {
		return false
	}
	b := string(body)
	if strings.Contains(b, "/"+pageID+"/"+name) {
		return true
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	return stem != "" && strings.Contains(b, pageID+"-"+stem+`"`)
}

// AttachmentVersion は添付の版の印です（書いた時刻のミリ秒・36進）。住所に `?v=<印>` で添えます。
// 上書き（📝 ローカル編集・写真を小さくする・PDF の作り直し）で書いた時刻が変わると、印も変わります。
func AttachmentVersion(fi os.FileInfo) string {
	return strconv.FormatInt(fi.ModTime().UnixMilli(), 36)
}

// setAttachmentCache は、添付をブラウザの手元にどう残すかを決めます（2026-10-27）。
//
// 利用者:「単なるトンネルではなく、画像のキャッシュなどはできますか」→（VPS に貯めず、ブラウザに残す案に）「作ってください」。
//
//   - 住所の `?v=` が今の版の印と合う → その人のブラウザだけが、確かめずに使ってよい（中身は版の印で決まるので変わらない）。
//   - 印が無い・古い → 使う前に毎回「変わったか」を聞く（変わっていなければ 304 で中身を送らない）。上書きしたあとも
//     前の中身を見せ続けない。それまでは何も言っていなかったので、ブラウザが自分の判断で古い写しを使うことがありました。
//
// どちらも `private`——途中の中継（VPS の Caddy など）には残させません。添付は、読める人だけに配るものだからです。
//
// ただし、ログインしていない訪問者に配るとき（ここまで来た＝実効公開のページの添付）は `public` にして、途中の中継にも
// 残させてよいことにします（2026-10-27・【考察】キャッシュ制御 案C）。印が合っても **10分**（PublicCacheSeconds）までです——
// 非公開に戻したら10分で外から見えなくなる、ページと同じ決まりにするため（利用者:「公開の添付は10分にとどめる（勧め）でお願いします。」）。
// Vary: Cookie で、ログインした人の応答と取り違えさせません。
func setAttachmentCache(w http.ResponseWriter, r *http.Request, fi os.FileInfo) {
	current := false
	if v := r.URL.Query().Get("v"); v != "" && v == AttachmentVersion(fi) {
		current = true
	}
	if auth.CurrentUser(r) == nil {
		w.Header().Set("Vary", "Cookie")
		if current {
			w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(PublicCacheSeconds))
			return
		}
		w.Header().Set("Cache-Control", "public, no-cache")
		return
	}
	if current {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
}

// SetAttachmentCache は、添付から作ったもの（PDF の絵——cms の pdf_pages.go）の応答に、元の添付 fi と同じ残し方を付けます。
// 住所の ?v= は元の添付の版の印です。
func SetAttachmentCache(w http.ResponseWriter, r *http.Request, fi os.FileInfo) {
	setAttachmentCache(w, r, fi)
}

// PublicCacheSeconds は、ログインしていない訪問者への公開の応答（ページ・添付・sitemap.xml・robots.txt）を取っておかせてよい
// 秒数です（10分・2026-08-21 利用者:「キャッシュにとって必要な時間を知りませんが、10分程度でどうでしょう？」）。
// 公開⇄非公開の切り替えが外から見えるまでの遅れでもあります。cms の publicCacheSeconds はこの値です。
const PublicCacheSeconds = 600

// inlineImageTypes はインライン配信を許すラスタ画像の拡張子と Content-Type です。
// SVG はここに入れません（上の分岐で不活性化して配るため）。
var inlineImageTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
}
