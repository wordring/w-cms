package mailgraph

// ─────────────────────────────────────────────────────────────────────────
// メール送受信プラグイン（Microsoft Graph）の登録口
//
// コアが宣言した `cms.Mailer` の中身をここが持ちます（internal/cms/mail.go）。
// 使う側はコアに尋ねるだけなので、**このパッケージを外してもビルドは通ります**
// ——`-tags minimal` で丸ごと消え、画面から返信ボタンが消えるだけです。
// ─────────────────────────────────────────────────────────────────────────

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

func init() {
	cms.RegisterMailer(graphMailer{})
	cms.Register(mailPlugin{})
	if Configured() {
		log.Printf("メール送受信: 有効（Microsoft Graph）")
	} else {
		log.Printf("メール送受信: 設定待ち（%s と %s が未設定）", envClientID, envTenantID)
	}
}

// mailPlugin はルートを提供するだけのプラグインです（専用テーブルは持ちません）。
type mailPlugin struct{}

func (mailPlugin) Name() string     { return "mailgraph" }
func (mailPlugin) Schema() []string { return nil }
func (mailPlugin) Tables() []string { return nil }

func (mailPlugin) Routes() []cms.Route {
	return []cms.Route{
		{Pattern: "/api/mail/status", Handler: MailStatusAPIHandler},
		{Pattern: "/api/mail/signin", Handler: MailSignInAPIHandler},
		// 取り込みは**人が押したときだけ**走ります（自動で回し続けない）。
		{Pattern: "/api/mail/import", Handler: MailImportAPIHandler},
	}
}

// graphMailer は cms.Mailer の実装です。
type graphMailer struct{}

func (graphMailer) Name() string { return "Microsoft Graph" }

// Ready は、その利用者が送れる状態か（設定済み＋サインイン済み）を返します。
func (graphMailer) Ready(user *auth.User) bool {
	if user == nil || !Configured() {
		return false
	}
	return SignedInAddress(user.Username) != ""
}

// Send は user の名前で1通送ります。
func (graphMailer) Send(user *auth.User, msg cms.OutgoingMail) error {
	if user == nil {
		return cms.ErrMailNotSignedIn
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err := sendViaGraph(ctx, user.Username, msg)
	if err == errNotSignedIn {
		return cms.ErrMailNotSignedIn
	}
	return err
}

// MailStatusAPIHandler は GET /api/mail/status です。
// **サインインしているかどうかと、どのアドレスか**だけを返します（トークンは返しません）。
func MailStatusAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"success":    true,
		"configured": Configured(),
		"address":    SignedInAddress(user.Username),
	})
}

// MailSignInAPIHandler は POST /api/mail/signin です。
//
// デバイスコードを1つ発行して**案内だけ**返し、サインインの完了は背後で待ちます
// ——利用者は Microsoft の画面で番号を入れるだけで、パスワードは w-cms を通りません。
func MailSignInAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	if !Configured() {
		cms.JSONFail(w, 0, "サーバーに "+envClientID+" と "+envTenantID+" が設定されていません")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	dc, err := StartDeviceCode(ctx)
	if err != nil {
		cms.JSONFail(w, 0, err.Error())
		return
	}

	// **待つのは背後で。** 応答を返してから利用者が Microsoft の画面を開くので、
	// ここで待つと画面が固まります。完了は /api/mail/status で確かめます。
	username := user.Username
	go func() {
		bg, cancel := context.WithTimeout(context.Background(),
			time.Duration(dc.ExpiresIn+30)*time.Second)
		defer cancel()
		addr, err := PollForToken(bg, username, dc)
		if err != nil {
			// **理由はログへ**（トークンは含まれません）。利用者には status が
			// 「まだサインインしていない」と答えるだけになります。
			log.Printf("メールのサインインが完了しませんでした user=%s: %v", username, err)
			return
		}
		auth.Audit(username, "mail.signin", addr)
	}()

	json.NewEncoder(w).Encode(map[string]any{
		"success":          true,
		"verification_uri": dc.VerificationURI,
		"user_code":        dc.UserCode,
		"expires_in":       dc.ExpiresIn,
	})
}
