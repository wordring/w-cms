package comm

// ─────────────────────────────────────────────────────────────────────────
// 取り込み係——回覧機構の4人目の段（2026-09-01）
//
// 既存の3段（観察係＝保存時・鏡型＝表示時・種まき＝新規作成時）に、
// **ファイル到着時**の段を足します。コアが知っているのは
// 「**通信箱にファイルが着いた**」という事実だけで、そのファイルが何を意味するか
// （メールなのか・発注書なのか）は取り込み係＝拡張の解釈です。
//
//	「発注書がやってきて物語が始まるのは当社の仕組みであって、汎用的なものでは
//	 ありません。w-cmsは他社にも使っていただきたいので、できればプラグインなどを
//	 使い、拡張として存在するものという立て付けにしたいです」（ユーザー・2026-09-01）
//
// 通信箱は**トップ直下の「通信箱」という名前のページ**です（テンプレート置き場と
// 同じ「名前が機能を決める」型——表示されている言葉が機能を表す。改名すると
// 機械が入れなくなるのも同じ受け入れ済みのリスク）。受信一覧は子ページ一覧の鏡が
// そのまま担い、整理は親の付け替えで行う——参照はページIDなので移動しても切れない。
//
// IntakeContext は**最小権限**です: 通信箱の下へページを作る・そのページへ添付を
// 置く、の2つだけ。既存ページの改変・索引への直接書き込みは渡しません
// （観察係に Replace が無いのと同じ、型で効く線）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"w-cms/internal/cms"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

func init() {
	// **通信箱への到着を取り込み係へ回す受け口**（2026-09-15 にアップロード口から裏返した）。
	cms.RegisterUploadInterceptor(intakeUpload)
}

// intakeUpload は、アップロード先が通信箱なら取り込み係へ回します。
//
// **通信箱への到着は取り込み係へ回覧する**（2026-09-01）。通信箱の本文は変更しない
// （子ページが生まれるだけ）ので編集ロックは要らない。取り込み係が居ない拡張子は
// false を返し、通常の添付として保存される。
func intakeUpload(w http.ResponseWriter, r *http.Request, pageID, formField string) bool {
	inboxID, ok := MailBoxPageID()
	if !ok || inboxID != pageID {
		return false
	}
	return serveIntake(w, r, inboxID, formField)
}

// serveIntake は通信箱へのアップロードを取り込み係に回します。
// 担当が居なければ false（通常の添付経路へ戻す）。
// formField はファイルが入っているフォーム欄の名前です（汎用の口は "file"、
// PDF専用の口は "pdf_file"）——**通信箱への到着はどちらの口からも同じ取り込み係へ
// 回す**ため、口ごとの違いはこの引数だけに閉じ込めます。
func serveIntake(w http.ResponseWriter, r *http.Request, inboxID, formField string) bool {
	file, header, err := r.FormFile(formField)
	if err != nil {
		return false
	}
	defer file.Close()
	h := intakeHandlerFor(strings.ToLower(filepath.Ext(header.Filename)))
	if h == nil {
		return false // 担当なし＝ただの添付
	}
	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "File read error", http.StatusInternalServerError)
		return true
	}
	// **種類ごとの検査はここで通す**——通信箱は1つの口で全部を受けるので、
	// 専用の口が持っていた守り（PDFのマジックナンバー・画像の種別検証とEXIF除去）を
	// 迂回させない（2026-09-03「受け口の一本化」）。
	content, err = cms.GuardUploadContent(header.Filename, content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return true
	}
	uploader := "system"
	if u := auth.CurrentUser(r); u != nil {
		uploader = u.Username
	}
	// 重複検知は**取り込み係を呼ぶ前**に行う（【考察】通信記録処理.md §8）。
	// 同じメールの再ドロップは普通に起きる（送り直し・取りこぼしの確認）が、
	// 黙って2枚できるとどちらが正かを人が見分けられない——しかも参照タグ
	// `受信元` の指す先が2つに割れる。**作ってから消す**のではなく、
	// 作らずに既存を指すのが正しい（可逆性は §2.7 の決定）。
	if f, ok := h.(SourceRefFinder); ok {
		if name, value, found := f.SourceRef(header.Filename, content); found {
			if existing, dup := ExistingIntakePage(name, value); dup {
				auth.Audit(uploader, "intake.duplicate", existing+" ("+name+"="+value+")")
				w.Header().Set("Content-Type", "application/json")
				resp := map[string]any{
					"success": true, "intake": true, "duplicate": true,
					"title": header.Filename,
				}
				// 読めない相手へ存在を教えない（匿名の404統一と同じ規律）。
				// 取り込み済みという事実だけは返す——でないと「なぜ入らないのか」が
				// 分からなくなる。
				if n, err := strconv.Atoi(existing); err == nil &&
					page.CanView(auth.CurrentUser(r), n) {
					resp["page_id"] = existing
				}
				json.NewEncoder(w).Encode(resp)
				return true
			}
		}
	}

	ctx := &IntakeContext{InboxID: inboxID, Uploader: uploader}
	pageID, title, err := h.OnFile(ctx, header.Filename, content)
	if err != nil {
		http.Error(w, "取り込めませんでした: "+err.Error(), http.StatusBadRequest)
		return true
	}
	// 添付PDFの解釈（発注書なら受注ページ生成）はここでは**しない**——
	// 「自動ではなくボタンのclickなどで解析が始まると良い」（2026-09-01 ユーザー決定・
	// §3 人間ゲート型）。記録ページの📎に出る「🤖 解析」ボタン（analyze_pdf.go）が担う。
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "intake": true, "page_id": pageID, "title": title,
	})
	return true
}

// MailBoxTitle は通信箱ページの名前です。テンプレート置き場（TemplateRootTitle）と
// 同じく h1（ページ名）が正で、「設定で変えられるようにするか」も同じ未決を共有します
// （作業引き継ぎの未決1番。settings.json へまとめて、が自然）。
//
// **受信箱と送信箱は 2026-09-05 に1つへ統合しました。** ユーザー:「受信と送信を
// 一つのフォルダに収めるのはどう思いますか？　一つのフォルダに時系列に並んでいて…」。
// 受信と送信は同じ案件のあいだを行き来するので、分けると**1件の経緯が2か所に散ります**。
// 向きは置き場所ではなく **`向き` のタグ**（DirectionTag）が表します。
const MailBoxTitle = "通信箱"

// MailBoxPageID はトップ直下の通信箱ページを返します（無ければ ok=false）。
// リクエスト時にしか呼ばれないためDBで足ります（テンプレートの isLeafPage と同じ理由）。
func MailBoxPageID() (string, bool) {
	return cms.TopLevelPageByTitle(MailBoxTitle)
}

// IntakeHandler は取り込み係の受け口です。宣言した拡張子のファイルが通信箱へ
// 着いたときに呼ばれ、ページを作って返します。
type IntakeHandler interface {
	Name() string
	Extensions() []string // 例: [".eml"]。小文字・ドットつき
	// OnFile はファイル1つを解釈してページを作ります。作れないファイル
	// （壊れている等）はエラーを返す——通信箱への素の添付にはしない
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
	ids, err := cms.PagesByTag(database.DB, tagName, value)
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
// intakeHandlerFor は拡張子の担当を返します（居なければ nil）。
//
// **既定の担当（何でも受ける係）は 2026-09-05 に取り止めました。** ユーザー:
// 「通信箱のPDF、DXF取り込みはやめましょう。メモに添付するようにしましょう」
// ——落ちてきた PDF を機械が `チャネル：FAX` と決めつけていたためです。PDF は
// スキャンからもダウンロードからも来るので、**経路は届いた本人しか知りません**。
//
// いまは人が「＋ 記録する」でチャネルと向きを選び、そのページへファイルを
// 落とします。**FAXサーバーを繋ぐときは機械専用の口を作ること**——人の手ドロップと
// 機械の投函を同じ口で受けると、また決めつけが戻ります。
func intakeHandlerFor(ext string) IntakeHandler {
	if h, ok := intakeRegistry[strings.ToLower(ext)]; ok {
		return h
	}
	return nil
}

// IntakeContext は取り込み係に渡す最小権限の道具です。
type IntakeContext struct {
	InboxID  string // 通信箱のページID（作るページの親）
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

// createUnder はページ作成の取り込み側の入口です（作成の芯＋監査＋作成済みの記録）。
//
// ⚠ **通信箱の直下へ作る口（`CreatePage`）は 2026-09-14 に撤去しました**——2026-09-05 に
// 記録が `通信箱／年／月` へ入る形になり、`CreateDatedPage` に取って代わられて
// 呼び手がゼロになっていたためです。直下へ作りたくなったら `createUnder(c.InboxID, …)`。
func (c *IntakeContext) createUnder(parentID, bodyHTML string) (string, error) {
	newID, err := cms.CreateChildPage(parentID, c.Uploader, bodyHTML)
	if err != nil {
		return "", err
	}
	auth.Audit(c.Uploader, "intake.create", newID+" under "+parentID)
	c.created = append(c.created, newID)
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
	safeHTML := cms.Sanitize(bodyHTML)
	htmlPath := filepath.Join(page.GetPageDir(pageID), pageID+".html")
	if err := page.WriteFileAtomic(htmlPath, []byte(safeHTML), 0644); err != nil {
		return err
	}
	return cms.SyncIndex(pageID, safeHTML)
}

// IntakeResult は取り込み1件の結果です。
type IntakeResult struct {
	PageID    string // 生まれた（または既にあった）ページ
	Title     string
	Duplicate bool // 既に取り込み済みだった
}

// IntakeFile は通信箱への到着を処理する**芯**です（担当探し・重複検知・ページ生成）。
//
// HTTPの口（serveIntake）と、メールの取り込み（ext/comm/mail）が共有します
// ——メールをIMAPで取ってきたときも、人が .eml をドロップしたときと**同じ道**を
// 通す必要があるからです。封筒タグ・スレッドの繋ぎ・添付の展開・重複検知は
// すべて取り込み係が持っているので、経路ごとに書き直すと必ず片方が古くなります。
//
// **中身の検査は呼ぶ側の責任**です（HTTPの口は checkIntakeContent を通す）。
// 担当が居ない拡張子なら ok=false を返します。
func IntakeFile(inboxID, uploader, fileName string, content []byte) (IntakeResult, bool, error) {
	h := intakeHandlerFor(strings.ToLower(filepath.Ext(fileName)))
	if h == nil {
		return IntakeResult{}, false, nil // 担当なし＝ただの添付
	}
	// 重複検知は**取り込み係を呼ぶ前**に行う（作ってから消すのではなく、作らない）。
	if f, ok := h.(SourceRefFinder); ok {
		if name, value, found := f.SourceRef(fileName, content); found {
			if existing, dup := ExistingIntakePage(name, value); dup {
				auth.Audit(uploader, "intake.duplicate", existing+" ("+name+"="+value+")")
				return IntakeResult{PageID: existing, Title: fileName, Duplicate: true}, true, nil
			}
		}
	}
	ctx := &IntakeContext{InboxID: inboxID, Uploader: uploader}
	pageID, title, err := h.OnFile(ctx, fileName, content)
	if err != nil {
		return IntakeResult{}, true, err
	}
	return IntakeResult{PageID: pageID, Title: title}, true, nil
}

// CreateDatedPage は通信箱の下の「年フォルダ／月フォルダ」へページを作ります。
//
// ユーザー:「メールは年フォルダと月フォルダで分類してはどうでしょうか」（2026-09-03）。
// 実測で年435通あり、過去分を入れると通信箱直下が数千枚になります——**通信箱が
// 「まだ分からないものの置き場」として機能しなくなる**ので、届いた時期で分けます。
//
//	通信箱
//	└ 2026年
//	  └ 09月
//	    └ お見積り依頼
//
// **時刻は「届いた時刻」を渡すこと**（取り込んだ時刻ではない）。2024年のメールを
// 今日取り込んでも2024年に入る必要があります。ゼロがゼロでない時刻を渡すのは
// 呼ぶ側の責任で、ゼロ値なら通信箱直下へ作ります（分からない時期を捏造しない）。
func (c *IntakeContext) CreateDatedPage(t time.Time, bodyHTML string) (string, error) {
	parent := c.InboxID
	if !t.IsZero() {
		var err error
		if parent, err = c.ensureDateFolder(t); err != nil {
			return "", err
		}
	}
	newID, err := c.createUnder(parent, bodyHTML)
	if err != nil {
		return "", err
	}
	// 並び順は**届いた時刻**（取り込んだ順ではない）。ISO表記なので文字列のまま
	// 正しく並びます。
	if !t.IsZero() {
		cms.SetSortKey(newID, t.In(time.Local).Format(time.RFC3339))
	}
	return newID, nil
}

// ensureDateFolder は「年／月」のページを必要なだけ作り、月フォルダのIDを返します。
func (c *IntakeContext) ensureDateFolder(t time.Time) (string, error) {
	// 置き場の作法は送信箱と共有します（ensureDateFolderUnder）——受信と送信で
	// フォルダの作り方が違うと、片方だけ直したときに気づけません。
	// 月は**ゼロ詰め**（`09月`）——名前で並べたときに順序が狂わないため。
	return cms.EnsureDateFolders(c.InboxID, c.Uploader, t)
}

// ChannelTag はどの経路で届いた（送った）かです。メール・FAX・電話・メモを
// **横断して**「取引先Aとのやりとり」を引けるようにするための軸。
//
// **人が選びます**（handler_memo.go）。2026-09-05 まではドロップされた PDF を
// 機械が `FAX` と決めつけていましたが、**取り止めました**——PDF はスキャンからも
// ダウンロードからも来るので、経路は届いた本人しか知りません。
const ChannelTag = "チャネル"

// AttachmentCountTag は添付の数です（0 なら書きません）。
//
// **一覧で「発注書が付いているか」を見るため**に索引へ載せます（2026-09-05）。
// 本文を開かないと分からない値だと、100件の一覧を出すたびに100個の本文を
// 読むことになります。受信原本（.eml）は数えません。
const AttachmentCountTag = "添付"

// DirectionTag は記録の向きです（値は DirectionIn / DirectionOut）。
//
// **置き場所ではなくタグで表します**（2026-09-05 ユーザー:「受信、送信、FAX、
// メール、電話などのタグを付けては？」）。`チャネル`（メール／FAX／電話）とは
// **直交する別の軸**なので、1つのタグに混ぜません——「送信 × FAX」（作った発注書を
// 業者へFAXする）が実際に要るためです。
//
// 向きが見える文字になったことで、**送信箱という置き場そのものが要らなくなりました**。
// 返信を返信元の子にしないのも同じ理由です——送受信は対等な出来事で、繋がりは
// 所有ではなく**参照**（返信元タグ）で表します。
const DirectionTag = "向き"

// DirectionIn / DirectionOut は向きの値です。
const (
	DirectionIn  = "受信"
	DirectionOut = "送信"
)

// TopLevelPageByTitle は page_lookup.go にあります（通信箱・テンプレート置き場・
// 取引先が共有する「名前が機能」の引き方）。

// ReplyToTag は「この記録はどの記録への返信か」を指す参照タグです。
// 値は返信元のページID——参照タグの文法（ref_render.go）に乗るのでリンクになり、
// **逆引き**（PagesByTag）で「この記録への返信」も引けます。
const ReplySourceTag = "返信元"

// CreateRecordPage は年フォルダ／月フォルダの下へ記録ページを1枚作ります。
//
// 通信箱の取り込み（IntakeContext.CreateDatedPage）と、送信の記録（ext/comm/mail）
// が共有します——**受信と送信で置き場の作法を変えない**ため。
func CreateRecordPage(rootID, owner string, t time.Time, bodyHTML string) (string, error) {
	parent := rootID
	if !t.IsZero() {
		var err error
		if parent, err = cms.EnsureDateFolders(rootID, owner, t); err != nil {
			return "", err
		}
	}
	newID, err := cms.CreateChildPage(parent, owner, bodyHTML)
	if err != nil {
		return "", err
	}
	if !t.IsZero() {
		cms.SetSortKey(newID, t.In(time.Local).Format(time.RFC3339))
	}
	return newID, nil
}
