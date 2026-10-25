package comm

import "w-cms/internal/cms"

// ─────────────────────────────────────────────────────────────────────────
// 通信の口（2026-09-15 に cmd/w-cms/main.go の直書きから移した）
//
// ルートも機能と一緒に出入りするよう、プラグインの `Routes()` で持ち込みます
// ——アドレス帳（`/api/contacts/…`）・下請け（`/api/analyze-attachment` 等）と同じ形。
// 通信の語彙を ext/comm へ出す下ごしらえで、ファイルごと移せる形にしてあります。
// ─────────────────────────────────────────────────────────────────────────

// commPlugin はルートを持ち込むためのプラグインです（表もスキーマも持たない）。
type commPlugin struct{}

func init() {
	// **名簿に載る**（起動ログと画面の出し分け・internal/cms/extensions.go）。
	cms.RegisterExtension("comm", "通信")
	// 画面の JS・CSS は、この拡張のフォルダの assets/ に置けば拾われます（2026-10-20・internal/cms/ext_scripts.go の置き場の決まり）。
	cms.Register(commPlugin{})
}

func (commPlugin) Name() string     { return "comm" }
func (commPlugin) Schema() []string { return nil }
func (commPlugin) Tables() []string { return nil }

// Routes は通信の口です。
func (commPlugin) Routes() []cms.Route {
	return []cms.Route{
		// この記録への返信（こちらが送った返信の一覧——親ページID がこの記録を指す送信の記録。Outlook から送って取り込んだ返信も入る）。
		{Pattern: "/api/replies", Handler: RepliesAPIHandler},
		// スレッドの前後（親と子）。2026-10-09 から `/api/replies` と同じ `親ページID` の鎖で、違いは
		// **受信どうしの返り**も出すこと（handler_thread.go の冒頭に違いを書いた。それまでは In-Reply-To の鎖）。
		{Pattern: "/api/thread", Handler: ThreadAPIHandler},
		// メールの一覧（受信も送信も・スレッドは画面が `親ページID` で組む——2026-10-09 までは In-Reply-To・2026-09-29）。
		{Pattern: "/api/mails", Handler: MailListAPIHandler},
		// 未処理の一覧・メールのページから `対応` を付ける（済・不要・未処理。まとめて押せる）。
		{Pattern: "/api/intake/handled", Handler: MarkHandledAPIHandler},
		// 手で記録を作る（電話・FAX・メール・メモ。FAXサーバーが繋がれば自動で増える）。
		{Pattern: "/api/intake/memo", Handler: NewMemoAPIHandler},
		{Pattern: "/api/machine/call", Handler: MachineCallAPIHandler}, // 電話の着信（機械専用の入口・2026-10-21 夜・machine_call.go）
		{Pattern: "/api/machine/fax", Handler: MachineFaxAPIHandler}, // FAX の受信（機械専用の入口・2026-10-21 夜・machine_fax.go）
		// FAX を送る（ページの PDF を HylaFAX へ頼む）と、その様子（2026-10-25 夜・fax_send.go）。
		{Pattern: "/api/fax/send", Handler: FaxSendAPIHandler},
		{Pattern: "/api/fax/status", Handler: FaxStatusAPIHandler},
		// `.eml` を通信箱の記録にする（道具 tools/mail/push が叩く・2026-09-30——通信箱へ落とす道をやめた代わり）。
		{Pattern: "/api/intake/eml", Handler: IntakeEMLAPIHandler},
		// 記録から作るページ（選ぶ欄と「作成」・種類は拡張が登録する・2026-10-01・record_make.go）。
		{Pattern: "/api/record-makers", Handler: RecordMakersAPIHandler},
		{Pattern: "/api/record-make", Handler: RecordMakeAPIHandler},
	}
}
