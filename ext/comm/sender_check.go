package comm

// ─────────────────────────────────────────────────────────────────────────
// 送り元の確かめ（2026-10-27・【考察】コードの矛盾 §7.2 の5つ目）
//
// 利用者:「成り済ましは検証できますか？」→（受け取ったメールサーバーの判定を読む案を出して）「今後メールサーバーが変わります」
// 「一つでも駄目なら、ダメと警告して設定なしにするのは問題ありますか？」
// 「どれか1本でも dmarc か compauth が失敗なら ⚠・設定なし・✓は出さない形で作ってください。返信先が差出人と違うときの ⚠ と、
// 今ある記録への書き足しもお願いします」
//
//   - 受け取ったメールサーバーは、送り元が本物かを確かめて、ヘッダ `Authentication-Results`（RFC 8601）に書きます。
//     その**どれか1本でも** `dmarc=fail` か `compauth=fail`（Microsoft 365 の総合の判定）なら「失敗」です。
//   - ⚠ **出すのは失敗の ⚠ だけ。「本物」の ✓ は出しません。** 送る人は、自分のメールに偽の判定のヘッダ（「通過」）を
//     書き足せます。それでも、受け取ったサーバーが書いた「失敗」は消せないので、「1本でも失敗なら ⚠」は効きます。
//     「通過」を信じて ✓ を出すと、偽の判定で ✓ を付けさせられます。どのヘッダを信じるかの設定が要らないのも、このためです
//     （メールサーバーが変わっても、そのまま効く）。
//   - SPF・DKIM だけの失敗では ⚠ にしません（DMARC が通っていれば本物のことが多い——本番の 559 通で2通あった）。
//   - 判定の無いメール（社内どうし・判定を書かないサーバー）は「判定なし」と残します（⚠ は出さない・数は数えられる）。
//   - 返信先（Reply-To）が差出人と違う会社（ドメイン）のアドレスなら、記録の箱と返信の送る欄に ⚠ を出します
//     （返信は、黙って返信先へ届くため）。
//   - 判定はタグ `送り元の確かめ` に書きます（受け取ったメールだけ）。今ある記録には、原本（.eml）から読んで書き足します
//     （FillSenderChecks——メールの読み込みのたびに、まだ書いていない記録だけ）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// SenderCheckTag は送り元の確かめの結果を書くタグです。値は SenderCheck* の3つ。
const SenderCheckTag = "送り元の確かめ"

const (
	SenderCheckFailed = "失敗"   // どれかの判定が dmarc=fail か compauth=fail
	SenderCheckNoFail = "失敗なし" // 判定はあるが、失敗は無い（⚠ 本物とは言わない——✓ は出さない）
	SenderCheckNone   = "判定なし" // 判定のヘッダが無い（社内どうし・判定を書かないサーバー・原本が無い）
)

// authFailRe は判定の中の「失敗」です（`dmarc=fail`・`compauth=fail`。大小は問わない）。
var authFailRe = regexp.MustCompile(`(?i)(^|[\s;(])(dmarc|compauth)\s*=\s*fail\b`)

// senderCheckOf はメールのヘッダから送り元の確かめの結果を返します。
func senderCheckOf(h mail.Header) string {
	vals := h["Authentication-Results"] // ⚠ `ARC-Authentication-Results` は別の名前（数えない）
	if len(vals) == 0 {
		return SenderCheckNone
	}
	for _, v := range vals {
		if authFailRe.MatchString(v) {
			return SenderCheckFailed
		}
	}
	return SenderCheckNoFail
}

// addressDomain はアドレス（`名前 <a@b>` かアドレスだけ）のドメインを小文字で返します（読めなければ空）。
func addressDomain(s string) string {
	a := BareAddress(s)
	i := strings.LastIndex(a, "@")
	if i < 0 || i == len(a)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(a[i+1:]))
}

// replyToElsewhere は、返信先のどれかが差出人と違う会社（ドメイン）のアドレスなら、そのアドレスを返します。
func replyToElsewhere(from, replyTo []string) string {
	fd := ""
	if len(from) > 0 {
		fd = addressDomain(from[0])
	}
	if fd == "" {
		return ""
	}
	for _, r := range replyTo {
		if d := addressDomain(r); d != "" && d != fd {
			return BareAddress(r)
		}
	}
	return ""
}

// SenderWarnings は記録のタグから、人に見せる注意（⚠ の文）を返します（無ければ空）。記録の箱と返信の送る欄が使います。
func SenderWarnings(tags map[string][]string) []string {
	var out []string
	if v := tags[SenderCheckTag]; len(v) > 0 && strings.TrimSpace(v[0]) == SenderCheckFailed {
		out = append(out, "⚠ このメールは、受け取ったメールサーバーの送り元の確かめに失敗しています。差出人になりすましたメールかもしれません。"+
			"返信・支払い・振込先の変更などの前に、電話など別の道で確かめてください。")
	}
	if r := replyToElsewhere(tags[FromTag], tags[ReplyToTag]); r != "" {
		from := ""
		if v := tags[FromTag]; len(v) > 0 {
			from = BareAddress(v[0])
		}
		out = append(out, "⚠ 返信先（"+r+"）は、差出人（"+from+"）と違う会社のアドレスです。返信すると、返信先へ届きます。")
	}
	return out
}

// senderWarningsHTML は記録の箱のいちばん上に出す注意です（下書きには出さない）。
func senderWarningsHTML(_ *auth.User, pageIDInt int, draft bool) string {
	if draft {
		return ""
	}
	tags, err := cms.TagsOfPage(database.DB, pageIDInt)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, w := range SenderWarnings(tags) {
		b.WriteString(`<p class="mail-warn">` + stdhtml.EscapeString(w) + `</p>`)
	}
	return b.String()
}

func init() {
	RegisterRecordSection(RecordSection{Order: 5, Render: senderWarningsHTML})
}

// FillSenderChecks は、送り元の確かめを書いていない受け取ったメールの記録に、原本（.eml）から読んで書き足します。
// 書き足した記録の数を返します。原本が見つからない・読めない記録は「判定なし」にします（何度も読み直さないため）。
// 誰かが道具で握っている記録は飛ばします（次に呼ばれたときに書く）。
func FillSenderChecks(by string) (int, error) {
	rows, err := database.DB.Query(`SELECT DISTINCT d.page_id FROM page_tags d
		  JOIN page_tags c ON c.page_id = d.page_id AND c.name = ? AND c.value = ?
		 WHERE d.name = ? AND d.value = ?
		   AND NOT EXISTS (SELECT 1 FROM page_tags s WHERE s.page_id = d.page_id AND s.name = ?)`,
		ChannelTag, ChannelMail, DirectionTag, DirectionIn, SenderCheckTag)
	if err != nil {
		return 0, err
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	n := 0
	for _, id := range ids {
		if !InMailBox(id) { // 書き足すのは通信箱の記録だけ（外のページの本文は書き換えない）
			continue
		}
		pid := page.FormatID(id)
		result := senderCheckOfRaw(pid)
		wrote := false
		if cms.RewriteBodyUnlessHeld(pid, by, func(cur string) string {
			d := cms.EditBody(cur)
			d.SetTag(SenderCheckTag, result)
			out := d.HTML()
			wrote = out != cur
			return out
		}) == nil && wrote {
			n++
		}
	}
	return n, nil
}

// senderCheckOfRaw は記録の原本（取り込みが置いた .eml）を読んで、送り元の確かめの結果を返します。
func senderCheckOfRaw(pageID string) string {
	for stored, m := range cms.ReadAttachmentMetas(pageID) {
		if (m.Source != "mail" && m.Source != "") || !strings.EqualFold(filepath.Ext(stored), ".eml") {
			continue
		}
		path, ok := page.AttachmentPath(pageID, stored)
		if !ok {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		msg, err := mail.ReadMessage(f)
		f.Close()
		if err != nil {
			continue
		}
		return senderCheckOf(msg.Header)
	}
	return SenderCheckNone
}
