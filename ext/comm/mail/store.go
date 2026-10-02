package mail

// ─────────────────────────────────────────────────────────────────────────
// トークンの保管——**ここだけが秘密を持ちます**（2026-09-03）
//
// 置くのはリフレッシュトークン1つだけです。アクセストークンは記憶の中の
// 期限つき保管に留め、ディスクへは書きません（漏れる面を狭くする）。
//
// 保管先は `data/mail/<利用者>.json`（`data/` は .gitignore 対象・日本語の名前は `~<16進>.json`——tokenPath）。
// 権限は 0600 で作りますが、**Windows では POSIX の権限どおりには効きません**
// ——サーバーを置く機械のアクセス制御が実質の守りです。運用の前提として
// デプロイ手順に書くこと。
//
// **トークンはログにも応答にも出しません。** 表に出すのは「誰としてサインイン
// しているか」（メールアドレス）と、いつ更新したかだけです。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

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

// maxEncodedNameBytes は16進で保管名にする利用者名の長さの上限です（UTF-8 のバイト数）。
// 16進で倍になるので、ファイル名が長くなりすぎない所で止めます。
const maxEncodedNameBytes = 96

// tokenPath は保管先を返します（名前が使えなければ空）。
//
// ASCII の名前（英数字と `._-`）はそのまま `<名前>.json`。**それ以外（日本語の名前など）は `~` ＋ UTF-8 の16進**で
// `~<16進>.json` にします（2026-10-02——ログイン名を連絡帳の人のページの題に合わせると決めたら、「姓 名」の
// ような名前ではサインインを保管も読み出しもできず、メールが読めなかった）。⚠ `~` は ASCII の形に使えない文字
// なので、2つの形はぶつかりません。16進なので `../` も作れません。
func tokenPath(username string) string {
	if safeName.MatchString(username) {
		return filepath.Join("data", "mail", username+".json")
	}
	if name := encodedName(username); name != "" {
		return filepath.Join("data", "mail", name+".json")
	}
	return ""
}

// encodedName は ASCII でない利用者名の保管名（拡張子なし）を返します（使えない名前なら空）。
func encodedName(username string) string {
	if username == "" || len(username) > maxEncodedNameBytes || !utf8.ValidString(username) {
		return ""
	}
	for _, r := range username {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return "~" + hex.EncodeToString([]byte(username))
}

// usernameOfTokenFile は保管のファイル名（`.json` を除いた部分）から利用者名を返します（tokenPath の逆・
// 読めなければ空）。
func usernameOfTokenFile(base string) string {
	if !strings.HasPrefix(base, "~") {
		return base
	}
	b, err := hex.DecodeString(base[1:])
	if err != nil {
		return ""
	}
	// ⚠ ASCII の名前は `~` の形では保管しないので、その形のファイルは読みません（同じ人が2つに見えないように）。
	if u := string(b); !safeName.MatchString(u) && encodedName(u) == base {
		return u
	}
	return ""
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

// deleteToken は保存を捨てます。
//
// **自動では呼びません**（2026-09-05）。更新に失敗しただけで捨てると、
// 同意が足りないとき（`AADSTS65001`）にサインインごと消えます——実際に、
// 受信を IMAP へ移して権限が増えた日にそれで消しました。捨てても得は無く、
// サインインし直せば上書きされます。将来サインアウトの口を作るときの土台として残します。
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

// signedInAddresses はサインインしている全員のアドレスです（保管のファイルを見る・トークンには触れない）。
// 通信箱の `.eml` の取り込みが「自分が出したメール」を見分けるために使います（comm.RegisterOwnAddresses）。
func signedInAddresses() []string {
	entries, err := os.ReadDir(filepath.Join("data", "mail"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		u := usernameOfTokenFile(strings.TrimSuffix(name, ".json"))
		if u == "" {
			continue
		}
		if a := SignedInAddress(u); a != "" {
			out = append(out, a)
		}
	}
	return out
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
