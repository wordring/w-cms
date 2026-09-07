package cms

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// davRequest は WebDAV の要求を1つ投げます（合言葉は任意）。
//
// パスは**日本語を含みます**（ページの題がそのままフォルダ名）。実際のクライアントと
// 同じになるよう、区画ごとにURLエンコードして組み立てます。
func davRequest(t *testing.T, method string, segments []string, user, pass string) *httptest.ResponseRecorder {
	return davRequestBody(t, method, segments, user, pass, "")
}

// davRequestBody は本文つきで投げます（PUT の確認用）。
func davRequestBody(t *testing.T, method string, segments []string, user, pass, body string) *httptest.ResponseRecorder {
	t.Helper()
	p := DavPrefix
	for i, s := range segments {
		if i > 0 {
			p += "/"
		}
		p += url.PathEscape(s)
	}
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, p, nil)
	} else {
		req = httptest.NewRequest(method, p, strings.NewReader(body))
	}
	if user != "" {
		req.Header.Set("Authorization", "Basic "+
			base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	}
	rr := httptest.NewRecorder()
	DavHandler(rr, req)
	return rr
}

// davPage はページを1枚作り、添付を1つ置きます（添付名が空なら置きません）。
func davPage(t *testing.T, id, title, owner, mode, parent, attach string) {
	t.Helper()
	newPage(t, id, "<h1>"+title+"</h1>", page.PageMeta{
		Owner: owner, Mode: mode, ParentID: parent})
	if attach == "" {
		return
	}
	dir := page.AttachmentDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("添付フォルダを作れません: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, attach), []byte("0\nSECTION\n"), 0o644); err != nil {
		t.Fatalf("添付を作れません: %v", err)
	}
}

// setupDavTest は木を1つ組み、WebDAV を開けた状態にします。
//
//	トップ
//	├ 部品A       （添付 a1b2.dxf）
//	├ RE: 見積り   （題に Windows で使えない文字）
//	└ 秘密        （alice だけ・bob からは見えない）
//
// **ファイルDBを使います**——`visibleChildren` は `:memory:` では動きません
// （行を読みながら別のクエリを投げると別の空DBに当たる。引き継ぎの罠）。
//
// **認証DBも用意します**——WebDAV は Cookie ではなく HTTP Basic を通るので、
// 利用者を注入する他のテストと違い、本物の照合が走ります。
func setupDavTest(t *testing.T) {
	t.Helper()
	setupTemplateAPITest(t)
	t.Setenv("WCMS_DAV", "1")
	restoreSettings(t)

	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: "333"})
	davPage(t, "000101", "部品A", "alice", "333", TopPageID, "a1b2.dxf")
	davPage(t, "000102", "RE: 見積り", "alice", "333", TopPageID, "")
	davPage(t, "000103", "秘密", "alice", "300", TopPageID, "")
	davPage(t, "000104", "読むだけ", "alice", "332", TopPageID, "c3d4.dxf")

	if err := database.InitAuthDB(); err != nil {
		t.Fatalf("認証DBを用意できません: %v", err)
	}
	t.Cleanup(func() { database.AuthDB.Close() })
	for _, u := range []string{"alice", "bob"} {
		if err := auth.CreateUser(u, "pw", false, ""); err != nil {
			t.Fatalf("利用者を作れません: %v", err)
		}
	}
}

// withSettings は設定を**写しごと差し替えて**変更を当てます。
//
// **中身を直に書き換えてはいけません**——`settings` は共有され、`restoreSettings` が
// 戻すのは**ポインタだけ**なので、フィールドを直接いじると後続のテストへ漏れます
// （実際に漏らして、単独では通るのに並べると落ちるテストを作りました）。
// settings.go 自身が「差し替えは常にポインタごと」と書いている規律そのものです。
func withSettings(t *testing.T, apply func(*Settings)) {
	t.Helper()
	settingsMu.Lock()
	cp := *settings
	apply(&cp)
	settings = &cp
	settingsMu.Unlock()
}

// TestDavClosedByDefault は、**環境変数を置くまで口が開かない**ことを固定します。
//
// 平文HTTPでの Basic 認証は要求のたびに合言葉を流すので、「気づかないうちに開いて
// いた」が起きない形にしてあります（2026-09-07）。
func TestDavClosedByDefault(t *testing.T) {
	setupDavTest(t)
	t.Setenv("WCMS_DAV", "")

	rr := davRequest(t, "GET", []string{"部品A", "a1b2.dxf"}, "alice", "pw")
	if rr.Code != http.StatusNotFound {
		t.Errorf("既定で開いています: %d", rr.Code)
	}
}

// TestDavRequiresAuth は、合言葉が無ければ 401 と認証の要求を返すことを固定します。
// エクスプローラはこの返事を見て合言葉を尋ねます——無いと黙って失敗します。
func TestDavRequiresAuth(t *testing.T) {
	setupDavTest(t)

	rr := davRequest(t, "PROPFIND", nil, "", "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("認証を求めていません: %d", rr.Code)
	}
	if !strings.HasPrefix(rr.Header().Get("WWW-Authenticate"), "Basic ") {
		t.Errorf("認証の要求が返っていません: %q", rr.Header().Get("WWW-Authenticate"))
	}
}

// TestDavBlocksDestructiveMethods は、**消す・作る・動かすを通さない**ことを固定します。
//
// Ctrl+S の輪に要らず、事故のとき取り返しがつきにくいためです（`DELETE` は添付を消し、
// `MOVE` は行方を分からなくします）。**上書き（PUT）と LOCK は通します**。
func TestDavBlocksDestructiveMethods(t *testing.T) {
	setupDavTest(t)

	for _, m := range []string{"DELETE", "MKCOL", "MOVE", "COPY", "PROPPATCH"} {
		rr := davRequest(t, m, []string{"部品A", "a1b2.dxf"}, "alice", "pw")
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s を断っていません: %d", m, rr.Code)
		}
	}
}

// TestDavOverwriteKeepsVersion は、**上書きすると前の中身が版として残る**ことを
// 固定します（2026-09-07 の決定1）。
//
// ページ本文には版があるのに添付に無いと、編集ミスも「間違ったファイルを開いた」も
// 取り返せません。**ここが効かないまま書き込みを許すのがいちばん危ない状態**です。
func TestDavOverwriteKeepsVersion(t *testing.T) {
	setupDavTest(t)

	// **更新時刻はRFC3339（秒まで）**なので、同じ秒に書くと変化が見えません。
	// 古い値を置いてから確かめます（時間に依らない形にする）。
	meta, _ := page.ReadSidecar("000101")
	meta.UpdatedAt = "2000-01-01T00:00:00Z"
	if err := page.WriteSidecar("000101", meta); err != nil {
		t.Fatalf("前提を作れません: %v", err)
	}

	rr := davRequestBody(t, "PUT", []string{"部品A", "a1b2.dxf"}, "alice", "pw", "0 NEW ")
	if rr.Code != http.StatusCreated && rr.Code != http.StatusNoContent {
		t.Fatalf("上書きできていません: %d %s", rr.Code, rr.Body.String())
	}

	// 新しい中身になっている。
	fp, _ := page.AttachmentPath("000101", "a1b2.dxf")
	got, _ := os.ReadFile(fp)
	if !strings.Contains(string(got), "NEW") {
		t.Errorf("中身が変わっていません: %q", got)
	}
	// **前の中身が版として残っている。**
	vers := AttachmentVersions("000101", "a1b2.dxf")
	if len(vers) != 1 {
		t.Fatalf("版が積まれていません: %+v", vers)
	}
	old, _ := os.ReadFile(filepath.Join(
		page.AttachmentDir("000101"), davVersionsDir, "a1b2.dxf", vers[0]))
	if !strings.Contains(string(old), "SECTION") {
		t.Errorf("版の中身が前のものではありません: %q", old)
	}
	// **ページを触ったことになっている**（添付を直しても本文は変わらないため）。
	after, _ := page.ReadSidecar("000101")
	if after.UpdatedAt == "2000-01-01T00:00:00Z" {
		t.Error("更新時刻が進んでいません（いつ直したか分からなくなる）")
	}
	// **版は WebDAV の一覧に出さない。**
	rr = davRequest(t, "PROPFIND", []string{"部品A"}, "alice", "pw")
	if strings.Contains(rr.Body.String(), davVersionsDir) {
		t.Errorf("版の置き場が一覧に出ています: %s", rr.Body.String())
	}
}

// TestDavReadOnlyArea は、**設定で読み取り専用にした範囲は書けない**ことを固定します
// （2026-09-07 ユーザー:「編集するCADファイルは弊社の物です。メール由来のものでは
// ありません」）。届いた添付は届いた事実の証拠なので、書き換えさせません。
func TestDavReadOnlyArea(t *testing.T) {
	setupDavTest(t)
	withSettings(t, func(s *Settings) { s.WebDAVReadOnly = []string{"部品A"} })

	rr := davRequestBody(t, "PUT", []string{"部品A", "a1b2.dxf"}, "alice", "pw", "x")
	if rr.Code == http.StatusCreated || rr.Code == http.StatusNoContent {
		t.Fatalf("読み取り専用の範囲に書けています: %d", rr.Code)
	}
	fp, _ := page.AttachmentPath("000101", "a1b2.dxf")
	got, _ := os.ReadFile(fp)
	if !strings.Contains(string(got), "SECTION") {
		t.Errorf("中身が変わっています: %q", got)
	}
}

// TestDavWriteNeedsPermission は、**読めるだけでは書けない**ことを固定します。
func TestDavWriteNeedsPermission(t *testing.T) {
	setupDavTest(t)

	// bob は「読むだけ」を読める（other=2）が、書けない。
	if rr := davRequest(t, "GET", []string{"読むだけ", "c3d4.dxf"}, "bob", "pw"); rr.Code != http.StatusOK {
		t.Fatalf("前提が崩れています: bob は読めるはず: %d", rr.Code)
	}
	rr := davRequestBody(t, "PUT", []string{"読むだけ", "c3d4.dxf"}, "bob", "pw", "x")
	if rr.Code == http.StatusCreated || rr.Code == http.StatusNoContent {
		t.Errorf("書き込み権限が無いのに書けています: %d", rr.Code)
	}
}

// TestDavCannotCreateNewFile は、**新しいファイルを作らせない**ことを固定します。
//
// 作れるようにすると、拡張子の許可リストと大きさの上限を素通りします——そこは
// 別に決めることで、いまの輪（Ctrl+S）には要りません。
func TestDavCannotCreateNewFile(t *testing.T) {
	setupDavTest(t)

	rr := davRequestBody(t, "PUT", []string{"部品A", "新しい.dxf"}, "alice", "pw", "x")
	if rr.Code == http.StatusCreated || rr.Code == http.StatusNoContent {
		t.Errorf("新しいファイルが作れています: %d", rr.Code)
	}
	if _, ok := page.AttachmentPath("000101", "新しい.dxf"); ok {
		t.Error("新しいファイルが置かれています")
	}
}

// TestDavServesTree は、**ページの木がフォルダとして見える**ことを固定します
// （2026-09-07 にページID1枚ずつから変更）。
func TestDavServesTree(t *testing.T) {
	setupDavTest(t)

	rr := davRequest(t, "PROPFIND", nil, "alice", "pw")
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("入口の一覧が返りません: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), url.PathEscape("部品A")) {
		t.Errorf("入口に部品Aが出ていません: %s", rr.Body.String())
	}

	// 題をたどって添付が読める。
	rr = davRequest(t, "GET", []string{"部品A", "a1b2.dxf"}, "alice", "pw")
	if rr.Code != http.StatusOK {
		t.Fatalf("添付を配れていません: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "SECTION") {
		t.Errorf("中身が違います: %q", rr.Body.String())
	}
}

// TestDavHidesUnreadablePage は、**読めないページが木に出ない**ことを固定します。
// 見えてしまうと、題（＝業務の情報）がそのまま漏れます。
func TestDavHidesUnreadablePage(t *testing.T) {
	setupDavTest(t)

	rr := davRequest(t, "PROPFIND", nil, "bob", "pw")
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("一覧が返りません: %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), url.PathEscape("秘密")) {
		t.Errorf("読めないページが見えています: %s", rr.Body.String())
	}
	if rr := davRequest(t, "PROPFIND", []string{"秘密"}, "bob", "pw"); rr.Code != http.StatusNotFound {
		t.Errorf("読めないページへ直接たどれています: %d", rr.Code)
	}
}

// TestDavHiddenBySettings は、**設定で見せないと言ったページが消える**ことを固定します
// （2026-09-07 ユーザー:「設定で見せないとするもの以外は見せて良いのでは？」）。
//
// ここが効かないと、コアが `通信箱` を名前で特別扱いする——**仕組みの側に語彙が漏れる**
// 形へ戻ってしまいます。
func TestDavHiddenBySettings(t *testing.T) {
	setupDavTest(t)
	withSettings(t, func(s *Settings) { s.WebDAVHidden = []string{"部品A"} })

	rr := davRequest(t, "PROPFIND", nil, "alice", "pw")
	if strings.Contains(rr.Body.String(), url.PathEscape("部品A")) {
		t.Errorf("設定で隠したページが見えています: %s", rr.Body.String())
	}
	if rr := davRequest(t, "GET", []string{"部品A", "a1b2.dxf"}, "alice", "pw"); rr.Code == http.StatusOK {
		t.Error("隠したページの添付が読めています")
	}
}

// TestSafeFolderName は、**題をフォルダ名へ付け替える規則**を固定します。
//
// 実データで50件がWindowsの使えない文字を含み（`RE: …` のコロン）、14件が同じ親の下で
// 重複しています。潰し方を変えると、**割り当て済みのドライブのパスが全部変わります**。
func TestSafeFolderName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"RE: 見積り", "RE： 見積り"},  // コロンは全角へ
		{"A/B", "A／B"},          // スラッシュも
		{"  余白  ", "余白"},        // 前後の空白
		{"末尾のドット...", "末尾のドット"}, // Windows が黙って落とすので先に落とす
		{"CON", "CON_"},         // 予約語
		{"", ""},
	}
	for _, c := range cases {
		if got := safeFolderName(c.in); got != c.want {
			t.Errorf("safeFolderName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDavRefusesForwarded は、**プロキシを通ってきた要求を断る**ことを固定します
// （2026-09-07。VPS＋リバースプロキシで公開する構成に決まった日）。
//
// 「WebDAV は公開しない」はプロキシの設定で実現しますが、**設定だけに頼りません**
// ——`location /dav/` を書き忘れた日に、Basic 認証の口が黙って世界に開きます。
func TestDavRefusesForwarded(t *testing.T) {
	setupDavTest(t)

	for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Real-IP", "Forwarded"} {
		req := httptest.NewRequest("GET", DavPrefix+url.PathEscape("部品A")+"/a1b2.dxf", nil)
		req.Header.Set("Authorization", "Basic "+
			base64.StdEncoding.EncodeToString([]byte("alice:pw")))
		req.Header.Set(h, "203.0.113.9")
		rr := httptest.NewRecorder()
		DavHandler(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s が付いた要求を通しています: %d", h, rr.Code)
		}
	}

	// **逃げ道は残します**——プロキシ経由でしか届かない構成にしたくなったとき用。
	t.Setenv("WCMS_DAV_ALLOW_FORWARDED", "1")
	req := httptest.NewRequest("GET", DavPrefix+url.PathEscape("部品A")+"/a1b2.dxf", nil)
	req.Header.Set("Authorization", "Basic "+
		base64.StdEncoding.EncodeToString([]byte("alice:pw")))
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rr := httptest.NewRecorder()
	DavHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("逃げ道が効いていません: %d", rr.Code)
	}
}
