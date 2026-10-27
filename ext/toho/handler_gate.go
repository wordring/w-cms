package toho

// ─────────────────────────────────────────────────────────────────────────
// 本文を書き換える口の前口上（2026-09-23 に寄せた）
//
// 09-22 に一日で書かれた口（発注部材表を作る・行を外す・発注済の印・送った印・
// PDFを作る・受注明細表のセル）が、**同じ3段を6回**写していました——ページIDを
// 6桁へ畳む → そのページの write を確かめる → 誰かが編集中なら断る。そして
// `RewriteBody` の失敗を 500 で返す4行も5回。ここへ寄せ、以後はここを通します。
//
// ⚠ **順序がこの関門の全部です。** 畳めないIDで権限を引くと「読めない」と
// 「存在しない」の区別が漏れ、編集中の判定を write より先にすると、書けない人に
// 「誰が開いているか」を教えることになります。
//
// （2026-10-12 から）ブラウザは編集権を取らなくなりました。3段目の「誰かが編集中なら断る」は、
// 道具が `/api/lock` で一瞬握るとき以外は素通りです。人の保存とぶつからないのは、
// `RewriteBody` がページごとの書き込みの錠（`cms.LockPageBody`）の中で書くからです。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"net/http"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// requireWritableIdle は「そのページへ write できて、テンプレートの中ではなく、
// 道具が編集権を握っていない」を確かめます。
//
// ⚠ **機械が既存ページの本文を書き換えるときの関門**です。
// 2026-10-12 までは、エディタが開いているとオートセーブと機械の書き込みが黙って上書きし合うので、
// 誰かが開いていれば（自分でも）断っていました。いまはブラウザが編集権を取らないので、
// 最後の `editlock.RefuseWhileEditing` は、道具（`/api/lock` → `/api/save`）が一瞬握るとき以外は素通りです。
// 人の保存とは、`RewriteBody` の書き込みの錠（`cms.LockPageBody`）で順に並び、
// 同じブロックがぶつかれば画面が両方を並べます（機械も「ほかの書き手の1人」）。
// `RequireEditLock` は使えません（一覧の画面はトークンを持てず必ず 409 になる・2026-09-14 の決定）。
func requireWritableIdle(w http.ResponseWriter, r *http.Request, pageID string) bool {
	if !page.RequirePageWrite(w, r, pageID) {
		return false
	}
	// ⚠ **テンプレートの中へは書きません**（2026-09-27 利用者:「テンプレート以下では鏡もボタンも
	// 機能しないようにしましょう」）——テンプレートの上で押した発注部材表などが、そこから作る
	// ページ全部へコピーされるため（コアの `RefuseTemplateArea`）。権限の関門より後に置く。
	if !cms.RefuseTemplateArea(w, pageID) {
		return false
	}
	return editlock.RefuseWhileEditing(w, pageID)
}

// gateWritablePage は上の2つを続けて通し、通れば6桁のページIDを返します。
func gateWritablePage(w http.ResponseWriter, r *http.Request, raw string) (string, bool) {
	pageID, ok := cms.PageIDOrFail(w, raw)
	if !ok {
		return "", false
	}
	if !requireWritableIdle(w, r, pageID) {
		return "", false
	}
	return pageID, true
}

// gatePagePost は、入力が {page_id} だけの口の前口上です（POST・JSON → 本文を読む → gateWritablePage）。
// 2026-10-19 に寄せた（製造ページ・紙・送った印の口が、同じ13行を写していた）。
func gatePagePost(w http.ResponseWriter, r *http.Request) (*auth.User, string, bool) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return nil, "", false
	}
	var req struct {
		PageID string `json:"page_id"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return nil, "", false
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return nil, "", false
	}
	return user, pageID, true
}

// requireMfgPage は、製造ページ（`ページ種類：製造`）でなければ 400 を返して false です。
func requireMfgPage(w http.ResponseWriter, pageID string) bool {
	if pageKindOf(pageNum(pageID)) != PageKindMfg {
		cms.JSONFail(w, http.StatusBadRequest, "製造ページではありません")
		return false
	}
	return true
}

// requireRFQPage は、部材見積依頼書ページでなければ 400 を返して false です。
func requireRFQPage(w http.ResponseWriter, pageID string) bool {
	if !isRFQPage(pageID) {
		cms.JSONFail(w, http.StatusBadRequest, "部材見積依頼書ページではありません")
		return false
	}
	return true
}

// failCellEdit は、表のセルを書き換えられなかったときの返事です。
// ⚠ **食い違い（errCellConflict）は 409**（相手が悪いのではなく、状態がずれている）・ほかは 400。
func failCellEdit(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, errCellConflict) {
		code = http.StatusConflict
	}
	cms.JSONFail(w, code, err.Error())
}

// rebuildBody は、既にあるページの本文を build で組み直して書きます（2026-10-19 夜・【考察】文書の食い違い §3.3 の形）。
//
// body は呼び手が錠の外で読んだ本文です。まずそれで組んでみて、断られたら（buildErr）、または変わらなければ、書きません
// （`RewriteBody` は変わらなくても版を足すので、ここで止める）。
// ⚠ **書くときは、錠の中で読み直した本文から組み直します**——錠の外で組んだものを書くと、読んでから書くまでのあいだの
// ほかの人の保存が消えます。錠の中で組めなければ（そのあいだに行が動いた・値が変わった）、何も変えずに buildErr を返します。
// writeErr は書けなかったときだけです（呼び手は 500 にする）。
func rebuildBody(pageID, author, body string, build func(cur string) (string, error)) (changed bool, buildErr, writeErr error) {
	next, err := build(body)
	if err != nil {
		return false, err, nil
	}
	if next == body {
		return false, nil, nil
	}
	if rebuildBeforeWrite != nil {
		rebuildBeforeWrite(pageID) // 試験だけ（読んでから書くまでのあいだに、ほかの人の保存を挟む）
	}
	writeErr = cms.RewriteBody(pageID, author, func(cur string) string {
		fixed, err := build(cur)
		if err != nil {
			buildErr = err
			return cur
		}
		changed = fixed != cur
		return fixed
	})
	if writeErr != nil {
		return false, nil, writeErr
	}
	return changed, buildErr, nil
}

// rebuildBeforeWrite は試験だけが使う口です（rebuildBody が錠の外で組んでから、錠の中で書くまでのあいだに呼ぶ）。ふだんは nil。
var rebuildBeforeWrite func(pageID string)

// rewriteBodyOrFail は本文を書き換えます。書けなければ 500 を返して false。
func rewriteBodyOrFail(w http.ResponseWriter, pageID, username string, fn func(string) string) bool {
	if err := cms.RewriteBody(pageID, username, fn); err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "本文を書けません: "+err.Error())
		return false
	}
	return true
}

// errNoWrite は、ページを作る先（置き場・製造ページ など）に書く権限が無いときの誤りです（2026-10-21）。
// 呼び手は createFailStatus で 403 にします。
//
// 2026-10-21 利用者:「直します」——書類を作る API が、作る先へ書けるかを見ていなかった
// （ログインしていれば、書けない置き場の下にも書類を作れた。【考察】文書の食い違い §7.2）。
// コアの「新しいページ」（NewPageAPIHandler）と同じく、親ページへの write を求めます。
type errNoWrite struct{ pageID string }

func (e *errNoWrite) Error() string {
	return "「" + pageTitleOf(e.pageID) + "」に書く権限がないので、ページを作れません"
}

// requireWriteTo は、user が pageID へ書けなければ errNoWrite を返します。
func requireWriteTo(user *auth.User, pageID string) error {
	if !canWritePage(user, pageNum(pageID)) {
		return &errNoWrite{pageID: pageID}
	}
	// テンプレートの中は、業務の操作では書き換えない（2026-10-27 の見直し——requireWritableIdle と揃える）。
	if cms.IsTemplateArea(pageID) {
		return cms.ErrTemplateArea
	}
	return nil
}

// createFailStatus は、ページを作れなかった誤りを返すときの状態の番号です（書く権限が無ければ 403、ほかは fallback）。
func createFailStatus(err error, fallback int) int {
	var nw *errNoWrite
	if errors.As(err, &nw) {
		return http.StatusForbidden
	}
	return fallback
}

// newBuiltPage は parentID の子に「作成中」のページを作り、その番号で build した本文を書きます（2026-10-20 にまとめた）。
//
// 書類の番号（見積書番号・部材発注書番号 など）はページ番号なので、先にページを作ってから組みます。name は断り文に出す
// 書類の名前（「納品書」なら「納品書ページを作れません」）。作れない・組めない・書けないときは、その理由の誤りを返します
// （呼び手は createFailStatus で、書く権限が無ければ 403・ほかは 500 にする）。
// ⚠ 組めなかったときも、作った「作成中」のページは残ります（まとめる前と同じ）。
// ⚠ user が parentID へ書けなければ、何も作らずに errNoWrite を返します（2026-10-21）。
func newBuiltPage(parentID string, user *auth.User, name string, build func(pageID string) (string, error)) (string, error) {
	if err := requireWriteTo(user, parentID); err != nil {
		return "", err
	}
	author := user.Username
	newID, err := cms.CreateChildPage(parentID, author, "<h1>作成中</h1>")
	if err != nil {
		return "", errors.New(name + "ページを作れません: " + err.Error())
	}
	body, err := build(newID)
	if err != nil {
		return "", errors.New(name + "ページを組めません: " + err.Error())
	}
	if err := cms.RewriteBody(newID, author, func(string) string { return body }); err != nil {
		return "", errors.New("本文を書けません: " + err.Error())
	}
	return newID, nil
}

// aiFailure は、Gemini に読ませられなかったときの状態の番号と断り文です（2026-10-20 に5つの写しをまとめた）。
// 鍵が無ければ 503 と設定の案内、ほかは 502 と what（「解析に失敗しました」など）に理由を添えた文。
func aiFailure(err error, what string) (int, string) {
	if errors.Is(err, cms.ErrNoGeminiKey) {
		return http.StatusServiceUnavailable, "サーバーに GEMINI_API_KEY 環境変数が設定されていません。設定してから起動し直してください。"
	}
	return http.StatusBadGateway, what + ": " + err.Error()
}
