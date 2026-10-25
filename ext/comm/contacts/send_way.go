package contacts

// 相手ごとの送り方の勧め（2026-10-26）。
//
// 送る相手によって、いちばん合う送り方（メール・FAX・手渡し）が違います。連絡帳の人や組織のページにタグ「送り方」を
// 書いておくと、書類の送る欄が、その送り方を最初に選んだ形で出します（勧めるだけで、ほかの送り方も選べる）。
//
// > 2026-10-26 利用者:「送信する相手によって最適な通信手段がかわります。あらかじめ連絡帳でプリセット可能ですか?
// > これは最初に選択されているというような勧告のプリセットです」
// > 「担当者に通信メソッドのプリセットが無い場合、組織のプリセットを探すのも良いと思います。」
// > 「書類ごとの設定があればそれを使い、総称的な設定しがなければ、それを用いれば良いと思います」
//
// 書類ごとの送り方は「<書類>の送り方」（例: 部材発注書の送り方）のタグに書きます。書類の名前は、送る欄を出す拡張が渡す
// （連絡帳は書類の種類を知らない）。探す順は、人の書類ごと → 人の総称（送り方）→ 組織の書類ごと → 組織の総称。

import (
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// SendWayTag は、連絡帳の人・組織のページの、勧める送り方のタグです（型は設定の語彙で enum）。
const SendWayTag = "送り方"

// 送り方の値です（設定の語彙の values と同じ並び）。
const (
	SendWayMail = "メール"
	SendWayFax  = "FAX"
	SendWayHand = "手渡し"
)

// SendWay は勧める送り方と、それを書いていたページの題（人か組織）・タグの名前です。Way が空なら、勧めは無い。
type SendWay struct {
	Way  string
	From string
	Tag  string
}

// SendWayTagFor は、書類 doc の送り方のタグの名前です（doc が空なら総称の「送り方」）。
func SendWayTagFor(doc string) string {
	if doc = strings.TrimSpace(doc); doc != "" {
		return doc + "の" + SendWayTag
	}
	return SendWayTag
}

// PreferredSendWay は、組織 org（連絡帳の題）の人 person に書類 doc を送るときの勧める送り方を返します。
// 人のページ、組織のページの順に見て、それぞれ書類ごとの送り方（doc が空なら飛ばす）、総称の「送り方」の順に探す
// （person が空・見つからないときは組織だけ）。組織が見つからない・どこにも書いていない・値が3つのどれでもないときは空。
func PreferredSendWay(user *auth.User, org, person, doc string) SendWay {
	orgID, ok := PartnerByTitle(user, strings.TrimSpace(org))
	if !ok {
		return SendWay{}
	}
	if person = strings.TrimSpace(person); person != "" {
		for _, p := range PersonsOf(user, orgID) {
			if cms.NormalizeText(p.Title) != cms.NormalizeText(person) {
				continue
			}
			if w, tag := sendWayOfPage(p.ID, doc); w != "" {
				return SendWay{Way: w, From: p.Title, Tag: tag}
			}
			break
		}
	}
	if w, tag := sendWayOfPage(orgID, doc); w != "" {
		return SendWay{Way: w, From: cms.PageTitleByID(atoi(orgID)), Tag: tag}
	}
	return SendWay{}
}

// sendWayOfPage は、ページ id の書類 doc の送り方（無ければ総称の「送り方」）と、それを書いていたタグの名前です。
// 値が3つのどれでもないタグは、書いていないものとして次を見る。
func sendWayOfPage(id, doc string) (string, string) {
	tags, err := cms.TagsOfPage(database.DB, atoi(id))
	if err != nil {
		return "", ""
	}
	names := []string{SendWayTag}
	if strings.TrimSpace(doc) != "" {
		names = []string{SendWayTagFor(doc), SendWayTag}
	}
	for _, name := range names {
		switch w := strings.TrimSpace(cms.FirstTag(tags, name)); w {
		case SendWayMail, SendWayFax, SendWayHand:
			return w, name
		}
	}
	return "", ""
}

func atoi(id string) int {
	n, _ := strconv.Atoi(id)
	return n
}
