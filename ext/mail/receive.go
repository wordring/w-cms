package mail

// ─────────────────────────────────────────────────────────────────────────
// 受信——IMAP から生のMIMEを取ってくる（2026-09-05）
//
// **取ってくるのは生のMIME**です。そのまま既存の取り込み係へ渡せば、人が `.eml` を
// ドロップしたときと**同じ道**を通ります——封筒タグ・スレッドの繋ぎ・添付の展開・
// 重複検知は全部そこにあるので、経路ごとに書き直しません（内部で組み立て直すと
// 必ず片方が古くなる）。
//
// **重複は取ってくる前に弾きます。** 一覧では `Message-ID` の見出しだけ読み、索引の
// 逆引きで既に取り込み済みと分かれば本体を落としません——過去メールの一括取り込みで
// 同じものを何度も落とさないための要点です。IMAP でもここは変わりません
// （見出しの FETCH は本体より桁違いに安い）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"mime"
	netmail "net/mail" // このパッケージ自身が mail なので別名にする
	"strings"
)

// MessageRef は一覧で読む1件です（本体はまだ落としていません）。
type MessageRef struct {
	UID       string // IMAP の UID（本体を落とすときの鍵）
	MessageID string // Message-ID ヘッダ（重複判定の鍵）
	Subject   string // 記録と画面のため（RFC 2047 は復号済み）
}

// ListOptions は一覧の条件です。
type ListOptions struct {
	Folder string // 予約（いまは受信箱だけ）。移設後にフォルダ指定が要れば使う
	Max    int    // 取る最大件数（0 なら 50）
	Since  string // ISO 8601。これ以降に届いたものだけ（空なら全期間）
}

// headerFetchChunk は見出しをまとめて取る単位です。UID を全部1行に並べると
// コマンドが長くなりすぎるので刻みます。
const headerFetchChunk = 200

// list は封筒の見出しを**新しい順**に返します（本体は落としません）。
//
// 新しい順にするのは、取り込み側が「新しいものから見て、古いものへ向かって
// 取り込む」という既存の段取りに乗っているためです（import.go）。
func (s *imapSession) list(opt ListOptions) ([]MessageRef, error) {
	uids, err := s.searchUIDs(opt.Since)
	if err != nil {
		return nil, err
	}
	max := opt.Max
	if max <= 0 {
		max = 50
	}
	// SEARCH は昇順（古い順）なので、新しい側から必要なぶんだけ見出しを取ります。
	if len(uids) > max {
		uids = uids[len(uids)-max:]
	}

	byUID := map[string]*MessageRef{}
	for i := 0; i < len(uids); i += headerFetchChunk {
		end := i + headerFetchChunk
		if end > len(uids) {
			end = len(uids)
		}
		set := strings.Join(uids[i:end], ",")
		cmd := "UID FETCH " + set + " (UID BODY.PEEK[HEADER.FIELDS (MESSAGE-ID SUBJECT)])"
		err := s.fetch(cmd, func(attrs string, body []byte) {
			uid := uidOf(attrs)
			if uid == "" {
				return
			}
			id, subject := headerFields(body)
			byUID[uid] = &MessageRef{UID: uid, MessageID: id, Subject: subject}
		})
		if err != nil {
			return nil, err
		}
	}

	// UID の並び（昇順）を保ったまま、新しい順へ反転します。
	out := make([]MessageRef, 0, len(uids))
	for i := len(uids) - 1; i >= 0; i-- {
		if r, ok := byUID[uids[i]]; ok {
			out = append(out, *r)
		}
	}
	return out, nil
}

// fetchRaw は1件の生のMIME（`.eml` と同じ中身）を落とします。
func (s *imapSession) fetchRaw(uid string) ([]byte, error) {
	var raw []byte
	err := s.fetch("UID FETCH "+uid+" (BODY.PEEK[])", func(_ string, body []byte) {
		raw = body
	})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, errEmptyMessage
	}
	return raw, nil
}

// errEmptyMessage は本体が空で返ったときの印です（消された直後など）。
var errEmptyMessage = errEmpty{}

type errEmpty struct{}

func (errEmpty) Error() string {
	return "メールの本体が空でした（消された直後かもしれません）"
}

// uidOf は FETCH の属性行から UID を取り出します（`* 12 FETCH (UID 345 …`）。
func uidOf(attrs string) string {
	i := strings.Index(attrs, "UID ")
	if i < 0 {
		return ""
	}
	rest := attrs[i+len("UID "):]
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

// headerFields は Message-ID と件名を読みます。
//
// 件名は RFC 2047 の符号化語で来る（和文は必ずそう）ので復号します。**復号に
// 失敗しても素の文字列を返します**——記録と画面のためであって、判定には使わない
// ためです（判定に使うのは Message-ID だけ）。
func headerFields(header []byte) (messageID, subject string) {
	msg, err := netmail.ReadMessage(strings.NewReader(string(header) + "\r\n"))
	if err != nil {
		return "", ""
	}
	messageID = strings.TrimSpace(msg.Header.Get("Message-Id"))
	subject = msg.Header.Get("Subject")
	var dec mime.WordDecoder
	if s, err := dec.DecodeHeader(subject); err == nil {
		subject = s
	}
	return messageID, subject
}
