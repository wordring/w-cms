package cms

// ─────────────────────────────────────────────────────────────────────────
// メール送信の口——コアは「口」だけを持つ（2026-09-03）
//
// ユーザー:「メール関係はメールプラグインになって、それを使うプラグインは明示する
// 感じでしょうか」——そのとおりですが、**「使う側が import する」形は採りません**。
// それだとメールを外したときに使う側までビルドできなくなり、
// 「コンパイル時にプラグインを選べる」（-tags minimal）の思想と噛み合いません。
//
// 採るのは既存の回覧機構と同じ形です:
//
//	コアが口を宣言   … このファイル（Mailer・RegisterMailer）
//	プラグインが中身 … ext/mailgraph（Microsoft Graph 実装）
//	使う側はコアに尋ねる … CurrentMailer()。無ければ「送れません」と答えるだけ
//
// `RegisterIntake`・`RegisterView`・`RegisterMirror` と同じ規律なので、
// 新しい概念を1つも増やしていません。明示は**起動ログ**（メール送受信の有無）と
// **画面**（返信ボタンが出るかどうか）に現れます。
//
// **秘密はここを通りません。** 資格情報の扱いは実装（プラグイン）の持ち物で、
// コアは「誰が・何を・どこへ送るか」しか知りません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"

	"w-cms/internal/auth"
)

// MailAttachment は送信メールへ添える1ファイルです。
type MailAttachment struct {
	Name     string
	MIMEType string
	Content  []byte
}

// OutgoingMail は送る1通です。
//
// InReplyTo に返信元の Message-ID を入れると、**受信側と同じ仕組みでスレッドが
// 繋がります**——取り込みが `返信元メッセージID` タグを書き、索引の逆引き
// （PagesByTag）で辿れるのと同じ形（intake_eml.go）。
type OutgoingMail struct {
	To          []string
	Cc          []string
	Subject     string
	BodyText    string
	InReplyTo   string
	Attachments []MailAttachment
}

// Mailer はメールを送れる者です。実装はプラグインが持ちます。
//
// **送信は常に「その利用者として」行います**（2026-09-03 ユーザー決定「利用者ごとの
// アカウント」）。誰が送ったか分からないメールを業務記録に残さないためで、
// 実装側も本人のトークンでしか送れないよう作ります（委任のみ・他人のメールは
// 読めない）。
type Mailer interface {
	// Name は実装の名前です（起動ログに出ます）。
	Name() string

	// Ready は user が送れる状態か（サインイン済みか）を返します。
	// 画面が「返信」を出すかどうかの判断に使います。
	Ready(user *auth.User) bool

	// Send は user の名前で1通送ります。
	Send(user *auth.User, msg OutgoingMail) error
}

// ErrNoMailer はメールのプラグインが入っていない印です。
// 呼ぶ側が「設定の問題」と「送信の失敗」を区別できるようにしてあります
// （ErrNoGeminiKey と同じ考え）。
var ErrNoMailer = errors.New("メール送信のプラグインが入っていません")

// ErrMailNotSignedIn はプラグインは在るが、その利用者がまだサインインしていない印です。
var ErrMailNotSignedIn = errors.New("メールアカウントにサインインしていません")

// mailer は登録された実装です（1つだけ）。
var mailer Mailer

// RegisterMailer はメール送信の実装を登録します。**拡張の init() から**呼びます。
// 二重登録はその場で落とします（どちらが送るか分からなくなるため）。
func RegisterMailer(m Mailer) {
	if mailer != nil {
		panic("メール送信の実装が重複しています: " + mailer.Name() + " と " + m.Name())
	}
	mailer = m
}

// CurrentMailer は登録された実装を返します（無ければ false）。
func CurrentMailer() (Mailer, bool) {
	return mailer, mailer != nil
}

// SendMail は登録された実装へ送信を委ねます。実装が無ければ ErrNoMailer。
func SendMail(user *auth.User, msg OutgoingMail) error {
	m, ok := CurrentMailer()
	if !ok {
		return ErrNoMailer
	}
	if !m.Ready(user) {
		return ErrMailNotSignedIn
	}
	return m.Send(user, msg)
}
