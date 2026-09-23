package comm

// ─────────────────────────────────────────────────────────────────────────
// .eml の取り込み係——汎用寄りの同梱拡張（2026-09-01）
//
// メール（RFC 5322 / MIME）を**通信記録ページ**へ変換します。
//
//	<h1>件名</h1>
//	<dl data-type="tags">差出人・宛先・CC・返信先（表示名とアドレスを別のタグに分ける）・
//	                     受信日時（ISO 8601・ローカル時刻＋オフセット）・
//	                     メッセージID・返信元メッセージID（スレッドの親）</dl>
//	本文（text/plain を段落へ）
//	📎 添付（files/ へ保存・リンクは生成ID・download 属性が元名を運ぶ。
//	        人が落とす口と同じ検査を通し、ZIP は中身も1つずつ添付にする——2026-09-17）
//	📧 受信原本（生の .eml。解釈で落ちるものがあるので原本を残す＝やり直せる）
//
// メタはすべて**可変タグ**で持つ——名前：値なら索引に載り、検索も参照も
// 既存の仕組みがそのまま効く（発注書としての解釈は次の段＝下請け業務の
// 仕事で、この係は「メールを記録として残す」ことしかしない）。
//
// 和文メールの文字コード（ISO-2022-JP・Shift_JIS 等）は x/text で復号します
// （開発方針 §1 の承認済み依存——「通信記録の取り込みで直接利用予定」の予定が
// ここで現実になった）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"w-cms/internal/cms"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

func init() {
	RegisterIntake(emlIntake{})
}

type emlIntake struct{}

func (emlIntake) Name() string         { return "eml" }
func (emlIntake) Extensions() []string { return []string{".eml"} }

// charsetReader は MIME の charset 名から復号リーダーを作ります（ISO-2022-JP 等）。
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return nil, fmt.Errorf("未対応の文字コード: %s", charset)
	}
	return transform.NewReader(input, enc.NewDecoder()), nil
}

// wordDecoder は件名・差出人などのヘッダ（=?ISO-2022-JP?B?…?=）の復号器です。
var wordDecoder = mime.WordDecoder{CharsetReader: charsetReader}

// decodeHeader はヘッダ値を復号します（復号できない部分は原文のまま）。
func decodeHeader(s string) string {
	out, err := wordDecoder.DecodeHeader(s)
	if err != nil {
		return s
	}
	return out
}

// emlPart は展開した MIME パートです。
type emlPart struct {
	fileName  string // 添付なら元のファイル名（無ければ空＝本文候補）
	mediaType string
	body      []byte
}

// MessageIDTag は重複検知の鍵を置くタグの名前です。
//
// **専用テーブルは持ちません**（D-1 でドメイン表は全廃）。鍵の置き場は可変タグ、
// つまり**見える文字**しかありません——「見える文字がデータの手掛かり」
// （コンセプト §2）がここでも効いて、`vocab_index` の逆引き（`PagesByTag`）が
// そのまま重複判定になります。新しい仕組みは1つも要りません。
const MessageIDTag = "メッセージID"

// InReplyToTag は**スレッドの親**を指す鍵です（`In-Reply-To` ヘッダ。返信元メールの
// Message-ID が入る）。
//
// 名前が紛らわしいので注記します——**`Reply-To` とは別のヘッダ**です。
// `Reply-To` は「返信の宛先アドレス」（差出人と違う窓口に返させたいときに使う）で、
// 親子関係は作りません。作るのは `In-Reply-To` のほうです。
//
// **新しい仕組みは要りません**——値は Message-ID なので、親の記録ページは
// 重複検知と同じ逆引き（`PagesByTag(MessageIDTag, 値)`）1回で引けます。
// 取り込みの順にも依存しません（返信を先に落としても、あとで親が入れば繋がる）。
const InReplyToTag = "返信元メッセージID"

// 通信記録の相手と日時のタグ名です（2026-09-15 に定数へ）。
//
// **受信の取り込み（ここ）と送信の控え（ext/comm/mail の reply.go）が同じ名前で書く**ための
// 定数です。生の文字列で書いていたころ、2026-09-13 に `差出人アドレス` 等を廃止して
// 1人1タグへ移したとき、**受信側だけが追随し、送信の控えは廃止した名前で書き続けて**
// いました——読む側（返信の一覧・スレッド・未登録の連絡先）は送信の控えの相手を
// 黙って空で受け取ります。値は `名前 <アドレス>` かアドレスだけで、型（email）は
// `config/settings.json` の `vocabulary` が決めます。
const (
	FromTag       = "差出人"
	ToTag         = "宛先"
	CcTag         = "CC"
	ReplyToTag    = "返信先" // `Reply-To` ヘッダ。`InReplyToTag`（スレッドの親）とは別物
	ReceivedAtTag = "受信日時"
	SentAtTag     = "送信日時"
)

// SourceRef は重複検知の鍵（Message-ID）を返します。**鍵の取り出しは形式を知る
// 取り込み係の仕事**で、照合の仕組みはコアが持ちます（intake.go）。
//
// Message-ID が無いメールは珍しくない（手で組んだもの・一部のFAXゲートウェイ）。
// **鍵が無いことは異常ではない**ので ok=false を返すだけで、取り込みは止めません
// ——重複検知が効かないより、記録が残らないほうが困ります。
func (emlIntake) SourceRef(fileName string, content []byte) (string, string, bool) {
	msg, err := mail.ReadMessage(strings.NewReader(string(content)))
	if err != nil {
		return "", "", false // 壊れたメールは OnFile が理由つきで断る
	}
	id := strings.TrimSpace(msg.Header.Get("Message-ID"))
	if id == "" {
		return "", "", false
	}
	return MessageIDTag, id, true
}

// OnFile は .eml を通信記録ページにします。
func (emlIntake) OnFile(ctx *IntakeContext, fileName string, content []byte) (string, string, error) {
	msg, err := mail.ReadMessage(strings.NewReader(string(content)))
	if err != nil {
		return "", "", fmt.Errorf("メールとして読めません: %w", err)
	}

	subject := strings.TrimSpace(decodeHeader(msg.Header.Get("Subject")))
	if subject == "" {
		subject = "（件名なし）"
	}
	dateISO := ""
	var received time.Time
	if t, err := msg.Header.Date(); err == nil {
		// **届いた時刻**は置き場所（年フォルダ／月フォルダ）にも効きます
		// ——2024年のメールを今日取り込んでも2024年へ入るように。
		received = t
		// 日時は ISO 8601 が全域の正（要件 §3）。表記は**運用者のローカル時刻＋
		// オフセット**（例: 2026-09-01T10:30:00+09:00）——UTC の Z 表記は人が
		// 読み違えるため（2026-09-01 ユーザー要望「ISO表記の範囲内でローカル時刻に」）。
		dateISO = t.In(time.Local).Format(time.RFC3339)
	}

	parts, err := collectParts(msg.Header.Get("Content-Type"),
		msg.Header.Get("Content-Transfer-Encoding"), msg.Body)
	if err != nil {
		return "", "", err
	}

	// 先にページを作り（添付の置き場＝新ページのIDが要る）、添付を置いてから
	// リンク入りの本文で確定する。
	pageID, err := ctx.CreateDatedPage(received, "<h1>"+html.EscapeString(subject)+"</h1>")
	if err != nil {
		return "", "", err
	}

	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(subject) + "</h1>")
	b.WriteString(`<dl data-type="tags">`)
	// **向きとチャネルは直交する2軸**（2026-09-05）。向き＝受信／送信、
	// チャネル＝メール／FAX／電話。「送信 × FAX」が実際に要るので混ぜません。
	cms.WriteTag(&b, DirectionTag, DirectionIn)
	cms.WriteTag(&b, ChannelTag, ChannelMail)
	writeAddressTags(&b, FromTag, msg.Header.Get("From"))
	writeAddressTags(&b, ToTag, msg.Header.Get("To"))
	writeAddressTags(&b, CcTag, msg.Header.Get("Cc"))
	// 返信の宛先（差出人と違う窓口を指定してくることがある）。アドレス欄なので同じ扱い。
	writeAddressTags(&b, ReplyToTag, msg.Header.Get("Reply-To"))
	cms.WriteTag(&b, ReceivedAtTag, dateISO)
	// 重複検知の鍵。**見える文字として置く**——専用テーブルは無く、索引の逆引き
	// （pagesByTag）が判定そのものになる。人にとっては普段読まない値だが、
	// 「機械が使う値も本文にある」という原則を曲げてまで隠す理由が無い。
	cms.WriteTag(&b, MessageIDTag, strings.TrimSpace(msg.Header.Get("Message-ID")))
	// スレッドの親（In-Reply-To）。値は親メールの Message-ID なので、
	// PagesByTag(MessageIDTag, この値) で親の記録ページが引ける。
	cms.WriteTag(&b, InReplyToTag, strings.TrimSpace(msg.Header.Get("In-Reply-To")))
	// 添付の数。**一覧で「発注書が付いているか」を見るため**に索引へ載せます
	// （2026-09-05）——本文を開かないと分からない値だと、100件の一覧を出すたびに
	// 100個の本文を読むことになります。**受信原本（.eml）は数えません**
	// （必ず在るので、数えると全件が 1 から始まって手掛かりになりません）。
	// **受信原本（生の .eml）を先に置きます**（2026-09-03 ユーザー提案
	// 「マスタデータとしてEMLも保存してはどうでしょう」）。
	//
	// このあと作る本文は**解釈の産物**で、落ちているものがある——HTMLメールの見た目、
	// 拾っていないヘッダ、復号前の文字コード、署名。原本は全部を持っている
	// （添付も base64 で内包した完全アーカイブ）。**あとからやり直せる**ことが
	// 要件（§2.7④）なので、解釈を改良したときに読み直せる元が要る。
	//
	// 実ファイルと二重に持つことになるが、**役割が違うので有害な複製ではない**
	// （§8.1）——原本は証跡（不変・触らない）、添付は業務が使う作業用コピー。
	// 配信は `application/octet-stream`＋`attachment` なのでブラウザは解釈しない。
	// 先に置くのは、添付の目録（files/meta.json）の由来に**原本の保存名**を書くため（2026-09-17）。
	rawID, rawHref, err := ctx.SaveAttachment(pageID, fileName, "mail", content)
	if err != nil {
		return "", "", err // 原本が残せないなら取り込まない（証跡の無い記録は作らない）
	}
	rawName := rawID + ".eml"

	// 添付を置きます（数をタグへ書くため本文より先。ZIP は中身も展開して数えます）。
	attachHTML, attachCount, err := saveMailAttachments(ctx, pageID, rawName, parts)
	if err != nil {
		return "", "", err
	}
	cms.WriteTag(&b, AttachmentCountTag, attachCount)
	b.WriteString("</dl>")

	bodyWritten := false
	for _, p := range parts {
		if p.fileName == "" && !bodyWritten && strings.HasPrefix(p.mediaType, "text/plain") {
			b.WriteString(PlainTextBlockHTML(string(p.body)))
			bodyWritten = true
		}
	}
	b.WriteString(attachHTML)

	b.WriteString(`<p data-id="` + html.EscapeString(rawID) + `">📧 受信原本 <a href="` +
		html.EscapeString(rawHref) + `" download="` + html.EscapeString(fileName) + `">` +
		html.EscapeString(fileName) + `</a></p>`)

	if err := ctx.UpdatePage(pageID, b.String()); err != nil {
		return "", "", err
	}
	return pageID, subject, nil
}

// addressParser は差出人・宛先の解析器です（表示名の =?ISO-2022-JP?B?…?= も復号する）。
var addressParser = mail.AddressParser{WordDecoder: &wordDecoder}

// writeAddressTags はアドレス欄を**相手1人につき1つのタグ**として書きます。
//
//	差出人：山田 太郎 <yamada@example.co.jp>
//
// 値の形は `formatAddress` が決め、**畳んだ値がアドレスだけ**になります
// （列型 `email`・`normalizeEmailTag`）。だから索引の逆引きは**アドレスの完全一致**で
// 効きます（`PagesByTag`）——揺れるのは名前のほうなので、鍵はアドレスに置きます。
// 実データの宛先は
// `㈱東邦金属工業所 南　様 (admin@example-works.co.jp) <admin@example-works.co.jp>` のように
// 表示名の中にもアドレスが紛れる形で、部分一致に頼るのは筋が悪い
// （2026-09-03 ユーザー指摘「メールアドレス単体でDBに入れるほうが検索漏れが無くなる」）。
//
// ⚠ **2026-09-13 まで `差出人` と `差出人アドレス` の2つに割っていました**。
// 覆した理由は `formatAddress` のコメントにあります（このコメントも、そのとき
// 直し忘れて2表の形を説明したまま残っていました——2026-09-14 に直した）。
//
// 宛先が複数あれば**タグを繰り返します**（多値は繰り返し・[【一覧】語彙.md] §4）。
// 解析できないヘッダは原文のまま1つのタグに落とします——**記録を落とすより、
// 検索しにくい形でも残すほうがよい**（通信箱は不変アーカイブ）。
func writeAddressTags(b *strings.Builder, name, raw string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	list, err := addressParser.ParseList(raw)
	if err != nil || len(list) == 0 {
		cms.WriteTag(b, name, decodeHeader(raw))
		return
	}
	for _, a := range list {
		cms.WriteTag(b, name, formatAddress(a.Name, a.Address))
	}
}

// formatAddress は `名前 <アドレス>` を組み立てます（名前が無ければアドレスだけ）。
//
// **1人1タグです**（2026-09-13 ユーザー:「メールについては、メールアドレスを正規の
// コンポーネントとして扱えば良いのでは？名前などを変えても、アドレスが同じなら
// 届くのですから」）。もとは `差出人` と `差出人アドレス` の2つに割っていました:
//
//   - タグの**27%が重複**でした（実データ1172行のうち319行）
//   - **隣接で対応づける**必要がありました——`CCアドレス` の持ち主は1つ手前の `CC`。
//     CCが3人いると名前がずれる不具合を同日に踏んでいます（3人とも先頭の名前になった）
//   - 索引の鍵が**名前**になっていました。揺れるのは名前のほうなのに
//
// 形はメールヘッダそのもの（RFC 5322）です。`.eml` に書いてある形と同じなので、
// 取り込みは分解して組み直すのではなく**写すだけ**になりました。
// 引くときの鍵は畳んだ値＝アドレスだけ（`ColEmail`・vocab.go）。
func formatAddress(name, address string) string {
	name = strings.TrimSpace(name)
	address = strings.TrimSpace(address)
	if address == "" {
		return name
	}
	if name == "" {
		return address
	}
	return name + " <" + address + ">"
}

// PlainTextBlockHTML は平文の本文を `<pre>` 1つにします。
//
// **1行1段落にしていたのをやめました**（2026-09-05 ユーザー:「テキストメールは、
// 一行ごとに段落とするのではなく、preタグにしてはどうでしょう」）。段落に割ると
// **空行・字下げ・引用の `>` の位置が落ちます**——メールの平文は「そう書かれた形」に
// 意味があり、見積の桁揃えや署名の罫線は、崩すと読めません。
//
// `pre` はサニタイザの許可要素なので、保存も表示もそのまま通ります。折り返しは
// CSS（`white-space: pre-wrap`）が受け持ちます——**横スクロールにはしません**。
//
// 末尾の空行だけ落とします（メールソフトが付ける余白で、意味を持たないため）。
func PlainTextBlockHTML(text string) string {
	t := strings.ReplaceAll(text, "\r\n", "\n")
	t = strings.TrimRight(t, " \t\n")
	if strings.TrimSpace(t) == "" {
		return ""
	}
	return "<pre>" + html.EscapeString(t) + "</pre>"
}

// saveMailAttachments はメールの添付を files/ へ置き、リンクブロックのHTMLと数を返します。
//
// ── 検査は人が落とす口と同じ（2026-09-17）──
//
// それまでメールの添付は**拡張子の許可リストも中身の検査も通さずに**保存していました
// （`.exe` でも `.html` でも届いたまま files/ へ）。配信側の守り（未知の種別は
// `octet-stream`＋`attachment`＋`nosniff`）で実害は出ませんが、人が落とす口
// （`serveIntake`・`UploadFileHandler`）との非対称でした。いまは
// `cms.AttachmentExtAccepted`（PDF・画像・`attachment_extensions`）と
// `cms.GuardUploadContent`（PDFのマジックナンバー・画像の種別一致とEXIF除去）を通し、
// 通らなかったものは**保存せず、名前と理由を本文に残します**——受信原本（.eml）が
// 丸ごと持っているので、失うものはありません。
//
// ── ZIP は展開します（2026-09-17 ユーザー:「ZIP本体がEMLの中に存在するのであれば、
// ZIPを展開しても良いと思います」）──
//
// 中のファイルを1つずつ添付にします。**ZIP 本体も残します**（目録の表示がそのまま
// 効く・届いた形の証拠）。展開してよい根拠は原本が ZIP を持っていること——だから
// 人が手で落とした ZIP は展開しません（`cms.ExpandZip` の冒頭）。
// 中のパスは**フォルダを含めて**リンク文字にします。実データのフォルダ名は
// `Q055-サンプル装置仕様　図面` のように装置名称を含みます。
// `download` 属性（保存名）はファイル名だけです。
//
// 数は**保存したブロックの数**です（ZIP 1つ＋中の13件なら 14）。**0 なら空を返します**
// ——WriteTag が空を書かないので、添付の無い記録にはタグが付きません。
func saveMailAttachments(ctx *IntakeContext, pageID, rawName string, parts []emlPart) (string, string, error) {
	var b strings.Builder
	n := 0
	accepts := func(name string) bool { return cms.AttachmentExtAccepted(filepath.Ext(name)) }
	for _, p := range parts {
		if p.fileName == "" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(p.fileName))
		if !accepts(p.fileName) {
			writeUnsavedNote(&b, p.fileName, "受け付けない形式")
			continue
		}
		content, err := cms.GuardUploadContent(p.fileName, p.body)
		if err != nil {
			writeUnsavedNote(&b, p.fileName, err.Error())
			continue
		}
		id, href, err := ctx.SaveAttachment(pageID, p.fileName, "mail:"+rawName, content)
		if err != nil {
			return "", "", err
		}
		n++
		if ext != ".zip" {
			writeAttachmentBlock(&b, id, href, p.fileName, p.fileName, "")
			continue
		}

		members, skipped, zerr := cms.ExpandZip(content, accepts)
		if zerr != nil {
			writeAttachmentBlock(&b, id, href, p.fileName, p.fileName,
				"（ZIPとして読めないので展開していません）")
			continue
		}
		var inner strings.Builder
		saved := 0
		for _, m := range members {
			mc, err := cms.GuardUploadContent(m.Name, m.Content)
			if err != nil {
				skipped = append(skipped, cms.ZipSkipped{Name: m.Name, Reason: err.Error()})
				continue
			}
			// 由来は「どの ZIP の、中のどのパスか」。名前（ファイル名だけ）は目録の name に。
			mid, mhref, err := ctx.SaveAttachment(pageID, zipBaseName(m.Name), "zip:"+id+ext+"/"+m.Name, mc)
			if err != nil {
				return "", "", err
			}
			saved++
			writeAttachmentBlock(&inner, mid, mhref, zipBaseName(m.Name), "↳ "+m.Name, "")
		}
		n += saved
		writeAttachmentBlock(&b, id, href, p.fileName, p.fileName,
			"（中の"+strconv.Itoa(saved)+"件を下に展開）")
		b.WriteString(inner.String())
		if len(skipped) > 0 {
			var parts []string
			for _, s := range skipped {
				parts = append(parts, s.Name+"（"+s.Reason+"）")
			}
			b.WriteString("<p>↳ 展開しなかったもの: " + html.EscapeString(strings.Join(parts, "・")) +
				"。受信原本の中にあります。</p>")
		}
	}
	if n == 0 {
		return b.String(), "", nil
	}
	return b.String(), strconv.Itoa(n), nil
}

// writeAttachmentBlock は添付のリンクブロックを書きます。
// リンク文字（label）と保存名（downloadName）は別です——ZIP の中身はフォルダつきの
// パスを見せ、保存するときはファイル名だけにします。
func writeAttachmentBlock(b *strings.Builder, id, href, downloadName, label, note string) {
	b.WriteString(`<p data-id="` + html.EscapeString(id) + `">📎 <a href="` +
		html.EscapeString(href) + `" download="` + html.EscapeString(downloadName) + `">` +
		html.EscapeString(label) + `</a>` + html.EscapeString(note) + `</p>`)
}

// writeUnsavedNote は、保存しなかった添付の名前と理由を本文に残します（黙って落とさない）。
func writeUnsavedNote(b *strings.Builder, name, reason string) {
	b.WriteString("<p>📎 " + html.EscapeString(name) + "（" + html.EscapeString(reason) +
		"。保存していません——受信原本の中にあります）</p>")
}

// zipBaseName は ZIP の中のパスからファイル名だけを返します（`/` と `\` の両方を区切りと見る）。
func zipBaseName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		return name[i+1:]
	}
	return name
}

// collectParts は MIME を展開して（本文候補と添付の）平らな一覧にします。
// multipart は入れ子ごと辿り、text/* は宣言された文字コードから UTF-8 へ復号します。
func collectParts(contentType, cte string, body io.Reader) ([]emlPart, error) {
	if contentType == "" {
		contentType = "text/plain"
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType, params = "text/plain", nil
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return nil, fmt.Errorf("multipart に boundary がありません")
		}
		mr := multipart.NewReader(body, boundary)
		var out []emlPart
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return out, nil // 途中で壊れていても、読めた分は取り込む
			}
			sub, err := collectParts(p.Header.Get("Content-Type"),
				p.Header.Get("Content-Transfer-Encoding"), p)
			if err == nil {
				// パート自身のファイル名（添付の印）を優先する
				if fn := partFileName(p); fn != "" {
					for i := range sub {
						if sub[i].fileName == "" {
							sub[i].fileName = fn
						}
					}
				}
				out = append(out, sub...)
			}
			p.Close()
		}
		return out, nil
	}

	// 単一パート: 転送符号化を解いてから、text/* は文字コードを復号する。
	r := body
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "base64":
		r = newBase64Reader(r)
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(mediaType, "text/") {
		if cs := params["charset"]; cs != "" && !strings.EqualFold(cs, "utf-8") {
			if dec, err := charsetReader(cs, strings.NewReader(string(raw))); err == nil {
				if converted, err := io.ReadAll(dec); err == nil {
					raw = converted
				}
			}
		}
	}
	return []emlPart{{mediaType: mediaType, body: raw}}, nil
}

// partFileName は添付パートの元の名前を返します（復号済み・パス区切りは落とす。無ければ空）。
//
// ⚠ **Go 標準の `Part.FileName()` は使いません**（2026-09-17）。あれは RFC 2047 の
// 復号**前**に `filepath.Base` を掛けるので、符号化文字列の中の `/`（base64 の1文字）で
// 名前が切れます——実データで `K120-DC装置D…20240913.zip` の形の名前が
// `XkxMISEbKEIyMDI0MDkx?= 3.zip` になりました（和文の名前は必ず符号化されるので、
// 和文の添付の何割かがこうなる）。ここでは**復号してから**区切りを落とします。
// 見る順は `Content-Disposition` の `filename` → `Content-Type` の `name`（標準と同じ。
// RFC 2231 の `filename*=` は `mime.ParseMediaType` が `filename` に畳む）。
func partFileName(p *multipart.Part) string {
	for _, h := range [][2]string{{"Content-Disposition", "filename"}, {"Content-Type", "name"}} {
		_, params, err := mime.ParseMediaType(p.Header.Get(h[0]))
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(params[h[1]]); v != "" {
			return zipBaseName(decodeHeader(v))
		}
	}
	return ""
}

// newBase64Reader は改行入りの base64 本文を読むリーダーです。
func newBase64Reader(r io.Reader) io.Reader {
	return base64.NewDecoder(base64.StdEncoding, &newlineStripper{r: r})
}

// newlineStripper は CR/LF を読み飛ばします（MIME の base64 は 76 桁で折られる）。
type newlineStripper struct{ r io.Reader }

func (n *newlineStripper) Read(p []byte) (int, error) {
	buf := make([]byte, len(p))
	for {
		m, err := n.r.Read(buf)
		w := 0
		for _, c := range buf[:m] {
			if c != '\r' && c != '\n' {
				p[w] = c
				w++
			}
		}
		if w > 0 || err != nil {
			return w, err
		}
	}
}
