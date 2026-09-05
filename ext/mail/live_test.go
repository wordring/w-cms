package mail

// 実物のMicrosoft 365へ繋ぐ確認です。**既定では走りません**——ネットワークと
// 実アカウントが要り、しかも本当にメールが飛ぶためです。
//
//	WCMS_MAIL_LIVE_TEST=1 \
//	WCMS_MAIL_CLIENT_ID=... WCMS_MAIL_TENANT_ID=... \
//	go test ./ext/mail/ -run Live -v
//
// 宛先は**サインインした本人**です（外へは出ません）。走らせる前に
// `/api/mail/signin` でサインインを済ませておくこと（保存先は data/mail/）。

import (
	"context"
	"os"
	"testing"
	"time"

	"w-cms/internal/cms"
)

// TestLiveSendToSelf は、保存済みのトークンで実際に1通送れることを確かめます。
func TestLiveSendToSelf(t *testing.T) {
	if os.Getenv("WCMS_MAIL_LIVE_TEST") == "" {
		t.Skip("実送信の確認は WCMS_MAIL_LIVE_TEST=1 のときだけ走ります")
	}
	// トークンの保管先は `data/mail/` の**相対パス**（サーバーはリポジトリ直下で
	// 動く）。テストはパッケージのフォルダで走るので、そこへ揃えます。
	origWd, _ := os.Getwd()
	if err := os.Chdir("../.."); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })

	username := os.Getenv("WCMS_MAIL_LIVE_USER")
	if username == "" {
		username = "a"
	}
	addr := SignedInAddress(username)
	if addr == "" {
		t.Fatalf("%s はメールアカウントにサインインしていません（先に /api/mail/signin）", username)
	}
	t.Logf("差出人＝宛先: %s", addr)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := sendViaSMTP(ctx, username, cms.OutgoingMail{
		To:      []string{addr},
		Subject: "w-cms 送信テスト",
		BodyText: "w-cms からの送信テストです。\n\n" +
			"このメールが届いていれば、SMTP（OAuth2）経由の送信が動いています。\n" +
			"送信時刻: " + time.Now().In(time.Local).Format(time.RFC3339) + "\n",
		// **In-Reply-To が立つことが SMTP へ変えた理由**なので実物でも載せます。
		InReplyTo: "<probe-thread-check@w-cms.local>",
		Attachments: []cms.MailAttachment{{
			Name:     "添付の確認.txt",
			MIMEType: "text/plain",
			Content:  []byte("添付の確認用です。\n"),
		}},
	})
	if err != nil {
		t.Fatalf("送信に失敗しました: %v", err)
	}
	t.Log("送信しました（受信箱と送信済みアイテムを確認してください）")
}
