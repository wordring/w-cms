package cms

// ─────────────────────────────────────────────────────────────────────────
// ファイルの取り込み係——解釈しないが記録は残す（2026-09-03）
//
// 受信箱へ置かれたファイル（PDF・画像・図面・Office…）を**通信記録ページ**へ
// 変換します。**中身は解釈しません**——それは人が「🤖 解析」を押したときの仕事。
//
//	<h1>（ファイル名から）</h1>
//	<dl data-type="tags">チャネル・取り込み日時・内容ハッシュ</dl>
//	📎 受け取ったPDF（＝これ自身が受信原本。解釈していないので原本と作業用の別が無い）
//
// **同じ口で2つの運用に効きます**（2026-09-03 ユーザー:「オンプレミスサーバで
// CMSとFAXサーバを運用しようと思います。しかし、最初はVPSを借りて始めるので、
// FAXのPDFをはる形かもしれません」）:
//
//   - **VPS期**: 人が受信箱へPDFをドロップする
//   - **オンプレ期**: FAXサーバの橋渡しが同じ口（`/api/upload-pdf` または
//     `/api/upload-file`）へ POST する
//
// どちらも `serveIntake` → この係、という**同じ道**を通ります。本体に足すものは
// 何もなく、変わるのは「誰が押すか」だけ——§3.1 が言っていた
// 「外部ブリッジが受信して同じ口へPOST・本体無変更」がそのまま効きます。
//
// **メールと違って解釈しません**。PDF の中身は読まず（読むのは人が押す 🤖 解析）、
// 封筒メタも無い——差出人も受信時刻も PDF からは分からないからです。
// 分かることだけを書き、**分からないことは書きません**（`受信日時` は空のまま。
// 人が知っていれば足すし、橋渡しが将来メタを添えて POST してもよい）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"crypto/sha256"
	"encoding/hex"
	"html"
	"path/filepath"
	"strings"
	"time"
)

func init() {
	// PDF と画像は**FAXの道**（複合機の scan-to-PDF／FAXサーバの出力）。
	// §2 のチャネル表がこの2つを FAX の入口と定めている。
	RegisterIntake(fileIntake{channel: "FAX"}, ".pdf", ".png", ".jpg", ".jpeg", ".webp", ".gif")
	// それ以外（DXF・Office・ZIP…）は**既定の担当**へ。届いた事実は記録するが、
	// どの経路で来たかは分からないので `チャネル` は書かない（分かることだけ書く）。
	RegisterIntakeFallback(fileIntake{})
}

// fileIntake は「解釈しないファイル」の取り込み係です。
// channel が空でなければ `チャネル` タグを書きます。
type fileIntake struct{ channel string }

func (f fileIntake) Name() string {
	if f.channel != "" {
		return "file:" + f.channel
	}
	return "file"
}

// Extensions は RegisterIntake の引数で渡すので、ここでは空を返します
// （既定の担当としても登録されるため、自分では拡張子を宣言しない）。
func (fileIntake) Extensions() []string { return nil }

// ContentHashTag は中身から作る重複検知の鍵です。
//
// メールの `Message-ID` にあたる自然な鍵が PDF には無いので、**中身そのもの**を
// 鍵にします（同じファイルを2度ドロップしても記録は二重にならない）。
// 再送されて改めてスキャンされた FAX はバイト列が変わるので別の記録になります
// ——それは正しい（別の受信だから）。
const ContentHashTag = "内容ハッシュ"

// ChannelTag はどの経路で届いたかです。メール・FAX・電話を**横断して**
// 「取引先Aからの受信」を引けるようにするための軸（§6）。
const ChannelTag = "チャネル"

// SourceRef は重複検知の鍵（中身のSHA-256）を返します。
func (fileIntake) SourceRef(fileName string, content []byte) (string, string, bool) {
	sum := sha256.Sum256(content)
	return ContentHashTag, hex.EncodeToString(sum[:]), true
}

// OnFile は PDF を通信記録ページにします。
func (f fileIntake) OnFile(ctx *IntakeContext, fileName string, content []byte) (string, string, error) {
	// 題はファイル名から採る——FAXサーバは `20260903_1430_0312345678.pdf` のように
	// 受信時刻や発信番号を名前へ入れることが多く、**人が見て分かる唯一の手掛かり**
	// になっている。中身から推測はしない（それは 🤖 解析の仕事）。
	title := strings.TrimSpace(strings.TrimSuffix(fileName, filepath.Ext(fileName)))
	if title == "" {
		title = "受信文書"
	}

	// **届いた時刻が分からないので取り込んだ時刻で分けます**——FAXやPDFは封筒メタを
	// 持たないため（メールと違い受信日時が読めない）。分からないことを捏造せず、
	// タグの名前も「取り込み日時」のままにしてあります。
	pageID, err := ctx.CreateDatedPage(time.Now(), "<h1>"+html.EscapeString(title)+"</h1>")
	if err != nil {
		return "", "", err
	}

	// 拡張子は**受け取った名前から採る**。ここを決め打ちにすると中身と種別が
	// 食い違い、配信の Content-Type も解析ボタンの判定も狂う（FAX専用だったころの
	// `.pdf` 決め打ちが、汎用化のときに取り残されていた——実データのDXFが
	// `.pdf` として保存されて発覚。2026-09-03）。
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == "" {
		ext = ".bin"
	}
	id, href, err := ctx.SaveAttachment(pageID, ext, content)
	if err != nil {
		return "", "", err
	}

	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(`<dl data-type="tags">`)
	writeTag(&b, ChannelTag, f.channel) // 空なら書かれない
	// **取り込み日時であって受信日時ではない**——PDF は受け取った時刻を持たない。
	// 名前を正確にしておけば、あとで `受信日時` を足したときに矛盾しない。
	writeTag(&b, "取り込み日時", time.Now().In(time.Local).Format(time.RFC3339))
	sum := sha256.Sum256(content)
	writeTag(&b, ContentHashTag, hex.EncodeToString(sum[:]))
	b.WriteString("</dl>")
	// 📎 の形にするのは意味がある——閲覧モードのクロームが「▶ 表示」と
	// 「🤖 解析」を付ける。**PDFが発注書とは限らない**（図面・見積書・納品書・
	// 案内・DM…）ので、解析は「発注書か？」と**人が尋ねる手段**であって、
	// 押せば必ず何かが生まれる分類器ではない——発注書でなければ何も作らず
	// そう答えるだけで、記録ページは資料として残る（§4.1「既定は資料として
	// 保存・表示、抽出は高価値・低コストなものに限定する」）。
	b.WriteString(`<p data-id="` + html.EscapeString(id) + `">📎 <a href="` +
		html.EscapeString(href) + `" download="` + html.EscapeString(fileName) + `">` +
		html.EscapeString(fileName) + `</a></p>`)

	if err := ctx.UpdatePage(pageID, b.String()); err != nil {
		return "", "", err
	}
	return pageID, title, nil
}
