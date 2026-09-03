package mailgraph

// ─────────────────────────────────────────────────────────────────────────
// Microsoft Graph でメールを送る（2026-09-03）
//
// 外部依存はありません——net/http と encoding/json だけです。
//
// **スレッドの繋ぎには制限があります。** Graph の `sendMail` は
// `In-Reply-To` / `References` を立てられません（`internetMessageHeaders` は
// `x-` で始まる独自ヘッダしか受け付けない）。返信を相手のメールソフトで
// 同じスレッドに並べるには、元のメールを探して `createReply` を使う必要があり、
// それには `Mail.ReadWrite`（下書きを作る）が要ります。
// いまは件名の `RE:` だけで、機械的な繋ぎは w-cms 側の記録に持ちます
// ——**送った控えは返信元の子ページになる**ので、社内では辿れます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"w-cms/internal/cms"
)

// graphRecipient は宛先1件です。
type graphRecipient struct {
	EmailAddress struct {
		Address string `json:"address"`
	} `json:"emailAddress"`
}

func recipients(addrs []string) []graphRecipient {
	out := make([]graphRecipient, 0, len(addrs))
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		var r graphRecipient
		r.EmailAddress.Address = a
		out = append(out, r)
	}
	return out
}

// graphAttachment は添付1件です。Graph は base64 で受け取ります。
type graphAttachment struct {
	ODataType    string `json:"@odata.type"`
	Name         string `json:"name"`
	ContentType  string `json:"contentType"`
	ContentBytes string `json:"contentBytes"`
}

// sendMailBody は /me/sendMail の本体です。
type sendMailBody struct {
	Message struct {
		Subject string `json:"subject"`
		Body    struct {
			ContentType string `json:"contentType"`
			Content     string `json:"content"`
		} `json:"body"`
		ToRecipients []graphRecipient  `json:"toRecipients"`
		CcRecipients []graphRecipient  `json:"ccRecipients,omitempty"`
		Attachments  []graphAttachment `json:"attachments,omitempty"`
	} `json:"message"`
	// **送信控えを相手（M365）側にも残します**——w-cms が唯一の記録だと、
	// 普段のメールソフトから見たときに送った形跡が消えて混乱します。
	SaveToSentItems bool `json:"saveToSentItems"`
}

// graphSendLimit は Graph の sendMail に載せられる添付の目安です。
// これを超えるものはアップロードセッションが要り、いまは扱いません
// （送れないことを黙って隠さず、はっきり断ります）。
const graphSendLimit = 3 << 20

// sendViaGraph は1通送ります。
func sendViaGraph(ctx context.Context, username string, msg cms.OutgoingMail) error {
	token, err := accessToken(ctx, username)
	if err != nil {
		return err
	}

	var body sendMailBody
	body.SaveToSentItems = true
	body.Message.Subject = msg.Subject
	body.Message.Body.ContentType = "Text" // 本文は平文（HTMLメールは作らない）
	body.Message.Body.Content = msg.BodyText
	body.Message.ToRecipients = recipients(msg.To)
	body.Message.CcRecipients = recipients(msg.Cc)

	total := 0
	for _, a := range msg.Attachments {
		total += len(a.Content)
		if total > graphSendLimit {
			return errors.New("添付が大きすぎて送れません（合計3MiBまで）")
		}
		body.Message.Attachments = append(body.Message.Attachments, graphAttachment{
			ODataType:    "#microsoft.graph.fileAttachment",
			Name:         a.Name,
			ContentType:  a.MIMEType,
			ContentBytes: base64.StdEncoding.EncodeToString(a.Content),
		})
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://graph.microsoft.com/v1.0/me/sendMail", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK {
		return nil
	}
	// **理由をそのまま伝えます**——「権限が足りない」のか「宛先が不正」なのかが
	// 分からないと運用側が直しようがありません。トークンは含めません。
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
	return errors.New("メールを送れませんでした（" + resp.Status + "）: " + graphErrorMessage(detail))
}

// graphErrorMessage は Graph のエラー本文から人が読む部分だけ取り出します。
func graphErrorMessage(body []byte) string {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Error.Message != "" {
		return e.Error.Code + " " + e.Error.Message
	}
	return strings.TrimSpace(string(body))
}
