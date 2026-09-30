package comm

// ─────────────────────────────────────────────────────────────────────────
// 送る欄の「用件」——初期値と、送る前後の仕事を登録する口（2026-09-30）
//
// 利用者:「メール表示、メール送信、メール編集などを部品化したら良いと思います。FAXなどもいずれ
// 部品化したいです」「メール編集は下書きのことです」。
//
// それまでメールを書く欄は2つあり、別々に作られていました——返信の欄（画面が組む・CC あり・署名なし）と、
// 発注書の送信の欄（東邦の拡張が組む・CC なし・署名あり・送ったら発注済みの印）。**同じことをする欄が、
// 片方にしか無いものを持っていました。** いまは欄は1つ（`assets/mail-compose.js`）で、違うのは**用件**だけです:
//
//	用件   … 何のために書くか（返信・新規・発注書）。名前は画面に出る言葉のまま
//	初期値 … 宛先・件名・本文（署名入り）・添付の候補・注意（Defaults）
//	送る前 … 送る直前に要るもの（発注書なら PDF を作って添える・Prepare）
//	送った後 … 送れた結果の反映（発注書なら行を発注済みに・AfterSent）
//
// ⚠ **口はここ（通信）、中身は使う側の拡張**です——メールの拡張は発注書を知らず、東邦の拡張が
// 「発注書」の用件を登録します（開発方針 §0「口はこちら、言葉は拡張から」・`RegisterMailer` と同じ形）。
// ⚠ **いまはメールの項目だけ**です。FAX を足すときは、宛先（FAX 番号）と送る道を用件の外側で選ぶ形に
// 広げます——用件（初期値・前後の仕事）は道に依りません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"sort"

	"w-cms/internal/auth"
)

// 組み込みの用件の名前です（画面にもこの言葉で出ます）。
const (
	PurposeReply = "返信" // 通信記録への返信（In-Reply-To と `返信元` が付く）
	PurposeNew   = "新規" // 元のページの無いメール
)

// 下書きのページが持つタグです（2026-09-30）。
//
// ⚠ **下書きは通信箱のページです**（受信・送信の記録と同じ年月の置き場）——家でも職場でも開けて、
// **対応のタグが無いので未処理の一覧にも並びます**（書きかけを忘れない）。送ると、送った日の控えを
// 作って下書きはごみ箱へ移します。
const (
	// DraftTag は下書きの印です。値は用件の名前（返信・新規・発注書）。
	DraftTag = "下書き"
	// DraftSourceTag は用件の元のページ（返信元のメール・発注書）を指す参照タグです。
	// ⚠ `返信元` にはしません——送る前から「この記録への返信」に並ぶと、出していない返信が
	// 出したように見えます。
	DraftSourceTag = "下書きの元"
)

// ComposeAttachment は添付の候補1つです（w-cms の中のファイルを指す）。
type ComposeAttachment struct {
	PageID  string `json:"page_id"`
	File    string `json:"file"`
	Name    string `json:"name"`
	Checked bool   `json:"checked"`
}

// ComposeDraft は送る欄に出す中身です（初期値にも、保存した下書きにも使う）。
type ComposeDraft struct {
	Purpose     string              `json:"purpose"`
	PageID      string              `json:"page_id"`            // 用件の元のページ（無ければ空）
	DraftID     string              `json:"draft_id,omitempty"` // 下書きのページ（新しく書くときは空）
	To          []string            `json:"to"`
	Cc          []string            `json:"cc"`
	Subject     string              `json:"subject"`
	Body        string              `json:"body"`
	Attachments []ComposeAttachment `json:"attachments"`
	// Notes は人に見せる注意です（連絡帳に宛先が無い・読めない資料があった など）。
	Notes []string `json:"notes"`
	// SendNote は「送ると何が起きるか」の一文です（発注書なら PDF を作って添える・発注済みにする）。
	SendNote string `json:"send_note,omitempty"`
	// Reload は送れたあとにページを開き直すかです（送った結果が本文に入る用件——発注書）。
	Reload bool `json:"reload,omitempty"`
}

// SendPurpose は用件1つぶんの仕事です。どれも任意（nil なら何もしない）。
type SendPurpose struct {
	// Defaults は初期値を組みます。pageID は用件の元のページ（読めることは呼ぶ側が確かめる）。
	Defaults func(user *auth.User, pageID string) (ComposeDraft, error)
	// Prepare は送る直前に呼ばれ、添えるファイルを返します。⚠ **断るときは自分で応答を書いて
	// false を返します**（関門と同じ作法——書けない・編集中などの理由は用件の側が知っている）。
	Prepare func(w http.ResponseWriter, r *http.Request, pageID string) ([]ComposeAttachment, bool)
	// AfterSent は送れたあとに呼ばれます。⚠ **ここで失敗しても送信は成功のまま**です（出た事実を
	// 隠さない）——エラーは理由として人に見せます。recordID は送信の控えのページ（作れなければ空）。
	AfterSent func(user *auth.User, pageID, recordID string) error
	// InReplyTo は用件の元のページへの返信として送るかです（In-Reply-To と `返信元` が付く）。
	InReplyTo bool
	// NeedsPage は用件の元のページが要るかです（新規以外は要る）。
	NeedsPage bool
}

var sendPurposes = map[string]SendPurpose{}

// RegisterSendPurpose は用件を登録します（拡張の init から）。二重登録はその場で落とします
// （どちらの初期値が出るか分からなくなるため・`RegisterMailer` と同じ）。
func RegisterSendPurpose(name string, p SendPurpose) {
	if name == "" {
		panic("comm.RegisterSendPurpose: 名前が空です")
	}
	if _, dup := sendPurposes[name]; dup {
		panic("comm.RegisterSendPurpose: 用件「" + name + "」は登録済みです")
	}
	sendPurposes[name] = p
}

// SendPurposeOf は用件を名前で引きます。
func SendPurposeOf(name string) (SendPurpose, bool) {
	p, ok := sendPurposes[name]
	return p, ok
}

// SendPurposeNames は登録された用件の名前です（名前の順・試験と画面の案内のため）。
func SendPurposeNames() []string {
	out := make([]string, 0, len(sendPurposes))
	for k := range sendPurposes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
