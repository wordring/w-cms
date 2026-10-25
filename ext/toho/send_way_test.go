package toho

import (
	stdhtml "html"
	"regexp"
	"strings"
	"testing"

	"w-cms/ext/comm/contacts"
)

// 送る欄の送り方の欄（2026-10-26・send_way.go）——最初に選ばれているのは連絡帳の勧め（無ければ1つ目のメール）・
// ボタンの文字はその送り方のもの・選んだ送り方の押す先（隠した、いままでのボタン・メールの欄）が同じ送る欄にあること。
func TestSendWayPick(t *testing.T) {
	selected := func(html string) string {
		m := regexp.MustCompile(`<option data-target="([^"]*)" data-go="[^"]*" selected>`).FindStringSubmatch(html)
		if m == nil {
			return ""
		}
		return stdhtml.UnescapeString(m[1])
	}
	order := func(w contacts.SendWay) string {
		return orderSendFormHTML(41, OrderSendCounts{}, nil, "", w)
	}
	if got := selected(order(contacts.SendWay{})); got != "details.order-mail" {
		t.Errorf("勧めの無い相手で最初に選ばれているもの: %q（メールのはず）", got)
	}
	h := order(contacts.SendWay{Way: contacts.SendWayFax, From: "ひかりレーザー", Tag: "部材発注書の送り方"})
	if got := selected(h); got != "[data-fax-purpose]" {
		t.Errorf("勧めが FAX なのに最初に選ばれているもの: %q", got)
	}
	if !strings.Contains(h, `class="chip-btn send-way-go">▶ 送る</button>`) || !strings.Contains(h, "ひかりレーザーの「部材発注書の送り方」") {
		t.Errorf("FAX の勧めのボタンか一言が違います:\n%s", h)
	}
	h = order(contacts.SendWay{Way: contacts.SendWayHand, From: "x", Tag: "送り方"})
	if got := selected(h); got != `[data-order-sent="`+sendByHand+`"]` || !strings.Contains(h, "✓ 記録する</button>") {
		t.Errorf("勧めが手渡しなのに: %q\n%s", got, h)
	}
	// 押す先がどれも送る欄の中にある（「▶ 送る」は送る欄の中で探す）。
	for _, want := range []string{`<details class="order-mail">`, `data-fax-purpose="` + OrderMailPurpose + `"`,
		`data-order-sent="` + sendByHand + `"`, `data-order-sent="` + sendByFax + `"`, `class="order-send send-way-box"`} {
		if !strings.Contains(h, want) {
			t.Errorf("送る欄に %q がありません", want)
		}
	}
	// 設定の使い道は最初に選ぶものだけ——メールの欄の開き方は変えない。
	if strings.Contains(order(contacts.SendWay{Way: contacts.SendWayMail, From: "x", Tag: "送り方"}), `<details class="order-mail" open`) {
		t.Errorf("勧めがメールでメールの欄を開いています（設定は最初に選ぶものだけに使う）")
	}
}
