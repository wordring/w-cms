package cms

// ─────────────────────────────────────────────────────────────────────────
// 取り込み係——回覧機構の4人目の段（2026-09-01）
//
// 既存の3段（観察係＝保存時・鏡型＝表示時・種まき＝新規作成時）に、
// **ファイル到着時**の段を足します。コアが知っているのは
// 「**受信箱にファイルが着いた**」という事実だけで、そのファイルが何を意味するか
// （メールなのか・発注書なのか）は取り込み係＝拡張の解釈です。
//
//	「発注書がやってきて物語が始まるのは当社の仕組みであって、汎用的なものでは
//	 ありません。w-cmsは他社にも使っていただきたいので、できればプラグインなどを
//	 使い、拡張として存在するものという立て付けにしたいです」（ユーザー・2026-09-01）
//
// 受信箱は**トップ直下の「受信箱」という名前のページ**です（テンプレート置き場と
// 同じ「名前が機能を決める」型——表示されている言葉が機能を表す。改名すると
// 機械が入れなくなるのも同じ受け入れ済みのリスク）。受信一覧は子ページ一覧の鏡が
// そのまま担い、整理は親の付け替えで行う——参照はページIDなので移動しても切れない。
//
// IntakeContext は**最小権限**です: 受信箱の下へページを作る・そのページへ添付を
// 置く、の2つだけ。既存ページの改変・索引への直接書き込みは渡しません
// （観察係に Replace が無いのと同じ、型で効く線）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// InboxTitle は受信箱ページの名前です。テンプレート置き場（TemplateRootTitle）と
// 同じく h1（ページ名）が正で、「設定で変えられるようにするか」も同じ未決を共有します
// （作業引き継ぎの未決1番。settings.json へ2つまとめて、が自然）。
const InboxTitle = "受信箱"

// InboxPageID はトップ直下の受信箱ページを返します（無ければ ok=false）。
// リクエスト時にしか呼ばれないためDBで足ります（テンプレートの isLeafPage と同じ理由）。
func InboxPageID() (string, bool) {
	var id int
	err := database.DB.QueryRow(
		`SELECT id FROM pages WHERE parent_id = 0 AND title = ? LIMIT 1`, InboxTitle).Scan(&id)
	if err != nil {
		return "", false
	}
	norm, ok := page.NormalizeID(strconv.Itoa(id))
	return norm, ok
}

// IntakeHandler は取り込み係の受け口です。宣言した拡張子のファイルが受信箱へ
// 着いたときに呼ばれ、ページを作って返します。
type IntakeHandler interface {
	Name() string
	Extensions() []string // 例: [".eml"]。小文字・ドットつき
	// OnFile はファイル1つを解釈してページを作ります。作れないファイル
	// （壊れている等）はエラーを返す——受信箱への素の添付にはしない
	// （中途半端に取り込むより、入らなかった事実が見えるほうがよい）。
	OnFile(ctx *IntakeContext, fileName string, content []byte) (pageID string, title string, err error)
}

// intakeRegistry は取り込み係の登録表です（拡張子ごとに1人・表引き）。
var intakeRegistry = map[string]IntakeHandler{}

// RegisterIntake は取り込み係を登録します（init から呼ぶ・回覧機構と同じ流儀）。
func RegisterIntake(h IntakeHandler) {
	for _, ext := range h.Extensions() {
		if prev, dup := intakeRegistry[ext]; dup {
			panic("取り込み係の拡張子が重複しています: " + ext + " (" + prev.Name() + " と " + h.Name() + ")")
		}
		intakeRegistry[ext] = h
	}
}

// intakeHandlerFor は拡張子の担当を返します（居なければ nil）。
func intakeHandlerFor(ext string) IntakeHandler {
	return intakeRegistry[strings.ToLower(ext)]
}

// IntakeContext は取り込み係に渡す最小権限の道具です。
type IntakeContext struct {
	InboxID  string // 受信箱のページID（作るページの親）
	Uploader string // 操作した人（新ページの所有者になる）
}

// CreatePage は受信箱の下へページを作ります。本文は保存経路と同じくサニタイズされ、
// 権限は受信箱から継承します（子ページ作成と同じ規則——受信箱の権限設定が
// 「受信物を誰が読めるか」をそのまま決める）。
func (c *IntakeContext) CreatePage(bodyHTML string) (string, error) {
	parentInt, err := strconv.Atoi(c.InboxID)
	if err != nil {
		return "", err
	}
	newID, err := reserveNewPageID(sql.NullInt64{Int64: int64(parentInt), Valid: true})
	if err != nil {
		return "", err
	}

	safeHTML := Sanitize(bodyHTML)
	dir := page.GetPageDir(newID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if err := page.WriteFileAtomic(filepath.Join(dir, newID+".html"), []byte(safeHTML), 0644); err != nil {
		return "", err
	}

	pp := page.GetPerms(parentInt)
	if err := page.EnsureSidecar(newID, c.Uploader, pp.Group, pp.Mode, c.InboxID); err != nil {
		log.Printf("取り込みページのサイドカー作成に失敗 page=%s: %v", newID, err)
	}
	if err := SyncIndex(newID, safeHTML); err != nil {
		return "", err
	}
	auth.Audit(c.Uploader, "intake.create", newID+" under "+c.InboxID)
	return newID, nil
}

// SaveAttachment は作ったページへ添付を置き、（生成ID, 配信アドレス）を返します。
// 生成IDは保存名＝URL＝リンクブロックの data-id の3役（storage.go）。
func (c *IntakeContext) SaveAttachment(pageID, ext string, content []byte) (id, href string, err error) {
	ext = strings.ToLower(ext)
	attachDir := page.AttachmentDir(pageID)
	if err := os.MkdirAll(attachDir, 0755); err != nil {
		return "", "", err
	}
	attachID := page.GeneratedAttachmentID(pageID, ext)
	name := attachID + ext
	if err := page.WriteFileAtomic(filepath.Join(attachDir, name), content, 0644); err != nil {
		return "", "", err
	}
	auth.Audit(c.Uploader, "attach", pageID+"/"+name)
	return attachID, page.AttachmentURLFor(pageID, name), nil
}

// UpdatePage は CreatePage で作った直後のページの本文を書き直します
// （添付を先に置いてからリンク入りの本文で確定する、という2段のため）。
// 対象は**この取り込みで作ったページ**に限る運用で、既存ページの改変には使いません。
func (c *IntakeContext) UpdatePage(pageID, bodyHTML string) error {
	safeHTML := Sanitize(bodyHTML)
	htmlPath := filepath.Join(page.GetPageDir(pageID), pageID+".html")
	if err := page.WriteFileAtomic(htmlPath, []byte(safeHTML), 0644); err != nil {
		return err
	}
	return SyncIndex(pageID, safeHTML)
}
