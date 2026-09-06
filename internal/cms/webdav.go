package cms

// ─────────────────────────────────────────────────────────────────────────
// WebDAV——添付をネットワークドライブとして見せる（2026-09-07・**読み取り専用**）
//
// ユーザー:「ブラウザでファイルをクリックするとローカルのアプリが立ち上がり、
// Ctrl＋Sで上書き保存されるような使い方をワンノートでしています」。
//
// **ローカルアプリの起動はブラウザにはできません**（できたら攻撃です）。輪を閉じる
// 主体は必ず「OSに登録された何か」——ここではネットワークドライブです。エクスプローラで
// 割り当てれば、そこから開いたファイルは**アプリにとって普通のファイル**になり、
// Ctrl+S が素直に効きます。
//
// **まず読み取り専用で出します。** 書き込みを許す前に決めることが2つ残っており
// （添付の版・同時編集）、それより先に **Windows の WebDAV クライアントの癖**を
// 実物で出させるのが目的です（50MBの既定上限・平文HTTPでの Basic 認証拒否・
// 認証切れの無言の失敗）。正本は
// [docs/【考察】添付をローカルアプリで編集する.md]。
//
// **書けるようにするときは、置き場所で線を引くこと。** ユーザー:「編集するCADファイルは
// 弊社の物です。メール由来のものではありません」——通信箱の下の添付は**届いた事実の
// 証拠**なので読み取り専用のまま、部品ページなどに人が置いたファイルだけ書けるように
// します。「記録は動かしません」の添付版で、**ファイルごとの印は要りません**。
//
// ── 依存について ──
//
// `golang.org/x/net/webdav` を使います。`x/net` は**既に直接依存**（HTMLパース）なので
// **新しいモジュールは増えません**（開発方針 §1-②）。
//
// ── 認証について ──
//
// エクスプローラは**セッションCookieを送りません**（ブラウザではないので）。したがって
// HTTP Basic になります。**平文HTTPでは、要求のたびに合言葉が流れます**——だから
// この口は**既定で閉じてあり**、`WCMS_DAV=1` を置いたときだけ開きます。
// **本番で開けるなら HTTPS が前提**です。
// ─────────────────────────────────────────────────────────────────────────

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/webdav"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// DavPrefix は WebDAV の入口です。
const DavPrefix = "/dav/"

// DavEnabled は WebDAV の口を開けるかを返します（既定は閉じています）。
//
// **明示的に開けるまで出しません。** 平文HTTPでの Basic 認証は合言葉を毎回流すので、
// 「気づかないうちに開いていた」が起きない形にしてあります。
func DavEnabled() bool { return os.Getenv("WCMS_DAV") == "1" }

// davLocks は WebDAV のロック置き場です。読み取り専用の間は使われませんが、
// ハンドラが要求するので1つ持ちます（要求ごとに作ると意味を成しません）。
var (
	davLocksOnce sync.Once
	davLocks     webdav.LockSystem
)

func davLockSystem() webdav.LockSystem {
	davLocksOnce.Do(func() { davLocks = webdav.NewMemLS() })
	return davLocks
}

// davWriteMethods は本文を変える要求です。**読み取り専用の間は全部断ります。**
var davWriteMethods = map[string]bool{
	"PUT": true, "DELETE": true, "MKCOL": true, "MOVE": true,
	"COPY": true, "PROPPATCH": true, "LOCK": true, "UNLOCK": true,
}

// DavHandler は `/dav/` 以下に**ページの木をそのまま**見せます。
//
// もとは `/dav/<ページID>/` の1ページずつでした。変えた理由は2つです（2026-09-07）:
//
//  1. **フォルダ名がページID**（`010267`）で、どの部品か分からない。1ページ＝1割り当て
//     なので、20部品なら20文字のドライブレターが要る。
//  2. **「読めないページが漏れる」という避けた理由が成り立たなかった。** ブラウザの
//     子ページ一覧が既に権限で絞った木を見せているので、同じ絞り方をすれば
//     新しく漏れるものはありません（`visibleChildren`）。
//
// **見せないものは設定が決めます**（`webdav_hidden`）。ユーザー:「通信箱も取引先も
// プラグインの領域ですが、クリックして開くは w-cms 本体の機能なので、設定で見せないと
// するもの以外は見せて良いのでは？」——コアが `通信箱` を名前で特別扱いすると、
// **仕組みの側に語彙が漏れます**。
func DavHandler(w http.ResponseWriter, r *http.Request) {
	if !DavEnabled() {
		http.NotFound(w, r)
		return
	}
	// **試用のあいだは全部記録します。** Windows のクライアントは失敗しても画面に
	// 「システムエラー 67」としか出さないので、**要求が届いたのかどうか**が分からないと
	// 何も切り分けられません（届かない＝OSが手前で断った、届いた＝こちらの返事の問題）。
	log.Printf("WebDAV %s %s auth=%v", r.Method, r.URL.Path, r.Header.Get("Authorization") != "")

	// **書き込みは全部断ります**（読み取り専用の期間）。405 ではなく 403 なのは、
	// 「その要求は理解したが、許していない」を伝えるためです。
	if davWriteMethods[strings.ToUpper(r.Method)] {
		http.Error(w, "いまは読み取り専用です", http.StatusForbidden)
		return
	}

	user := davAuthenticate(w, r)
	if user == nil {
		return // davAuthenticate が 401 を返しています
	}

	// **トップを読めない人には何も見せません。** 木の入口が読めないなら、
	// その先も辿れません（各段でも `visibleChildren` が絞ります）。
	topInt, err := strconv.Atoi(TopPageID)
	if err != nil || !page.GetPerms(topInt).CanRead(user) {
		http.NotFound(w, r)
		return
	}

	h := &webdav.Handler{
		Prefix:     strings.TrimSuffix(DavPrefix, "/"),
		FileSystem: davFS{user: user},
		LockSystem: davLockSystem(),
		Logger: func(req *http.Request, err error) {
			if err != nil {
				log.Printf("WebDAV %s %s: %v", req.Method, req.URL.Path, err)
			}
		},
	}
	h.ServeHTTP(w, r)
}

// davAuthenticate は HTTP Basic で利用者を確かめます。
//
// **セッションCookieは使えません**——エクスプローラはブラウザではないので送りません。
// 失敗したときに `WWW-Authenticate` を返すのが、クライアントに合言葉を尋ねさせる合図です。
func davAuthenticate(w http.ResponseWriter, r *http.Request) *auth.User {
	username, password, ok := r.BasicAuth()
	if !ok || username == "" {
		davChallenge(w)
		return nil
	}
	user, err := auth.Authenticate(username, password)
	if err != nil || user == nil {
		// **総当たりの数えは Authenticate が持っています**（isLockedOut）。ここは
		// 記録だけ——ログイン画面と同じ経路を通るので、締め出しもそのまま効きます。
		auth.Audit(username, "dav.login.fail", r.RemoteAddr)
		davChallenge(w)
		return nil
	}
	return user
}

// davChallenge は 401 と認証の要求を返します。
func davChallenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="w-cms", charset="UTF-8"`)
	http.Error(w, "認証が必要です", http.StatusUnauthorized)
}
