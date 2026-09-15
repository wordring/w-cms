package cms

// ─────────────────────────────────────────────────────────────────────────
// 通信の口（2026-09-15 に cmd/w-cms/main.go の直書きから移した）
//
// ルートも機能と一緒に出入りするよう、プラグインの `Routes()` で持ち込みます
// ——アドレス帳（`/api/contacts/…`）・下請け（`/api/analyze-attachment` 等）と同じ形。
// 通信の語彙を ext/comm へ出す下ごしらえで、ファイルごと移せる形にしてあります。
// ─────────────────────────────────────────────────────────────────────────

// commPlugin はルートを持ち込むためのプラグインです（表もスキーマも持たない）。
type commPlugin struct{}

func init() { Register(commPlugin{}) }

func (commPlugin) Name() string     { return "comm" }
func (commPlugin) Schema() []string { return nil }
func (commPlugin) Tables() []string { return nil }

// Routes は通信の口です。
func (commPlugin) Routes() []Route {
	return []Route{
		// この記録への返信（w-cms が送った返信の一覧）。
		{Pattern: "/api/replies", Handler: RepliesAPIHandler},
		// スレッドの前後（In-Reply-To の鎖）。`/api/replies` とは別の鎖で、
		// **受信どうしの返り**も繋がる（handler_thread.go の冒頭に違いを書いた）。
		{Pattern: "/api/thread", Handler: ThreadAPIHandler},
		// 未処理の一覧から「対応：不要」を付ける（まとめて押せる）。
		{Pattern: "/api/intake/handled", Handler: MarkHandledAPIHandler},
		// 手で記録を作る（電話・FAX・メール・メモ。FAXサーバーが繋がれば自動で増える）。
		{Pattern: "/api/intake/memo", Handler: NewMemoAPIHandler},
	}
}
