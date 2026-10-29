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
		// この記録への返信（RepliesTo）とスレッドの前後（ThreadOf）は、記録の箱がサーバーの中で描く（record_box.go）。
		// それを JSON で返す `/api/replies`・`/api/thread` は、画面から呼ぶ所が無くなったので 2026-10-30 に外した。
		// メールの一覧（受信も送信も・スレッドは画面が `親ページID` で組む——2026-10-09 までは In-Reply-To・2026-09-29）。
		{Pattern: "/api/mails", Handler: MailListAPIHandler}, // 通信の一覧（2026-10-29 からは全部のチャネル——通信箱の見え方「スレッド」）
		{Pattern: "/api/comm/threads", Handler: ThreadsRedirectAPIHandler}, // 通信箱の「スレッド」へ送る（2026-10-29）
		// 未処理の一覧・メールのページから `対応` を付ける（済・不要・未処理。まとめて押せる）。
		{Pattern: "/api/intake/handled", Handler: MarkHandledAPIHandler},
		// 手で記録を作る（電話・FAX・メール・メモ。FAXサーバーが繋がれば自動で増える）。
		{Pattern: "/api/intake/memo", Handler: NewMemoAPIHandler},
		{Pattern: "/api/machine/call", Handler: MachineCallAPIHandler}, // 電話の着信（機械専用の入口・2026-10-21 夜・machine_call.go）
		{Pattern: "/api/machine/fax", Handler: MachineFaxAPIHandler}, // FAX の受信（機械専用の入口・2026-10-21 夜・machine_fax.go）
		// 出ていない着信を記録にする・一覧から外す（2026-10-28・missed_call.go）。
		{Pattern: "/api/phone/missed/make", Handler: MissedCallMakeAPIHandler},
		{Pattern: "/api/phone/missed/dismiss", Handler: MissedCallDismissAPIHandler},
		// FAX を送る（ページの PDF を HylaFAX へ頼む）と、その様子（2026-10-25 夜・fax_send.go）。
		{Pattern: "/api/fax/send", Handler: FaxSendAPIHandler},
		{Pattern: "/api/fax/status", Handler: FaxStatusAPIHandler},
		{Pattern: "/api/fax/numbers", Handler: FaxNumbersAPIHandler}, // 送り先の番号の候補（fax_numbers.go）
		// 通信箱の「📠 FAX を送る」（パソコンの PDF をそのまま・fax_send_file.go）と「☎ 電話をかける」「📠」の相手の候補（box_numbers.go）——2026-10-28。
		{Pattern: "/api/fax/send-file", Handler: FaxSendFileAPIHandler},
		{Pattern: "/api/comm/numbers", Handler: BoxNumbersAPIHandler},
		// 業務サーバーのモデムで電話をかける（会社の電話機で話す・phone_dial.go・cmd/w-cms-dial——2026-10-28）。
		{Pattern: "/api/phone/dial/info", Handler: PhoneDialInfoAPIHandler},
		{Pattern: "/api/phone/dial", Handler: PhoneDialAPIHandler},
		{Pattern: "/api/phone/dial/release", Handler: PhoneDialReleaseAPIHandler},
		// `.eml` を通信箱の記録にする（道具 tools/mail/push が叩く・2026-09-30——通信箱へ落とす道をやめた代わり）。
		{Pattern: "/api/intake/eml", Handler: IntakeEMLAPIHandler},
		// 記録から作るページ（「作成」・種類は拡張が登録する・2026-10-01・record_make.go）。選ぶ欄は記録の箱が描く
		// （選べる種類を JSON で返す `/api/record-makers` は、画面から呼ぶ所が無くなったので 2026-10-30 に外した）。
		{Pattern: "/api/record-make", Handler: RecordMakeAPIHandler},
	}
}
