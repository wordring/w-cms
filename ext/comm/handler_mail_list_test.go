package comm

import (
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// TestMailListReturnsBothDirectionsAndChain は、メールの一覧（2026-09-29）が**受信も送信も**返し、
// 返信の鎖を組む手掛かり（メッセージID・返信元メッセージID）を持ち、**読めない記録は落とす**ことを固定します。
// 利用者:「過去のメールを一覧で見る方法を作れませんか？受信送信両方、そしてIn reply toによる返信の連鎖も」。
func TestMailListReturnsBothDirectionsAndChain(t *testing.T) {
	setupIntakeTest(t)
	tags := func(pairs ...string) string {
		s := `<dl data-type="tags">`
		for i := 0; i+1 < len(pairs); i += 2 {
			s += "<dt>" + pairs[i] + "</dt><dd>" + pairs[i+1] + "</dd>"
		}
		return s + "</dl>"
	}
	for _, p := range []struct{ id, mode, body string }{
		// 受信（スレッドの頭）
		{"000501", "333", "<h1>見積のお願い</h1>" + tags(ChannelTag, ChannelMail, DirectionTag, DirectionIn,
			ReceivedAtTag, "2026-09-01T10:00:00+09:00", FromTag, "客 &lt;c@example.jp&gt;", MessageIDTag, "&lt;a@x&gt;")},
		// 送信（その返信）
		{"000502", "333", "<h1>Re: 見積のお願い</h1>" + tags(ChannelTag, ChannelMail, DirectionTag, DirectionOut,
			SentAtTag, "2026-09-02T09:00:00+09:00", ToTag, "客 &lt;c@example.jp&gt;", MessageIDTag, "&lt;b@x&gt;",
			InReplyToTag, "&lt;a@x&gt;", HandledTag, HandledNotNeeded)},
		// 読めない受信（他人には閉じている）
		{"000503", "300", "<h1>内緒</h1>" + tags(ChannelTag, ChannelMail, DirectionTag, DirectionIn,
			ReceivedAtTag, "2026-09-03T09:00:00+09:00", MessageIDTag, "&lt;c@x&gt;")},
		// メールではない記録（電話）は出さない
		{"000504", "333", "<h1>電話</h1>" + tags(ChannelTag, "電話", DirectionTag, DirectionIn)},
	} {
		if err := page.WriteSidecar(p.id, page.PageMeta{Owner: "alice", Mode: p.mode, ParentID: "000100"}); err != nil {
			t.Fatal(err)
		}
		if err := cms.SyncIndex(p.id, p.body); err != nil {
			t.Fatal(err)
		}
	}

	list, err := MailList(&auth.User{Username: "carol"})
	if err != nil {
		t.Fatalf("MailList エラー: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("読めるメールは2通のはず（読めない記録と電話は出さない）: %+v", list)
	}
	// 新しい順——送信（返信）が先、受信（頭）が後。
	out, in := list[0], list[1]
	if out.PageID != "000502" || out.Direction != DirectionOut || out.When != "2026-09-02T09:00:00+09:00" ||
		out.InReplyTo != "<a@x>" || out.Handled != HandledNotNeeded || len(out.To) != 1 {
		t.Errorf("送信の記録が違います: %+v", out)
	}
	if in.PageID != "000501" || in.Direction != DirectionIn || in.MessageID != "<a@x>" || in.Handled != "" ||
		len(in.From) != 1 || in.Title != "見積のお願い" {
		t.Errorf("受信の記録が違います: %+v", in)
	}
	// 鎖が組める——返信の「返信元メッセージID」が頭の「メッセージID」と一致する。
	if out.InReplyTo != in.MessageID {
		t.Errorf("返信の鎖を組めません: 返信元 %q・頭 %q", out.InReplyTo, in.MessageID)
	}
}
