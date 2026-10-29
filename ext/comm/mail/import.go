package mail

// ─────────────────────────────────────────────────────────────────────────
// メールを通信箱へ取り込む（2026-09-03）
//
// 取ってきた生のMIMEを**既存の取り込み係へそのまま渡します**（comm.IntakeFileAs）。
// 人が `.eml` をドロップしたときと同じ道なので、封筒タグ・スレッドの繋ぎ・
// 添付の展開・重複検知が丸ごと効きます。
//
// **重複は落とす前に弾きます。** 一覧では `Message-ID` の見出しだけ読み（IMAP の見出しの FETCH・receive.go。
// `internetMessageId` は 2026-09-05 に撤去した Graph の欄の名前でした）、索引の
// 逆引きで取り込み済みと分かれば本体を取りに行きません——過去メールの一括取り込みで
// 同じものを何度も落とさないための要点です（実測: 300通で105MB。無駄に落とすと
// 効きます）。
//
// **起動は人の指先だけ**——自動で回し続けません（§3 人間ゲート型）。実測した
// 流量は 1.2通/日なので、気づいたときに押せば十分追いつきます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
)

// ImportSummary は取り込み1回の結果です。
type ImportSummary struct {
	Listed    int      `json:"listed"`    // 一覧で見た件数
	Imported  int      `json:"imported"`  // 新しく取り込んだ件数
	Duplicate int      `json:"duplicate"` // 既に取り込み済みだった件数
	Failed    int      `json:"failed"`    // 取り込めなかった件数
	Titles    []string `json:"titles"`    // 取り込んだものの題（先頭のいくつか）
	// New は新しく取り込んだ記録（ページ番号と題）です（2026-10-21 夜・届いたことを知らせるため——fetch_job.go）。
	New []comm.Arrival `json:"-"`
}

// importMax は1回の取り込みの上限です。**押すたびに少しずつ**進める形にして、
// 1回の要求が長く居座らないようにします（過去分は繰り返し押せば追いつく）。
// （2026-10-09 から、画面の「📥 新しいメールを読み込む」は裏の仕事〔fetch_job.go〕がこの1回分を尽きるまで繰り返すので、
// 1回分ずつ押す口 `/api/mail/import` は、画面から呼ぶ所が無くなったので 2026-10-30 に外しました。）
const importMax = 50

// listAllMax は一覧で見る上限です。**取り込む上限とは別**——一覧は封筒の見出し
// だけなので安く、範囲を全部見てから「まだ入っていないもの」を選ぶほうが、
// 押し直したときに確実に前へ進みます。実測の流量は年435通なので、
// これで10年分を一度に見渡せます。
const listAllMax = 5000

// ImportMessages は未取り込みのメールを通信箱へ取り込みます。
func ImportMessages(ctx context.Context, username string, opt ListOptions) (ImportSummary, error) {
	var sum ImportSummary

	// 取り込んだ記録は通信箱の下に作る——読み込む人が通信箱に書けなければ、何も読まない（2026-10-26）。
	usr, err := auth.LookupUser(username)
	if err != nil || usr == nil {
		return sum, comm.ErrMailBoxNotWritable
	}
	inboxID, err := comm.WritableMailBox(usr)
	if err != nil {
		return sum, err
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
	sess, err := openImportBox(ctx, username, opt.Folder)
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
	// In-Reply-To（2026-10-09 までの名前は「返信元メッセージID」）の逆引きが最初から繋がります（2026-10-09 からは、取り込みが親の記録を引いて
	// 子に `親ページID` をその場で書けます。親があとから来たときは FixRecordTags が書き足します）。
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
			if _, dup := comm.ExistingMailRecord(id); dup { // 直す前の記録（古い名前）も見る
				sum.Duplicate++
				continue
			}
		}
		// 前に取り込めなかったメール（壊れた形など）は、サーバーを起こし直すまで落とし直さない（2026-10-27 の見直し——
		// 外の人が壊れた大きなメールを送ると、自動の読み込みのたびに最大 64 MiB を落とし直していた）。
		failKey := fmt.Sprintf("%s\x00%s\x00%v", username, opt.Folder, r.UID)
		if mailFailedBefore(failKey) {
			sum.Failed++
			continue
		}
		raw, err := sess.fetchRaw(r.UID)
		if err != nil {
			log.Printf("メールを取得できませんでした subject=%q: %v", r.Subject, err)
			sum.Failed++
			continue
		}
		// ファイル名は取り込み係が拡張子で担当を決めるためのもの。
		// 題は本文（件名）から作られるので、ここは形だけで足ります。
		// 向きは読んだ箱で決める（送信済みの箱なら送信・ほかは受信——差出人では決めない）。
		direction := comm.DirectionIn
		if opt.Folder == FolderSent {
			direction = comm.DirectionOut
		}
		res, handled, err := comm.IntakeFileAs(inboxID, username, "mail.eml", raw, direction)
		if err != nil || !handled {
			if err != nil {
				log.Printf("メールを取り込めませんでした subject=%q: %v", r.Subject, err)
			}
			markMailFailed(failKey)
			sum.Failed++
			continue
		}
		if res.Duplicate {
			sum.Duplicate++
			continue
		}
		sum.Imported++
		sum.New = append(sum.New, comm.Arrival{PageID: res.PageID, Title: res.Title, Channel: comm.ChannelMail})
		if len(sum.Titles) < 20 {
			sum.Titles = append(sum.Titles, res.Title)
		}
	}
	return sum, nil
}

// FolderSent は取り込む箱の「送信済み」です（ListOptions.Folder・2026-09-29）。Outlook から送ったメールは送信済みの
// 箱にある——取り込むと、差出人が自分なので「送信」の記録になる（comm の `.eml` の取り込み）。
const FolderSent = "送信"

// openImportBox は取り込む箱を読み取り専用で開きます（空・「受信」は受信箱、FolderSent は送信済みの箱——
// LIST の \Sent の印で探す）。
func openImportBox(ctx context.Context, username, folder string) (*imapSession, error) {
	switch folder {
	case "", "受信", "INBOX":
		return openIMAP(ctx, username)
	case FolderSent:
		s, err := connectIMAP(ctx, username)
		if err != nil {
			return nil, err
		}
		box, err := s.specialBox(SentBox)
		if err != nil {
			s.Close()
			return nil, err
		}
		if _, err := s.command("EXAMINE " + imapQuote(box)); err != nil {
			s.Close()
			return nil, errors.New("送信済みの箱を開けません: " + err.Error())
		}
		return s, nil
	}
	return nil, errors.New("箱「" + folder + "」は分かりません（受信・送信）")
}

// 取り込めなかったメール（利用者・箱・UID）の覚え（サーバーを起こし直すと消える）。多すぎたら空にする。
var (
	mailFailedMu sync.Mutex
	mailFailed   = map[string]bool{}
)

func mailFailedBefore(key string) bool {
	mailFailedMu.Lock()
	defer mailFailedMu.Unlock()
	return mailFailed[key]
}

func markMailFailed(key string) {
	mailFailedMu.Lock()
	defer mailFailedMu.Unlock()
	if len(mailFailed) > 10000 {
		mailFailed = map[string]bool{}
	}
	mailFailed[key] = true
}
