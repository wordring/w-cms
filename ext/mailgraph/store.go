package mailgraph

// ─────────────────────────────────────────────────────────────────────────
// トークンの保管——**ここだけが秘密を持ちます**（2026-09-03）
//
// 置くのはリフレッシュトークン1つだけです。アクセストークンは記憶の中の
// 期限つき保管に留め、ディスクへは書きません（漏れる面を狭くする）。
//
// 保管先は `data/mail/<利用者>.json`（`data/` は .gitignore 対象）。
// 権限は 0600 で作りますが、**Windows では POSIX の権限どおりには効きません**
// ——サーバーを置く機械のアクセス制御が実質の守りです。運用の前提として
// デプロイ手順に書くこと。
//
// **トークンはログにも応答にも出しません。** 表に出すのは「誰としてサインイン
// しているか」（メールアドレス）と、いつ更新したかだけです。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"w-cms/internal/cms/page"
)

// storedToken はディスクに置く中身です。
type storedToken struct {
	RefreshToken string `json:"refresh_token"`
	Address      string `json:"address"`    // 表示用（誰としてサインインしているか）
	UpdatedAt    string `json:"updated_at"` // ローカル時刻のISO表記
}

// storeMu は同じ利用者への同時書き込みを直列化します
// （送信の最中にトークンが更新されることがある）。
var storeMu sync.Mutex

// safeName は利用者名をファイル名に使える形に絞ります。
// **通らない文字は落とさず弾きます**——`../` のような名前で保管先の外へ
// 書かせないためで、添付名の検査（SafeAttachmentName）と同じ規律です。
var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// tokenPath は保管先を返します（名前が使えなければ空）。
func tokenPath(username string) string {
	if !safeName.MatchString(username) {
		return ""
	}
	return filepath.Join("data", "mail", username+".json")
}

// saveToken はトークンを保存します。
func saveToken(username string, st storedToken) error {
	path := tokenPath(username)
	if path == "" {
		return os.ErrInvalid
	}
	storeMu.Lock()
	defer storeMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	// 書き込みは原子的に（途中で落ちても壊れた保管を残さない）。
	return page.WriteFileAtomic(path, b, 0600)
}

// loadToken は保存済みのトークンを読みます。
func loadToken(username string) (storedToken, bool) {
	path := tokenPath(username)
	if path == "" {
		return storedToken{}, false
	}
	storeMu.Lock()
	defer storeMu.Unlock()

	b, err := os.ReadFile(path)
	if err != nil {
		return storedToken{}, false
	}
	var st storedToken
	if err := json.Unmarshal(b, &st); err != nil || st.RefreshToken == "" {
		return storedToken{}, false
	}
	return st, true
}

// deleteToken は保存を捨てます（サインアウト・更新に失敗したとき）。
func deleteToken(username string) {
	path := tokenPath(username)
	if path == "" {
		return
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	os.Remove(path)
	// 保管はスコープごとなので、その利用者ぶんを全部落とします。
	accessCache.Range(func(k, _ any) bool {
		if s, ok := k.(string); ok && strings.HasPrefix(s, username+"|") {
			accessCache.Delete(k)
		}
		return true
	})
}

// SignedInAddress は、その利用者がどのアドレスでサインインしているかを返します
// （サインインしていなければ空）。**トークンそのものは返しません。**
func SignedInAddress(username string) string {
	st, ok := loadToken(username)
	if !ok {
		return ""
	}
	return st.Address
}
