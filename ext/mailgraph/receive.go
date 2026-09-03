package mailgraph

// ─────────────────────────────────────────────────────────────────────────
// Microsoft Graph からメールを取ってくる（2026-09-03）
//
// **取ってくるのは生のMIME**です（`/messages/{id}/$value`）。そのまま既存の
// 取り込み係へ渡せば、人が `.eml` をドロップしたときと**同じ道**を通ります
// ——封筒タグ・スレッドの繋ぎ・添付の展開・重複検知は全部そこにあるので、
// 経路ごとに書き直しません（内部で組み立て直すと必ず片方が古くなる）。
//
// **重複は取ってくる前に弾きます。** 一覧では `internetMessageId` だけ読み、
// 索引の逆引きで既に取り込み済みと分かれば本体を落としません——過去メールの
// 一括取り込みで同じものを何度も落とさないための要点です。
// ─────────────────────────────────────────────────────────────────────────

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// MessageRef は一覧で読む1件です（本体はまだ落としていません）。
type MessageRef struct {
	ID                string `json:"id"`
	InternetMessageID string `json:"internetMessageId"`
	Subject           string `json:"subject"`
	ReceivedDateTime  string `json:"receivedDateTime"`
	HasAttachments    bool   `json:"hasAttachments"`
}

// ListOptions は一覧の条件です。
type ListOptions struct {
	Folder string // "inbox"・"sentitems" など（空なら inbox）
	Max    int    // 取る最大件数（0 なら 50）
	Since  string // ISO 8601。これ以降に受信したものだけ（空なら全期間）
}

// ListMessages はメールの一覧を新しい順に返します（本体は落としません）。
func ListMessages(ctx context.Context, username string, opt ListOptions) ([]MessageRef, error) {
	token, err := accessToken(ctx, username)
	if err != nil {
		return nil, err
	}
	folder := opt.Folder
	if folder == "" {
		folder = "inbox"
	}
	max := opt.Max
	if max <= 0 {
		max = 50
	}

	q := url.Values{}
	q.Set("$select", "id,internetMessageId,subject,receivedDateTime,hasAttachments")
	q.Set("$orderby", "receivedDateTime desc")
	// 1回あたりの取得数は Graph の上限（1000）に収めつつ、要求件数を超えない。
	page := max
	if page > 200 {
		page = 200
	}
	q.Set("$top", strconv.Itoa(page))
	if s := strings.TrimSpace(opt.Since); s != "" {
		q.Set("$filter", "receivedDateTime ge "+s)
	}
	next := "https://graph.microsoft.com/v1.0/me/mailFolders/" +
		url.PathEscape(folder) + "/messages?" + q.Encode()

	var out []MessageRef
	for next != "" && len(out) < max {
		var body struct {
			Value    []MessageRef `json:"value"`
			NextLink string       `json:"@odata.nextLink"`
		}
		if err := graphGetJSON(ctx, token, next, &body); err != nil {
			return nil, err
		}
		out = append(out, body.Value...)
		next = body.NextLink
	}
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

// FetchRawMIME は1件の生のMIME（.eml と同じ中身）を落とします。
func FetchRawMIME(ctx context.Context, username, messageID string) ([]byte, error) {
	token, err := accessToken(ctx, username)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://graph.microsoft.com/v1.0/me/messages/"+url.PathEscape(messageID)+"/$value", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return nil, errors.New("メールを取得できません（" + resp.Status + "）: " + graphErrorMessage(detail))
	}
	return io.ReadAll(resp.Body)
}

// graphGetJSON は GET して JSON を読みます。
func graphGetJSON(ctx context.Context, token, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return errors.New("Graph の呼び出しに失敗しました（" + resp.Status + "）: " +
			graphErrorMessage(detail))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
