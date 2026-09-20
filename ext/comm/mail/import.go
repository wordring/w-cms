package mail

// ─────────────────────────────────────────────────────────────────────────
// メールを通信箱へ取り込む（2026-09-03）
//
// 取ってきた生のMIMEを**既存の取り込み係へそのまま渡します**（comm.IntakeFile）。
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
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"w-cms/ext/comm"
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

// listAllMax は一覧で見る上限です。**取り込む上限とは別**——一覧は封筒の見出し
// だけなので安く、範囲を全部見てから「まだ入っていないもの」を選ぶほうが、
// 押し直したときに確実に前へ進みます。実測の流量は年435通なので、
// これで10年分を一度に見渡せます。
const listAllMax = 5000

// ImportMessages は未取り込みのメールを通信箱へ取り込みます。
func ImportMessages(ctx context.Context, username string, opt ListOptions) (ImportSummary, error) {
	var sum ImportSummary

	inboxID, ok := comm.MailBoxPageID()
	if !ok {
		return sum, comm.ErrNoMailBox
	}
	// **Max は「取り込む上限」であって「見る上限」ではありません。**
	// 見るほうを絞ると、押し直しても同じ新しい50件を見て「全部重複」で止まり、
	// 古いほうへ進めません（実際にそう作ってしまい、進まないことで気づいた）。
	// 一覧は封筒の見出しだけなので、範囲を全部見ても安いのです。
	limit := opt.Max
	if limit <= 0 || limit > importMax {
		limit = importMax
	}
	listOpt := opt
	listOpt.Max = listAllMax

	// **接続は1本で通します。** IMAP は状態を持つ（認証・開いた箱）ので、
	// 1通ごとに繋ぎ直すのは無駄です。
	sess, err := openIMAP(ctx, username)
	if err != nil {
		return sum, err
	}
	defer sess.Close()

	refs, err := sess.list(listOpt)
	if err != nil {
		return sum, err
	}
	sum.Listed = len(refs)

	// 古いものから取り込みます——**スレッドの親が先に来る**ほうが、
	// 返信元メッセージIDの逆引きが最初から繋がります。
	for i := len(refs) - 1; i >= 0; i-- {
		if sum.Imported >= limit {
			break // 続きは次に押したときへ（1回の要求が長く居座らない）
		}
		r := refs[i]
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		// **落とす前に重複を確かめる**（本体は数百KBある）。
		if id := strings.TrimSpace(r.MessageID); id != "" {
			if _, dup := comm.ExistingIntakePage(comm.MessageIDTag, id); dup {
				sum.Duplicate++
				continue
			}
		}
		raw, err := sess.fetchRaw(r.UID)
		if err != nil {
			log.Printf("メールを取得できませんでした subject=%q: %v", r.Subject, err)
			sum.Failed++
			continue
		}
		// ファイル名は取り込み係が拡張子で担当を決めるためのもの。
		// 題は本文（件名）から作られるので、ここは形だけで足ります。
		res, handled, err := comm.IntakeFile(inboxID, username, "mail.eml", raw)
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

// MailImportAPIHandler は POST /api/mail/import です。
// 入力（省略可）: {folder, max, since}
func MailImportAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Folder string `json:"folder"`
		Max    int    `json:"max"`
		Since  string `json:"since"`
	}
	// 本体が空でも既定で動きます（50件）。
	json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	sum, err := ImportMessages(ctx, user.Username, ListOptions{
		Folder: req.Folder, Max: req.Max, Since: req.Since,
	})
	if err != nil {
		if err == errNotSignedIn {
			cms.JSONFail(w, http.StatusConflict, "メールアカウントにサインインしていません")
			return
		}
		cms.JSONFail(w, http.StatusBadGateway, err.Error())
		return
	}
	auth.Audit(user.Username, "mail.import", fmt.Sprintf("listed=%d imported=%d duplicate=%d failed=%d",
		sum.Listed, sum.Imported, sum.Duplicate, sum.Failed))

	cms.WriteJSON(w, map[string]any{"success": true, "summary": sum})
}
