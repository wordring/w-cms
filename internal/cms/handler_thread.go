package cms

// ─────────────────────────────────────────────────────────────────────────
// スレッドの前後へ移る（2026-09-14）
//
// ユーザー:「メールですが、In-reply-to をみて前後のメールにも移動できると便利です」。
//
// **新しい仕組みは1つも要りません。** 取り込みが `メッセージID` と
// `返信元メッセージID`（`In-Reply-To` ヘッダ）をタグに書いているので、鎖は
// 索引の逆引き2回で辿れます——`intake_eml.go` の `InReplyToTag` のコメントが
// 「親の記録ページは重複検知と同じ逆引き1回で引けます」と予告していたとおりです。
//
//	前（親）  = メッセージID が、このページの 返信元メッセージID と一致するページ
//	次（子）  = 返信元メッセージID が、このページの メッセージID と一致するページ
//
// **`/api/replies` とは別の鎖です。** あちらは w-cms **自身が送った**返信を
// `返信元`（値＝返信元のページID）で引きます——相手のメールソフトの都合に依らず
// 確実ですが、**w-cms を通っていない往復は見えません**。こちらはメールヘッダの鎖なので、
// **受信どうしの返り**（お客様が自分の前のメールに返信した、など）も繋がります。
// 送信の記録にも `返信元メッセージID` は書かれる（`ext/comm/mail/reply.go`）ので、
// 両方が同じ鎖に乗ります。
//
// **取り込みの順に依存しません。** 返信を先に落としても、あとで親が入れば繋がります
// ——どちらの向きも「いま索引にあるもの」を引くだけだからです。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"net/http"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ThreadRef はスレッド上の記録1件です（前にも次にも同じ形を使います）。
type ThreadRef struct {
	PageID    string `json:"page_id"`
	Title     string `json:"title"`
	When      string `json:"when"`      // 受信日時、無ければ送信日時（どちらも無ければ空）
	Direction string `json:"direction"` // `向き` タグ（受信／送信）。画面の矢印に使う
}

// threadRefOf は1件ぶんの見出しを組み立てます。読めない相手は ok=false です
// （見せ分けC案——黙って落ちる。欠けたことは知らせない）。
func threadRefOf(user *auth.User, idInt int) (ThreadRef, bool) {
	if !page.CanView(user, idInt) {
		return ThreadRef{}, false
	}
	r := ThreadRef{PageID: page.FormatID(idInt), Title: PageTitleByID(idInt)}
	// **受信と送信で欄の名前が違います**（向きに応じて片方だけ書かれる）。
	// 並べるのは時刻そのものではなく「いつの記録か」の手掛かりなので、どちらでも構いません。
	database.DB.QueryRow(
		`SELECT value FROM page_tags
		  WHERE page_id = ? AND name IN ('受信日時','送信日時','発信日時')
		  ORDER BY seq LIMIT 1`, idInt).Scan(&r.When)
	database.DB.QueryRow(
		`SELECT value FROM page_tags WHERE page_id = ? AND name = ? LIMIT 1`,
		idInt, DirectionTag).Scan(&r.Direction)
	return r, true
}

// tagOfPage はそのページの名前つきタグの生の値を1つ返します（無ければ空）。
func tagOfPage(idInt int, name string) string {
	var v string
	database.DB.QueryRow(
		`SELECT value FROM page_tags WHERE page_id = ? AND name = ? LIMIT 1`,
		idInt, name).Scan(&v)
	return v
}

// ThreadOf は pageID の前（親）と次（子）を返します。
//
// **前は高々1件**です（`In-Reply-To` は1つの親を指す）。Message-ID が重複していたら
// 最初の1件を採ります——実在しうる話ではありますが、そのときは鎖そのものが壊れており、
// ここで選び直しても直りません。
func ThreadOf(user *auth.User, idInt int) (prev *ThreadRef, next []ThreadRef, err error) {
	next = []ThreadRef{}

	if parentMsgID := tagOfPage(idInt, InReplyToTag); parentMsgID != "" {
		ids, e := PagesByTag(database.DB, MessageIDTag, parentMsgID)
		if e != nil {
			return nil, nil, e
		}
		for _, pid := range ids {
			if pid == idInt {
				continue // 自分自身は前にしない（壊れた鎖への保険）
			}
			if r, ok := threadRefOf(user, pid); ok {
				prev = &r
				break
			}
		}
	}

	if myMsgID := tagOfPage(idInt, MessageIDTag); myMsgID != "" {
		ids, e := PagesByTag(database.DB, InReplyToTag, myMsgID)
		if e != nil {
			return nil, nil, e
		}
		for _, pid := range ids {
			if pid == idInt {
				continue
			}
			if r, ok := threadRefOf(user, pid); ok {
				next = append(next, r)
			}
		}
	}
	return prev, next, nil
}

// ThreadAPIHandler は GET /api/thread?page_id=X です。
func ThreadAPIHandler(w http.ResponseWriter, r *http.Request) {
	// **メソッド確認もここに入りました**——写していたころは、この2つの口だけ
	// 抜けていました（GET専用のつもりで書いて、書き忘れに誰も気づかない形）。
	_, idInt, user, ok := GateJSONPageRead(w, r, r.URL.Query().Get("page_id"))
	if !ok {
		return
	}
	prev, next, err := ThreadOf(user, idInt)
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, "スレッドを引けません: "+err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "prev": prev, "next": next})
}
