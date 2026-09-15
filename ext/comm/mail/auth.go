package mail

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
// **このファイルは M365 のあいだだけの持ち物です**（2026-09-05）。メールサーバーは
// レンタルへ移ることが決まっており、移設後の認証はパスワード（TLS の上）になります。
// 通信の骨格（IMAP／SMTP）はそのままで、差し替わるのは接続先とここだけです
// （imap.go の「継ぎ目」）。
//
// **委任のみで動きます。** アプリに与えるのは「委任されたアクセス許可」だけなので、
// w-cms が触れるのは**サインインした本人のメール**に限られます。アプリケーション
// 許可（全社員のメールボックスを読める）は使いません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"context"
	"encoding/base64"
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

// mailScopes は受信（IMAP）と送信（SMTP）の権限です。
//
// **1つにまとめられます。** Microsoft の v2 エンドポイントは1回の要求につき
// 1つのリソースにしかトークンを出しませんが、IMAP も SMTP も**同じ Exchange Online**
// （`outlook.office.com`）なので同じ札で足ります。**リソースが2つに割れていると
// 混ぜられず**（`AADSTS70011`）、スコープごとにトークンを取り分ける必要が出ます
// ——受信と送信が同じリソースなら、その面倒が最初から起きません。
//
// `offline_access` が無いと、一度サインインしてもアクセストークンの期限
// （およそ1時間）で切れて毎回サインインし直しになります。
//
// `openid`/`profile`/`email` は**自分のメールアドレスを知るため**だけに要ります
// （id_token の claim から読む・idTokenAddress）。IMAP も SMTP も「あなたは誰か」を
// 教えてくれないのに、SMTP の差出人としてアドレスが要るためです。
var mailScopes = []string{
	imapScope,
	smtpScope,
	"offline_access",
	"openid", "profile", "email",
}

// imapScope は受信の権限です（Exchange Online 側。imap.go）。
const imapScope = "https://outlook.office.com/IMAP.AccessAsUser.All"

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
	form.Set("scope", strings.Join(mailScopes, " "))

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
	IDToken      string `json:"id_token"` // 自分のアドレスはここから読む（idTokenAddress）
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
			addr, err := idTokenAddress(t.IDToken)
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
	cacheAccessKey(username+"|"+strings.Join(mailScopes, " "), token, expiresIn)
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

// mailAccessToken は受信・送信の両方に使えるアクセストークンを返します。
// **IMAP と SMTP は同じリソース**（Exchange Online）なので1つで足ります。
func mailAccessToken(ctx context.Context, username string) (string, error) {
	return accessTokenFor(ctx, username, mailScopes)
}

// accessTokenFor は指定した権限のアクセストークンを返します（必要なら更新します）。
//
// **保管はスコープごと**です。いまは1組しか使いませんが、リソースをまたぐ札は
// 共有できない（別リソースのトークンでは通らない）ので、鍵は残してあります。
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
		// **保管は自動で捨てません**（2026-09-05 に方針を変えました）。
		//
		// 以前は `invalid_grant` を「失効した」とみなして捨てていましたが、
		// **同意が足りないときも `invalid_grant` で返ってきます**（`AADSTS65001`）。
		// 受信を IMAP へ移して要求する権限が増えた日、既存のサインインが
		// **その場で消えました**——原因も残らないので、何が起きたか分からなくなります。
		//
		// 捨てても得はありません。古いトークンは無害で、サインインし直せば
		// 上書きされます。**理由をそのまま見せて、人に判断させる**ほうが安全です。
		if strings.Contains(t.ErrorDesc, "AADSTS65001") {
			return "", errors.New(
				"この操作に必要な同意がありません。設定からサインインし直してください（" +
					firstLine(t.ErrorDesc) + "）")
		}
		if t.Error == "invalid_grant" {
			return "", errors.New(
				"サインインが期限切れか取り消されています。サインインし直してください（" +
					firstLine(t.ErrorDesc) + "）")
		}
		return "", errors.New("トークンを取得できません: " + firstLine(t.ErrorDesc))
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

// firstLine は Microsoft の長い説明から先頭の1行だけを取ります
// （相関IDやタイムスタンプまで画面へ出すと、肝心の理由が埋もれます）。
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// errNotSignedIn はこのパッケージの内部用です（コア側の ErrMailNotSignedIn へ翻訳）。
var errNotSignedIn = errors.New("メールアカウントにサインインしていません")

// idTokenAddress はサインインした本人のメールアドレスを id_token から読みます。
//
// **IMAP も SMTP も「あなたは誰か」を教えてくれません。** それでも SMTP の差出人と
// XOAUTH2 の `user=` にアドレスが要るので、サインインの応答に付いてくる `id_token`
// （OpenID Connect の身元トークン）の claim から採ります。
//
// **署名は検証しません。** Microsoft のトークン発行口から TLS の上で直接受け取った
// ものを、そのまま表示と差出人に使うだけだからです——第三者から渡された JWT を
// 信用するのとは話が違います。もし中身が嘘なら、そのアドレスでは投函が通りません
// （SMTP 側が本人確認をします）。
func idTokenAddress(idToken string) (string, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", errNoAddress
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errNoAddress
	}
	var c struct {
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
		UPN               string `json:"upn"`
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", errNoAddress
	}
	for _, v := range []string{c.PreferredUsername, c.Email, c.UPN} {
		if strings.Contains(v, "@") {
			return v, nil
		}
	}
	return "", errNoAddress
}

// errNoAddress はアドレスが分からなかった印です。**黙って空で進めません**
// ——差出人が空のまま送ろうとすると、原因の分からない投函失敗になります。
var errNoAddress = errors.New(
	"サインインした本人のメールアドレスが分かりませんでした（openid / email の同意が要ります）")

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
