package mailgraph

// ─────────────────────────────────────────────────────────────────────────
// メールを受信箱へ取り込む（2026-09-03）
//
// 取ってきた生のMIMEを**既存の取り込み係へそのまま渡します**（cms.IntakeFile）。
// 人が `.eml` をドロップしたときと同じ道なので、封筒タグ・スレッドの繋ぎ・
// 添付の展開・重複検知が丸ごと効きます。
//
// **重複は落とす前に弾きます。** 一覧では `internetMessageId` だけ読み、索引の
// 逆引きで取り込み済みと分かれば本体を取りに行きません——過去メールの一括取り込みで
// 同じものを何度も落とさないための要点です（実測: 300通で105MB。無駄に落とすと
// 効きます）。
//
// **起動は人の指先だけ**——自動で回し続けません（§3 人間ゲート型）。実測した
// 流量は 1.2通/日なので、気づいたときに押せば十分追いつきます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// ImportSummary は取り込み1回の結果です。
type ImportSummary struct {
	Listed    int      `json:"listed"`    // 一覧で見た件数
	Imported  int      `json:"imported"`  // 新しく取り込んだ件数
	Duplicate int      `json:"duplicate"` // 既に取り込み済みだった件数
	Failed    int      `json:"failed"`    // 取り込めなかった件数
	Titles    []string `json:"titles"`    // 取り込んだものの題（先頭のいくつか）
}

// importMax は1回の取り込みの上限です。**押すたびに少しずつ**進める形にして、
// 1回の要求が長く居座らないようにします（過去分は繰り返し押せば追いつく）。
const importMax = 50

// ImportMessages は未取り込みのメールを受信箱へ取り込みます。
func ImportMessages(ctx context.Context, username string, opt ListOptions) (ImportSummary, error) {
	var sum ImportSummary

	inboxID, ok := cms.InboxPageID()
	if !ok {
		return sum, errNoInbox
	}
	if opt.Max <= 0 || opt.Max > importMax {
		opt.Max = importMax
	}
	refs, err := ListMessages(ctx, username, opt)
	if err != nil {
		return sum, err
	}
	sum.Listed = len(refs)

	// 古いものから取り込みます——**スレッドの親が先に来る**ほうが、
	// 返信元メッセージIDの逆引きが最初から繋がります。
	for i := len(refs) - 1; i >= 0; i-- {
		r := refs[i]
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		// **落とす前に重複を確かめる**（本体は数百KBある）。
		if id := strings.TrimSpace(r.InternetMessageID); id != "" {
			if _, dup := cms.ExistingIntakePage(cms.MessageIDTag, id); dup {
				sum.Duplicate++
				continue
			}
		}
		raw, err := FetchRawMIME(ctx, username, r.ID)
		if err != nil {
			log.Printf("メールを取得できませんでした subject=%q: %v", r.Subject, err)
			sum.Failed++
			continue
		}
		// ファイル名は取り込み係が拡張子で担当を決めるためのもの。
		// 題は本文（件名）から作られるので、ここは形だけで足ります。
		res, handled, err := cms.IntakeFile(inboxID, username, "mail.eml", raw)
		if err != nil || !handled {
			if err != nil {
				log.Printf("メールを取り込めませんでした subject=%q: %v", r.Subject, err)
			}
			sum.Failed++
			continue
		}
		if res.Duplicate {
			sum.Duplicate++
			continue
		}
		sum.Imported++
		if len(sum.Titles) < 20 {
			sum.Titles = append(sum.Titles, res.Title)
		}
	}
	return sum, nil
}

// errNoInbox は受信箱ページが無い印です（トップ直下に「受信箱」という名前のページ）。
var errNoInbox = errNoInboxErr{}

type errNoInboxErr struct{}

func (errNoInboxErr) Error() string {
	return "受信箱ページがありません（トップ直下に「受信箱」という名前のページを作ってください）"
}

// MailImportAPIHandler は POST /api/mail/import です。
// 入力（省略可）: {folder, max, since}
func MailImportAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	var req struct {
		Folder string `json:"folder"`
		Max    int    `json:"max"`
		Since  string `json:"since"`
	}
	// 本体が空でも既定で動きます（受信箱・50件）。
	json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	sum, err := ImportMessages(ctx, user.Username, ListOptions{
		Folder: req.Folder, Max: req.Max, Since: req.Since,
	})
	if err != nil {
		if err == errNotSignedIn {
			cms.JSONFail(w, 0, "メールアカウントにサインインしていません")
			return
		}
		cms.JSONFail(w, 0, err.Error())
		return
	}
	auth.Audit(user.Username, "mail.import",
		"listed="+itoa(sum.Listed)+" imported="+itoa(sum.Imported)+
			" duplicate="+itoa(sum.Duplicate)+" failed="+itoa(sum.Failed))

	json.NewEncoder(w).Encode(map[string]any{"success": true, "summary": sum})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
