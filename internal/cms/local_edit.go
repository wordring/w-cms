package cms

// ─────────────────────────────────────────────────────────────────────────
// 添付をローカルのアプリで編集する——常駐ヘルパー（cmd/w-cms-edit）の口（2026-09-29）
//
// 利用者:「実運用するには、ローカルアプリで添付ファイルを編集して保存できる必要があります。この実装も急がれます」
// 「ローカルアプリで編集する話は、Webdavを諦めたはずです」。
//
// **なぜ WebDAV でないか**（2026-09-18 に実物の CAD で分かった——変更履歴 同日・考察 案B）: Windows の WebDAV は
// 保存（Ctrl+S）を手元に溜め、**アプリを閉じるまでサーバーへ送らない**（75秒待っても届かず、閉じたときに届いた）。
// サーバー側では直せない。だから各 PC のヘルパーが、①手元へ落とし ②既定のアプリで開き ③**保存のたびに**変わったのを
// 見て、ここへ上げる。
//
//   - **鍵はそのファイル1つに限る・使い捨て**（`POST /api/local-edit/start` がログインした人に出す）。ヘルパーは
//     ブラウザのログインを持たないので、鍵でだけ確かめる（`/api/local-edit/file` はログインの外）。鍵は推測できない
//     長さの乱数・12時間で切れる（使うたびに延びる）・サーバーの記憶だけ（再起動で切れる——画面から開き直す）。
//   - **書けるか**は WebDAV と同じ規則（ページへの書き込み権限・`webdav_readonly` の範囲——通信箱の下は届いた事実の
//     証拠なので書き換えない）。上書きの手順も WebDAV と同じ（空なら差し替えない → いまの中身を版に残す →
//     差し替える → 目録を進める → ページの更新時刻 → 監査 `local-edit.write`）。
//   - **ほかの人が先に書き換えていたら上書きしない**（ヘルパーは落としたときの ETag を If-Match で送る。違えば 409）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// AttachmentSourceLocalEdit は目録の出どころ「ローカルのアプリで上書きした」です。
const AttachmentSourceLocalEdit = "local-edit"

// localEditTTL は鍵の寿命です（使うたびに延びる）。
const localEditTTL = 12 * time.Hour

type localEditGrant struct {
	username string
	pageID   string
	stored   string
	expires  time.Time
}

var (
	localEditMu     sync.Mutex
	localEditGrants = map[string]*localEditGrant{}
)

func newLocalEditToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// grantOf は鍵を引きます（切れていれば消して nil）。使うたびに寿命を延ばす。
func grantOf(token string) *localEditGrant {
	localEditMu.Lock()
	defer localEditMu.Unlock()
	g := localEditGrants[token]
	if g == nil {
		return nil
	}
	if time.Now().After(g.expires) {
		delete(localEditGrants, token)
		return nil
	}
	g.expires = time.Now().Add(localEditTTL)
	return g
}

// localEditUser は鍵の持ち主です（権限はいまの状態で確かめ直す——鍵を出したあとに権限が変わることがある）。
func localEditUser(g *localEditGrant) *auth.User {
	u, err := auth.LookupUser(g.username)
	if err != nil {
		return nil
	}
	return u
}

// fileETag は中身の要約です（ヘルパーが If-Match で送り返す）。
func fileETag(content []byte) string {
	sum := sha256.Sum256(content)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// LocalEditStartAPIHandler は POST /api/local-edit/start です（入力 `{ref}`・ファイル表示の参照と同じ形
// 「ページ番号-添付ID」）。応答は `{success, link, name, writable}`——`link` は `w-cms-edit:<w-cms のアドレス>#<鍵>`。
func LocalEditStartAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Ref string `json:"ref"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	ref := strings.TrimSpace(req.Ref)
	i := strings.LastIndex(ref, "-")
	if i <= 0 {
		JSONFail(w, http.StatusBadRequest, "ref は「ページ番号-添付ID」の形です")
		return
	}
	fr := FileRef{PageID: ref[:i], ID: ref[i+1:]}
	stored, display, found := AttachmentOfRef(user, fr)
	if !found {
		JSONFail(w, http.StatusNotFound, "添付が見つかりません")
		return
	}
	pageID, _ := page.NormalizeID(fr.PageID)
	writable := davFS{user: user}.canWriteAttachment(pageID) == nil
	token, err := newLocalEditToken()
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, "鍵を作れません")
		return
	}
	localEditMu.Lock()
	// 切れた鍵を掃除する（数は多くない）。
	for k, g := range localEditGrants {
		if time.Now().After(g.expires) {
			delete(localEditGrants, k)
		}
	}
	localEditGrants[token] = &localEditGrant{username: user.Username, pageID: pageID, stored: stored,
		expires: time.Now().Add(localEditTTL)}
	localEditMu.Unlock()
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if display == "" {
		display = stored
	}
	auth.Audit(user.Username, "local-edit.start", pageID+"/"+stored)
	WriteJSON(w, map[string]any{
		"success":  true,
		"link":     "w-cms-edit:" + scheme + "://" + r.Host + "#" + token,
		"name":     display,
		"writable": writable,
	})
}

// LocalEditFileAPIHandler は /api/local-edit/file?token= です（ヘルパーが使う・ログインの外で鍵だけを確かめる）。
//
//	GET  … 中身を返す（`ETag`・`X-WCMS-Name`＝表示の名前〔URL エンコード〕・`X-WCMS-Writable`）
//	PUT  … 中身を上書きする（`If-Match` が要る——いまの ETag と違えば 409・書けなければ 403）
func LocalEditFileAPIHandler(w http.ResponseWriter, r *http.Request) {
	g := grantOf(r.URL.Query().Get("token"))
	if g == nil {
		http.Error(w, "鍵が切れています（w-cms の画面から開き直してください）", http.StatusUnauthorized)
		return
	}
	user := localEditUser(g)
	if user == nil {
		http.Error(w, "利用者が見つかりません", http.StatusUnauthorized)
		return
	}
	stored, display, found := AttachmentOfRef(user, FileRef{PageID: g.pageID, ID: strings.TrimSuffix(g.stored, filepath.Ext(g.stored))})
	if !found || stored != g.stored {
		http.Error(w, "添付が見つかりません（読めなくなったか、消えた）", http.StatusNotFound)
		return
	}
	realPath, ok := page.AttachmentPath(g.pageID, g.stored)
	if !ok {
		http.Error(w, "添付が見つかりません", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		content, err := os.ReadFile(realPath)
		if err != nil {
			http.Error(w, "添付を読めません", http.StatusInternalServerError)
			return
		}
		if display == "" {
			display = g.stored
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("ETag", fileETag(content))
		w.Header().Set("X-WCMS-Name", url.PathEscape(display))
		w.Header().Set("X-WCMS-Writable", boolString(davFS{user: user}.canWriteAttachment(g.pageID) == nil))
		w.Header().Set("Cache-Control", "no-store")
		w.Write(content)
	case http.MethodPut:
		etag, err := writeLocalEdit(user, g.pageID, g.stored, realPath, r.Header.Get("If-Match"), r.Body)
		if err != nil {
			var he httpError
			if errors.As(err, &he) {
				if he.etag != "" {
					w.Header().Set("ETag", he.etag)
				}
				http.Error(w, he.msg, he.code)
				return
			}
			http.Error(w, "上書きできません: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

type httpError struct {
	code int
	msg  string
	etag string
}

func (e httpError) Error() string { return e.msg }

func boolString(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// writeLocalEdit は添付を上書きします（WebDAV の上書きと同じ手順・webdav_write.go）。新しい ETag を返す。
func writeLocalEdit(user *auth.User, pageID, stored, realPath, ifMatch string, body io.Reader) (string, error) {
	if err := (davFS{user: user}).canWriteAttachment(pageID); err != nil {
		return "", httpError{code: http.StatusForbidden, msg: "この添付は書き換えられません（権限か、読み取り専用の範囲——設定 webdav_readonly）"}
	}
	if strings.TrimSpace(ifMatch) == "" {
		return "", httpError{code: http.StatusPreconditionRequired, msg: "If-Match が要ります"}
	}
	limit := MaxUploadBytes()
	content, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(content)) > limit {
		return "", httpError{code: http.StatusRequestEntityTooLarge, msg: "大きすぎます（設定 max_upload_mib）"}
	}
	// ⚠ 空なら差し替えない（開いただけで消えるのが、いちばん取り返しのつかない壊れ方——webdav_write.go と同じ）。
	if len(content) == 0 {
		return "", httpError{code: http.StatusBadRequest, msg: "中身が空です（差し替えません）"}
	}
	// 同じ添付への上書きは1つずつ（ETag の確かめと差し替えの間に割り込ませない）。
	localEditMu.Lock()
	defer localEditMu.Unlock()
	current, err := os.ReadFile(realPath)
	if err != nil {
		return "", err
	}
	if now := fileETag(current); now != strings.TrimSpace(ifMatch) {
		return "", httpError{code: http.StatusConflict, etag: now,
			msg: "ほかの誰かが先に書き換えています（上書きしません）——w-cms の画面から開き直してください"}
	}
	if fileETag(content) == fileETag(current) {
		return fileETag(content), nil // 変わっていない（版を積まない）
	}
	// 種類ごとの守り（画像は写真の位置情報を落とす——**戻り値のほうを保存する**）。
	if content, err = GuardUploadContent(stored, content); err != nil {
		return "", httpError{code: http.StatusBadRequest, msg: err.Error()}
	}
	// 1. いまの中身を版として残す（差し替える前に。ここで失敗したら上書きしない）。
	if err := saveAttachmentVersion(pageID, stored); err != nil {
		return "", err
	}
	// 2. 差し替える（書き終わってから rename——途中で切れたファイルを正本の位置に置かない）。
	tmp := realPath + ".local-edit.tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, realPath); err != nil {
		os.Remove(tmp)
		return "", err
	}
	// 3. 目録を進める。
	fresh := NewAttachmentMeta(stored, user.Username, AttachmentSourceLocalEdit, content)
	if err := UpdateAttachmentMeta(pageID, stored, func(m *AttachmentMeta) {
		if m.Name == "" {
			m.Name = stored
		}
		m.SavedAt, m.By, m.Size, m.SHA256, m.Source = fresh.SavedAt, fresh.By, fresh.Size, fresh.SHA256, fresh.Source
	}); err != nil {
		auth.Audit(user.Username, "attach.meta-failed", pageID+"/"+stored+": "+err.Error())
	}
	// 4. ページを触ったことにする（添付を直しても本文は変わらない）。
	if _, err := page.BumpUpdatedAt(pageID); err != nil {
		auth.Audit(user.Username, "local-edit.touch-failed", pageID+"/"+stored+": "+err.Error())
	}
	auth.Audit(user.Username, "local-edit.write", pageID+"/"+stored)
	return fileETag(content), nil
}
