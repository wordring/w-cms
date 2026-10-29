package comm

// 通信箱の「📠 FAX を送る」（2026-10-28）——パソコンの PDF を、ページに置かずにそのまま FAX で送る。
//
//	POST /api/fax/send-file（multipart: file＝PDF・number＝FAX の番号）
//
// 通信箱に書ける人だけが送れます（送った FAX の記録を通信箱に作るため）。番号の確かめ・料金の高い番号・送った FAX の記録・
// 送れたかの見張りは、ページの PDF を送る口（/api/fax/send・fax_send.go）と同じです。記録には送った PDF が写り、
// 親ページID は付きません（元のページが無いため）。
//
// > 2026-10-28 利用者:「FAXの送信と電話のダイヤルを通信フォルダから出来るようにしたいです。」

import (
	"net/http"
	"os"

	"w-cms/internal/cms"
)

// FaxSendFileAPIHandler は POST /api/fax/send-file です。
func FaxSendFileAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	if _, err := WritableMailBox(user); err != nil {
		cms.JSONFail(w, http.StatusConflict, "FAX の記録を作れないので、送っていません: "+err.Error())
		return
	}
	if !cms.ParseUploadForm(w, r) {
		return
	}
	number, ok := outboundNumber(w, user, r.FormValue("number"), "FAX の番号", "送れる")
	if !ok {
		return
	}
	name, content, ok := cms.UploadedFile(w, r, "file", map[string]bool{".pdf": true}, "FAX で送れるのは PDF だけです")
	if !ok {
		return
	}
	// 送る道具（sendfax）はファイルの場所を受けるので、一時のファイルに置く（送る仕事に写したあと消す）。
	tmp, err := os.CreateTemp("", "w-cms-fax-*.pdf")
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "一時のファイルを作れません")
		return
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		cms.JSONFail(w, http.StatusInternalServerError, "一時のファイルに書けません")
		return
	}
	tmp.Close()

	// 元のページは無い（記録に 親ページID は付かない）。控えの紙は送ったファイルそのもの（paperFile）。
	dispatchFax(w, r, user, faxSource{File: name, Path: path, Name: name}, number, "（パソコンのファイル）"+name)
}
