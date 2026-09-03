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
	"fmt"
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

// SourceRefFinder は「このファイルは取り込み済みか」を判定する鍵を出せる
// 取り込み係が**任意で**実装する追加の口です（【考察】通信記録処理.md §8）。
//
// **鍵の取り出しは形式を知る取り込み係、照合の仕組みはコア**という分担です。
// メールなら Message-ID、FAX なら受信機の通番——どれが鍵かは形式ごとに違い、
// コアには決められません。逆に「その鍵を持つページが既にあるか」は索引の
// 逆引き1回で、形式に依りません。
//
// 実装しなければ重複検知が効かないだけで、取り込みは従来どおり動きます。
type SourceRefFinder interface {
	// SourceRef は鍵のタグ名と値を返します。鍵を持たないファイル
	// （Message-ID の無いメール等）は ok=false——**異常ではありません**。
	SourceRef(fileName string, content []byte) (name, value string, ok bool)
}

// ExistingIntakePage は鍵（名前：値）を持つページを索引から逆引きします。
//
// **判定に使うのは通信記録ページの存在だけ**です（§2.7 の決定）。「このメールから
// 受注ページを作ったか」は使いません——使うと、受注ページを取り消したあとに
// 再実行できなくなり、可逆性が失われます。
//
// 専用テーブルは持たないので（D-1）、鍵は本文の可変タグにあり、逆引きは
// `vocab_index` の値索引がそのまま効きます。生テキストで引くのは、
// 生が正本だからです（アーキテクチャとDBスキーマ §9.1）。
func ExistingIntakePage(tagName, value string) (string, bool) {
	if tagName == "" || value == "" {
		return "", false
	}
	ids, err := PagesByTag(database.DB, tagName, value)
	if err != nil || len(ids) == 0 {
		return "", false
	}
	norm, ok := page.NormalizeID(strconv.Itoa(ids[0]))
	if !ok {
		return "", false
	}
	return norm, true
}

// intakeRegistry は取り込み係の登録表です（拡張子ごとに1人・表引き）。
var intakeRegistry = map[string]IntakeHandler{}

// RegisterIntake は取り込み係を登録します（init から呼ぶ・回覧機構と同じ流儀）。
// 担当する拡張子は h.Extensions() が宣言しますが、**引数でも渡せます**——
// 1つの実装を設定違いで複数の拡張子へ登録する場合に使います（intake_file.go）。
func RegisterIntake(h IntakeHandler, exts ...string) {
	if len(exts) == 0 {
		exts = h.Extensions()
	}
	for _, ext := range exts {
		if prev, dup := intakeRegistry[ext]; dup {
			panic("取り込み係の拡張子が重複しています: " + ext + " (" + prev.Name() + " と " + h.Name() + ")")
		}
		intakeRegistry[ext] = h
	}
}

// intakeFallback は拡張子の担当が居ないときの既定の担当です。
//
// 受信箱は「**何かが届いた**」という1つの事実を受ける場所なので、種類が何であれ
// 記録は残るべきです（2026-09-03 ユーザー:「その受け口ではメールや、PDF、DXFも
// 受け付け」）。拡張子ごとの担当は「その形式を**解釈できる**者」で、
// 既定の担当は「解釈しないが記録は残す」者。
var intakeFallback IntakeHandler

// RegisterIntakeFallback は既定の担当を登録します（init から呼ぶ・1人だけ）。
func RegisterIntakeFallback(h IntakeHandler) {
	if intakeFallback != nil {
		panic("取り込みの既定の担当が二重に登録されています: " + intakeFallback.Name() + " と " + h.Name())
	}
	intakeFallback = h
}

// intakeHandlerFor は拡張子の担当を返します（居なければ既定の担当・それも無ければ nil）。
func intakeHandlerFor(ext string) IntakeHandler {
	if h, ok := intakeRegistry[strings.ToLower(ext)]; ok {
		return h
	}
	return intakeFallback
}

// IntakeContext は取り込み係に渡す最小権限の道具です。
type IntakeContext struct {
	InboxID  string // 受信箱のページID（作るページの親）
	Uploader string // 操作した人（新ページの所有者になる）

	// created はこの取り込みで作ったページのID（作成順）。SaveAttachment と
	// UpdatePage の対象をここに限ることで、「既存ページの改変は渡さない」の線を
	// 運用ではなく型と検査で守ります。
	created []string
}

// isCreated はこの取り込みで作ったページかを返します。
func (c *IntakeContext) isCreated(pageID string) bool {
	for _, id := range c.created {
		if id == pageID {
			return true
		}
	}
	return false
}

// CreatePage は受信箱の下へページを作ります。本文は保存経路と同じくサニタイズされ、
// 権限は受信箱から継承します（子ページ作成と同じ規則——受信箱の権限設定が
// 「受信物を誰が読めるか」をそのまま決める）。
func (c *IntakeContext) CreatePage(bodyHTML string) (string, error) {
	return c.createUnder(c.InboxID, bodyHTML)
}

// createUnder はページ作成の取り込み側の入口です（作成の芯＋監査＋作成済みの記録）。
func (c *IntakeContext) createUnder(parentID, bodyHTML string) (string, error) {
	newID, err := CreateChildPage(parentID, c.Uploader, bodyHTML)
	if err != nil {
		return "", err
	}
	auth.Audit(c.Uploader, "intake.create", newID+" under "+parentID)
	c.created = append(c.created, newID)
	return newID, nil
}

// CreateChildPage はページ作成の芯です（権限は親から継承・サニタイズ・索引まで。
// 監査は呼び手が録る——取り込みと解析で行為名が違うため）。
// 取り込み（IntakeContext）とPDF解析ボタン（analyze_pdf.go）が共用します。
func CreateChildPage(parentID, owner, bodyHTML string) (string, error) {
	parentInt, err := strconv.Atoi(parentID)
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
	if err := page.EnsureSidecar(newID, owner, pp.Group, pp.Mode, parentID); err != nil {
		log.Printf("取り込みページのサイドカー作成に失敗 page=%s: %v", newID, err)
	}
	if err := SyncIndex(newID, safeHTML); err != nil {
		return "", err
	}
	return newID, nil
}

// SaveAttachment は作ったページへ添付を置き、（生成ID, 配信アドレス）を返します。
// 生成IDは保存名＝URL＝リンクブロックの data-id の3役（storage.go）。
func (c *IntakeContext) SaveAttachment(pageID, ext string, content []byte) (id, href string, err error) {
	if !c.isCreated(pageID) {
		return "", "", fmt.Errorf("この取り込みで作ったページにしか添付できません: %s", pageID)
	}
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
// 対象は**この取り込みで作ったページ**に限ります（検査で強制。既存ページの改変口にしない）。
func (c *IntakeContext) UpdatePage(pageID, bodyHTML string) error {
	if !c.isCreated(pageID) {
		return fmt.Errorf("この取り込みで作ったページしか書き直せません: %s", pageID)
	}
	safeHTML := Sanitize(bodyHTML)
	htmlPath := filepath.Join(page.GetPageDir(pageID), pageID+".html")
	if err := page.WriteFileAtomic(htmlPath, []byte(safeHTML), 0644); err != nil {
		return err
	}
	return SyncIndex(pageID, safeHTML)
}
