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
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"w-cms/internal/auth"
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
	limit := cms.MaxUploadBytes()
	r.Body = http.MaxBytesReader(w, r.Body, limit+1<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		cms.JSONFail(w, http.StatusBadRequest, "ファイルを受け取れません: "+err.Error())
		return
	}
	number := faxSendNumber(r.FormValue("number"))
	if number == "" {
		cms.JSONFail(w, http.StatusBadRequest, "FAX の番号が読めません（数字6〜15桁。ハイフンは入れてもよい）")
		return
	}
	if costlyFaxNumber(number) && !user.IsAdmin {
		cms.JSONFail(w, http.StatusForbidden, "国際・事業者を選ぶ番号・有料の番号（010・00・0990 で始まる番号）へ送れるのは管理者だけです")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, "送るファイルがありません")
		return
	}
	defer f.Close()
	name := filepath.Base(strings.TrimSpace(hdr.Filename))
	if !strings.EqualFold(filepath.Ext(name), ".pdf") {
		cms.JSONFail(w, http.StatusBadRequest, "FAX で送れるのは PDF だけです: "+name)
		return
	}
	content, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(content)) > limit {
		cms.JSONFail(w, http.StatusRequestEntityTooLarge, "ファイルが大きすぎます")
		return
	}
	if content, err = cms.GuardUploadContent(name, content); err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
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

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := runSendfax(ctx, number, path)
	m := sendfaxJobRe.FindStringSubmatch(string(out))
	if err != nil || m == nil {
		cms.JSONFail(w, http.StatusServiceUnavailable, "FAX の道具に頼めませんでした: "+firstFaxLine(string(out), err))
		return
	}
	auth.Audit(user.Username, "fax.send", "（パソコンのファイル）"+name+" -> "+number+" (job "+m[1]+")")
	resp := map[string]any{"success": true, "job": m[1], "number": number}
	src := faxSource{File: name, Path: path, Name: name}
	if recID, err := recordSentFax(user, src, number, m[1]); err != nil {
		resp["record_error"] = err.Error()
	} else {
		resp["record"] = recID
		if err := putFaxJob(faxJob{Job: m[1], Record: recID, User: user.Username, File: name, Number: number, Started: time.Now()}); err != nil {
			resp["record_error"] = "仕事の控えを書けませんでした（送れたかは記録に書かれません）: " + err.Error()
		} else {
			go watchFaxJob(m[1])
		}
	}
	cms.WriteJSON(w, resp)
}
