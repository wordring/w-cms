package comm

// ─────────────────────────────────────────────────────────────────────────
// 親ページID——通信記録の親子を書く（2026-10-09）
//
// 利用者:「メール関連のタグはメールヘッダそのまま In-Reply-To などとして記録し、それとは別に親子関係を表すタグとして
// 『親ページID』タグを付けるのはどうでしょう？もちろん、通信記録の前後をたどるには、基本的に『親ページID』を検索します」。
// ヘッダの名前にするのは Message-ID・In-Reply-To だけ（差出人・宛先などは FAX・電話と共通の日本語のまま）。
//
//   - `親ページID`（ParentPageTag・参照）は、この記録が返信している記録のページ。メールどうしでも、FAX・電話・メモの記録
//     から返信したときでも同じ形（元の記録に Message-ID が無くても書ける）。
//   - 書くところ: メールの取り込み（In-Reply-To と同じ Message-ID を持つ記録が在れば——intake_eml.go）・w-cms から送った
//     返信の控え（返信元のページ——ext/comm/mail の reply.go）。
//   - **親があとから取り込まれたら、既にある子に書き足す**（利用者が「書き足す」を選んだ）——受信の箱を先に読むと、
//     こちらが送ったメール（親）より相手の返信（子）が先に入るため。誰かが子を開いて編集していたら飛ばし、次に読み込んだ
//     ときに書き足す（FixRecordTags）。
//     （2026-10-12 から、ブラウザは編集権を取らないので、飛ばすのは道具〔`/api/lock`〕が子を握っているときだけ。
//     人の保存とは `cms.RewriteBody` の書き込みの錠で順に並びます。）
//     いま書き足すのは、メールの読み込みのたびの `FixRecordTags` の②です（親を取り込んだその場で書き足す口 `LinkChildrenOf` は、
//     どこからも呼ばれていなかったので 2026-10-20 に消した）。
//   - 取り込み済みの記録（それまでの名前「メッセージID」「返信元メッセージID」「返信元」）は、メールの読み込みのたびに
//     最初に直す（FixRecordTags——直すものが無ければ索引を引くだけ）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"log"
	"regexp"
	"strings"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// それまでのタグの名前（2026-10-09 まで）——取り込み済みの記録を直すためと、直す前の重複の見分けのためだけに残す。
const (
	legacyMessageIDTag   = "メッセージID"
	legacyInReplyToTag   = "返信元メッセージID"
	legacyReplySourceTag = "返信元"
)

// legacyTagNames は古い名前 → 新しい名前です。
var legacyTagNames = map[string]string{
	legacyMessageIDTag:   MessageIDTag,
	legacyInReplyToTag:   InReplyToTag,
	legacyReplySourceTag: ParentPageTag,
}

// ExistingMailRecord は Message-ID が msgID の記録を引きます（重複の見分け）。直す前の記録（古い名前「メッセージID」）も
// 見ます——直し終わる前に読み込むと、取り込み済みのメールを二重に入れてしまうため。
func ExistingMailRecord(msgID string) (string, bool) {
	if id, ok := ExistingIntakePage(MessageIDTag, msgID); ok {
		return id, true
	}
	return ExistingIntakePage(legacyMessageIDTag, msgID)
}

// ParentRecordOf は In-Reply-To の値（親の Message-ID）から親の記録のページIDを引きます（self は除く・無ければ空）。
func ParentRecordOf(inReplyTo string, self int) string {
	inReplyTo = strings.TrimSpace(inReplyTo)
	if inReplyTo == "" {
		return ""
	}
	// 値そのままで引き、無ければ中の <…> を1つずつ引く（「<a@x> <b@y>」「<a@x> (コメント)」の形でも親へつながる・
	// 2026-10-22 の見直し）。
	tries := []string{inReplyTo}
	for _, m := range MessageIDTokenRe.FindAllString(inReplyTo, -1) {
		if m != inReplyTo {
			tries = append(tries, m)
		}
	}
	for _, v := range tries {
		ids, err := cms.PagesByTag(database.DB, MessageIDTag, v)
		if err != nil {
			continue
		}
		for _, id := range ids {
			if id != self && InMailBox(id) { // 親は通信箱の記録だけ（外のページに Message-ID を書いても親にならない・2026-10-26）
				return page.FormatID(id)
			}
		}
	}
	return ""
}

// MessageIDTokenRe は、ヘッダの中の <…> の1つです（Message-ID の形・空白を含まない）。取り込みの親子の結びと、メールの返信が
// 同じ形を別々に書いていたのを寄せた（2026-10-29）。
var MessageIDTokenRe = regexp.MustCompile(`<[^<>\s]+>`)

// NoSubject は、件名の無いメールの記録の題です（取り込み・下書き・送信の控えで同じ文字）。
const NoSubject = "（件名なし）"

// SubjectOrNone は、件名 s の前後の空白を落とし、空なら NoSubject を返します。
func SubjectOrNone(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return NoSubject
	}
	return s
}

// withParentTag は本文の最初の可変タグの並びに 親ページID を足した本文を返します（既にあるか、並びが無ければ何もしない）。
func withParentTag(body, parentID string) (string, bool) {
	if strings.Contains(body, "<dt>"+ParentPageTag+"</dt>") {
		return body, false
	}
	at := cms.PageTagListEnd(body) // 節の中の並び（添付の図面ブロックなど）には足さない
	if at < 0 {
		return body, false
	}
	var b strings.Builder
	cms.WriteTag(&b, ParentPageTag, parentID)
	return body[:at] + b.String() + body[at:], true
}

// addParent は記録 childID に 親ページID を書き足します。誰かが開いて編集していれば飛ばします（false——次の回で）。
// （2026-10-12 から、飛ばすのは道具〔`/api/lock`〕が握っているときだけです。）
func addParent(childID int, parentID, by string) bool {
	added := false
	err := cms.RewriteBodyUnlessHeld(page.FormatID(childID), by, func(cur string) string {
		out, ok := withParentTag(cur, parentID)
		added = ok
		return out
	})
	return err == nil && added
}

// legacyDtRe は古い名前のタグの見出し（`<dt>…</dt>`——中の空白は許す）です。
var legacyDtRe = regexp.MustCompile(`<dt>(\s*)(` + regexp.QuoteMeta(legacyInReplyToTag) + `|` + regexp.QuoteMeta(legacyMessageIDTag) + `|` +
	regexp.QuoteMeta(legacyReplySourceTag) + `)(\s*)</dt>`)

// renameLegacyTags は本文の古い名前のタグを新しい名前にした本文を返します（見出しの文字がちょうどその名前のものだけ）。
func renameLegacyTags(body string) string {
	return legacyDtRe.ReplaceAllStringFunc(body, func(m string) string {
		sub := legacyDtRe.FindStringSubmatch(m)
		return "<dt>" + sub[1] + legacyTagNames[sub[2]] + sub[3] + "</dt>"
	})
}

// FixRecordTags は取り込み済みの記録を今の形へ直します——古い名前のタグを新しい名前へ（①）、親ページID の無い子に、
// 引ける親を書き足す（②）。誰かが開いて編集している記録は飛ばします（次に呼ばれたときに直す。
// 2026-10-12 からは、道具〔`/api/lock`〕が握っている記録だけを飛ばします）。
// 直した記録の数を返します。直すものが無ければ索引を引くだけです（メールの読み込みのたびに最初に呼ぶ）。
func FixRecordTags(by string) (renamed, linked int, err error) {
	// ① 古い名前（先に読み切ってから書く——行を読みながら書き換えない）。
	rows, err := database.DB.Query(`SELECT DISTINCT page_id FROM page_tags WHERE name IN (?, ?, ?)`,
		legacyMessageIDTag, legacyInReplyToTag, legacyReplySourceTag)
	if err != nil {
		return 0, 0, err
	}
	var olds []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			olds = append(olds, id)
		}
	}
	rows.Close()
	for _, id := range olds {
		if !InMailBox(id) { // 直すのは通信箱の記録だけ（2026-10-26。外のページの本文は書き換えない・読み切ってから絞る）
			continue
		}
		changed := false
		if cms.RewriteBodyUnlessHeld(page.FormatID(id), by, func(cur string) string {
			out := renameLegacyTags(cur)
			changed = out != cur
			return out
		}) == nil && changed {
			renamed++
		}
	}
	// ② 親ページID の無い子（In-Reply-To がある）に、いま引ける親を書き足す。
	rows, err = database.DB.Query(`SELECT c.page_id, c.value FROM page_tags c
		 WHERE c.name = ? AND c.value <> ''
		   AND NOT EXISTS (SELECT 1 FROM page_tags p WHERE p.page_id = c.page_id AND p.name = ?)`,
		InReplyToTag, ParentPageTag)
	if err != nil {
		return renamed, 0, err
	}
	type orphan struct {
		id        int
		inReplyTo string
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if rows.Scan(&o.id, &o.inReplyTo) == nil {
			orphans = append(orphans, o)
		}
	}
	rows.Close()
	for _, o := range orphans {
		if !InMailBox(o.id) {
			continue
		}
		if parent := ParentRecordOf(o.inReplyTo, o.id); parent != "" && addParent(o.id, parent, by) {
			linked++
		}
	}
	// ③ 送り元の確かめを書いていない受け取ったメールに、原本から読んで書き足す（2026-10-27・sender_check.go）。
	if n, err := FillSenderChecks(by); err != nil {
		log.Printf("送り元の確かめを書き足せませんでした: %v", err)
	} else if n > 0 {
		log.Printf("送り元の確かめを %d 件の記録に書き足しました", n)
	}
	return renamed, linked, nil
}
