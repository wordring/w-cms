package cms

// ─────────────────────────────────────────────────────────────────────────
// アップロードの受け口（2026-09-15）
//
// ページへのアップロードを、**通常の添付として保存する前に引き受ける**口です。
// もとは汎用の口（upload_file.go・pdf_handler.go）が `MailBoxPageID()` を直接呼び、
// 通信箱宛てなら取り込み係へ回していました——**コアが通信箱を名指し**していたので、
// 通信の語彙を拡張（ext/comm）へ出すと、ここだけ置いていかれます
// （組み替えの 3d・docs/【考察】通信拡張と下請け拡張への組み替え.md §3.2）。
//
// 引き受ける側は、**そのページが自分の担当か**を自分で判断し、担当なら応答を書き終えて
// true を返します。担当でなければ false——コアは通常の添付の経路へ進みます。
// 権限（write）はコアが先に確かめ済みで、編集ロックは**確かめる前**に呼びます
// （通信箱のように本文を変えずに子ページを生むだけの受け口があるため）。
// ─────────────────────────────────────────────────────────────────────────

import "net/http"

// UploadInterceptor はアップロードを先に引き受ける口です。formField はファイルの入った
// フォーム欄の名前（汎用の口は "file"、PDF専用の口は "pdf_file"）。
type UploadInterceptor func(w http.ResponseWriter, r *http.Request, pageID, formField string) bool

// uploadInterceptors は登録された受け口です。**`init()` の中からだけ**登録します。
var uploadInterceptors []UploadInterceptor

// RegisterUploadInterceptor は受け口を足します（拡張の `init()` から呼ぶ）。
func RegisterUploadInterceptor(f UploadInterceptor) {
	uploadInterceptors = append(uploadInterceptors, f)
}

// interceptUpload は受け口に順に尋ね、どれかが引き受けたら true を返します。
func interceptUpload(w http.ResponseWriter, r *http.Request, pageID, formField string) bool {
	for _, f := range uploadInterceptors {
		if f(w, r, pageID, formField) {
			return true
		}
	}
	return false
}
