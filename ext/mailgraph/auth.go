package mailgraph

// ─────────────────────────────────────────────────────────────────────────
// Microsoft 365 への認証——デバイスコードフロー（2026-09-03）
//
// **メールのパスワードは w-cms に渡りません。** 利用者は Microsoft の画面で
// 一度サインインし、サーバーが預かるのは更新用のトークン（リフレッシュトークン）
// だけです。取り消しは Entra 側からいつでもでき、アプリを削除すれば全員ぶんが
// 一度に無効になります。
//
// **なぜデバイスコードフローか**——リダイレクトURIの登録が要らないので、
// localhost → VPS → オンプレと引っ越しても Entra 側の設定を変えずに済みます。
// クライアントシークレットも不要で、鍵の保管・更新という仕事が発生しません。
//
// **なぜ IMAP/SMTP ではなく Graph か**——実物の受信メールのヘッダから、御社の
// メールボックスが Exchange Online にあると分かりました（2026-09-03 に確認）。
// M365 は IMAP/SMTP の基本認証を廃止済みなので**どちらにせよ OAuth2 が要り**、
// それなら Graph のほうが外部依存ゼロ（net/http と encoding/json だけ）で済みます。
// 他社サーバー向けの IMAP 実装は、別のプラグインとして後から足せます。
//
// **委任のみで動きます。** アプリに与えるのは「委任されたアクセス許可」だけなので、
// w-cms が触れるのは**サインインした本人のメール**に限られます。アプリケーション
// 許可（全社員のメールボックスを読める）は使いません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// 環境変数——**どちらも秘密ではありません**（アプリとテナントを識別する GUID で、
// 単体では何もできない）。それでも設定として環境変数へ置くのは、環境ごとに違う値を
// リポジトリへ入れないためです。
const (
	envClientID = "WCMS_MAIL_CLIENT_ID"
	envTenantID = "WCMS_MAIL_TENANT_ID"
)

// graphScopes と smtpScopes——**混ぜて要求できません。**
//
// Microsoft の v2 エンドポイントは**1回の要求につき1つのリソース**しかトークンを
// 出しません。Graph（`graph.microsoft.com`）と Exchange（`outlook.office.com`）を
// 1つの `scope` に並べると `AADSTS70011` で弾かれます。
//
// なので**サインインは Graph の権限で行い**（リフレッシュトークンはリソースに
// 依らない）、SMTP のトークンはそのリフレッシュトークンから別途取り直します。
// どちらの権限も Entra 側で同意済みである必要があります。
//
// `offline_access` が無いと、一度サインインしてもアクセストークンの期限
// （およそ1時間）で切れて毎回サインインし直しになります。
var graphScopes = []string{
	"https://graph.microsoft.com/Mail.Read",
	"https://graph.microsoft.com/User.Read",
	"offline_access",
}

// smtpScopes は投函だけの権限です（Exchange Online 側。smtp.go）。
var smtpScopes = []string{smtpScope, "offline_access"}

// ErrNotConfigured は環境変数が未設定の印です。
var ErrNotConfigured = errors.New(
	envClientID + " と " + envTenantID + " が設定されていません")

// httpClient は外部への口です。**時間切れを必ず持たせる**——既定の
// http.DefaultClient は無期限で、相手が黙ると保存要求ごと止まります。
var httpClient = &http.Client{Timeout: 30 * time.Second}

// config は Entra の設定を返します。
func config() (clientID, tenantID string, err error) {
	clientID = strings.TrimSpace(os.Getenv(envClientID))
	tenantID = strings.TrimSpace(os.Getenv(envTenantID))
	if clientID == "" || tenantID == "" {
		return "", "", ErrNotConfigured
	}
	return clientID, tenantID, nil
}

// Configured は設定が揃っているかを返します（起動ログと画面の出し分けに使います）。
func Configured() bool {
	_, _, err := config()
	return err == nil
}

// DeviceCode は利用者に見せる案内です。
type DeviceCode struct {
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`

	// deviceCode は**利用者に見せません**（ポーリングに使う内部の値）。
	deviceCode string
}

// deviceCodeResponse は Microsoft の応答です。
type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Error           string `json:"error"`
	ErrorDesc       string `json:"error_description"`
}

// StartDeviceCode はサインインを始めます。返した案内を利用者へ見せ、
// そのあと PollForToken を回します。
func StartDeviceCode(ctx context.Context) (*DeviceCode, error) {
	clientID, tenantID, err := config()
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("scope", strings.Join(graphScopes, " "))

	var d deviceCodeResponse
	if err := postForm(ctx,
		"https://login.microsoftonline.com/"+tenantID+"/oauth2/v2.0/devicecode",
		form, &d); err != nil {
		return nil, err
	}
	if d.Error != "" {
		return nil, errors.New("サインインを始められません: " + d.ErrorDesc)
	}
	interval := d.Interval
	if interval <= 0 {
		interval = 5
	}
	return &DeviceCode{
		UserCode:        d.UserCode,
		VerificationURI: d.VerificationURI,
		ExpiresIn:       d.ExpiresIn,
		Interval:        interval,
		deviceCode:      d.DeviceCode,
	}, nil
}

// tokenResponse は token 要求の応答です。
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// PollForToken は利用者がサインインし終えるまで待ち、トークンを保存します。
//
// **トークンはログにも応答にも出しません。** 返すのは「誰としてサインインしたか」
// （メールアドレス）だけです。
func PollForToken(ctx context.Context, username string, dc *DeviceCode) (string, error) {
	clientID, tenantID, err := config()
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	form.Set("device_code", dc.deviceCode)
	endpoint := "https://login.microsoftonline.com/" + tenantID + "/oauth2/v2.0/token"

	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(dc.Interval) * time.Second):
		}

		var t tokenResponse
		if err := postForm(ctx, endpoint, form, &t); err != nil {
			return "", err
		}
		switch t.Error {
		case "":
			addr, err := meAddress(ctx, t.AccessToken)
			if err != nil {
				return "", err
			}
			if err := saveToken(username, storedToken{
				RefreshToken: t.RefreshToken,
				Address:      addr,
				UpdatedAt:    time.Now().In(time.Local).Format(time.RFC3339),
			}); err != nil {
				return "", err
			}
			cacheAccess(username, t.AccessToken, t.ExpiresIn)
			return addr, nil
		case "authorization_pending":
			continue // まだサインインしていない
		case "slow_down":
			dc.Interval += 5
		default:
			// **理由をそのまま伝えます**——「権限が足りない」のか「まだ押していない」
			// のかが分からないと、運用側が直しようがありません。
			return "", errors.New("サインインできませんでした: " + t.ErrorDesc)
		}
	}
	return "", errors.New("サインインの待ち時間が切れました。もう一度お試しください")
}

// accessCache はアクセストークンの一時保管です（期限つき・ファイルには書きません）。
// **ディスクへ置くのはリフレッシュトークンだけ**にして、漏れる面を狭くします。
var accessCache sync.Map // username|scopes -> cachedAccess

type cachedAccess struct {
	token     string
	expiresAt time.Time
}

func cacheAccess(username, token string, expiresIn int) {
	cacheAccessKey(username+"|"+strings.Join(graphScopes, " "), token, expiresIn)
}

// cacheAccessKey はスコープまで含めた鍵で保管します。
func cacheAccessKey(key, token string, expiresIn int) {
	if token == "" || expiresIn <= 0 {
		return
	}
	accessCache.Store(key, cachedAccess{
		token: token,
		// 期限ぎりぎりで使うと送信の途中で切れるので、1分手前で切り上げます。
		expiresAt: time.Now().Add(time.Duration(expiresIn-60) * time.Second),
	})
}

// accessToken は Graph 用のアクセストークンを返します。
func accessToken(ctx context.Context, username string) (string, error) {
	return accessTokenFor(ctx, username, graphScopes)
}

// smtpAccessToken は SMTP 投函用のアクセストークンを返します。
func smtpAccessToken(ctx context.Context, username string) (string, error) {
	return accessTokenFor(ctx, username, smtpScopes)
}

// accessTokenFor は指定した権限のアクセストークンを返します（必要なら更新します）。
//
// **保管はスコープごと**です——Graph 用のトークンで SMTP へは投函できません
// （リソースが違う）。取り違えると「認証は通るのに送れない」という分かりにくい
// 失敗になります。
func accessTokenFor(ctx context.Context, username string, scopes []string) (string, error) {
	key := username + "|" + strings.Join(scopes, " ")
	if v, ok := accessCache.Load(key); ok {
		if c := v.(cachedAccess); time.Now().Before(c.expiresAt) {
			return c.token, nil
		}
	}
	clientID, tenantID, err := config()
	if err != nil {
		return "", err
	}
	st, ok := loadToken(username)
	if !ok {
		return "", errNotSignedIn
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", st.RefreshToken)
	form.Set("scope", strings.Join(scopes, " "))

	var t tokenResponse
	if err := postForm(ctx,
		"https://login.microsoftonline.com/"+tenantID+"/oauth2/v2.0/token",
		form, &t); err != nil {
		return "", err
	}
	if t.Error != "" {
		// **権限が足りないだけのときに保管を捨てない。** 捨ててしまうと、
		// SMTP の同意漏れでサインインごと失われ、原因が見えなくなります。
		if t.Error == "invalid_grant" {
			deleteToken(username)
			return "", errNotSignedIn
		}
		return "", errors.New("トークンを取得できません: " + t.ErrorDesc)
	}
	// **リフレッシュトークンは共有**です（リソースに依らない）。新しいものが
	// 返ってきたら差し替えます。
	if t.RefreshToken != "" && t.RefreshToken != st.RefreshToken {
		st.RefreshToken = t.RefreshToken
		st.UpdatedAt = time.Now().In(time.Local).Format(time.RFC3339)
		saveToken(username, st)
	}
	cacheAccessKey(key, t.AccessToken, t.ExpiresIn)
	return t.AccessToken, nil
}

// errNotSignedIn はこのパッケージの内部用です（コア側の ErrMailNotSignedIn へ翻訳）。
var errNotSignedIn = errors.New("メールアカウントにサインインしていません")

// meAddress はサインインした本人のメールアドレスを返します。
func meAddress(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://graph.microsoft.com/v1.0/me?$select=mail,userPrincipalName", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var me struct {
		Mail string `json:"mail"`
		UPN  string `json:"userPrincipalName"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return "", err
	}
	if me.Mail != "" {
		return me.Mail, nil
	}
	return me.UPN, nil
}

// postForm はフォームを投げて JSON を読みます。
func postForm(ctx context.Context, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// エラーでも本文に理由が入るので、状態コードでは切らずに読みます。
	return json.NewDecoder(resp.Body).Decode(out)
}
