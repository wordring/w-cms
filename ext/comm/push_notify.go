package comm

// 届いた記録を、通知を受けると決めた人の端末へ知らせる（2026-10-21 夜・docs/考察/【考察】通信箱の改善.md §7.5）。
//
// > 2026-10-21 利用者:「スマホに知らせて貰えれば最高です。」
//
// 電話に出たとき・FAX が届いたとき・メールが届いたとき（NotifyArrival）に、通知を受ける端末を持つ人（画面の「🔔」で
// 受けると決めた人）のうち、その記録を読める人へ、コアの Web Push（cms.SendPush）で送ります。押すと、その記録が開きます。

import (
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

func init() {
	RegisterArrivalListener(pushArrival)
}

// arrivalPushTitle は通知の題です（チャネルごと）。
func arrivalPushTitle(channel string) string {
	switch channel {
	case ChannelPhone:
		return "📞 電話に出ました——メモを書けます"
	case ChannelFax:
		return "📠 FAX が届きました"
	case ChannelMail:
		return "✉️ メールが届きました"
	}
	return "📥 通信箱に記録が届きました"
}

// arrivalMessage は、届いた記録1つの通知です（記録が決まっていなければ、通信箱を開く）。
func arrivalMessage(a Arrival) (cms.PushMessage, int) {
	m := cms.PushMessage{Title: arrivalPushTitle(a.Channel), Body: strings.TrimSpace(a.Title)}
	if id, ok := page.NormalizeID(strings.TrimSpace(a.PageID)); ok && id != "" {
		m.URL, m.Tag = "/"+id, "arrival-"+id
		return m, atoiPage(id)
	}
	if box, ok := MailBoxPageID(); ok {
		m.URL = "/" + box
		return m, atoiPage(box)
	}
	m.URL = "/"
	return m, 0
}

// pushArrival は、通知を受ける人のうち、その記録を読める人へ送ります。
func pushArrival(a Arrival) {
	m, pid := arrivalMessage(a)
	for _, name := range cms.PushSubscribers() {
		u, err := auth.LookupUser(name)
		if err != nil || u == nil {
			continue
		}
		if pid > 0 && !page.CanView(u, pid) {
			continue
		}
		cms.SendPush(name, m)
	}
}
