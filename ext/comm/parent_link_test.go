package comm

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// 親ページID——通信記録の親子（parent_link.go・2026-10-09）の番人です。
// 利用者:「メール関連のタグはメールヘッダそのまま In-Reply-To などとして記録し、それとは別に親子関係を表すタグとして
// 『親ページID』タグを付けるのはどうでしょう？もちろん、通信記録の前後をたどるには、基本的に『親ページID』を検索します」。

func readRecord(t *testing.T, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// replyEml は In-Reply-To つきの最小のメールです。
func replyEml(msgID, inReplyTo, subject string) []byte {
	return []byte("From: sender@example.jp\r\nTo: order@example.co.jp\r\nSubject: " + subject + "\r\n" +
		"Date: Mon, 01 Sep 2026 11:00:00 +0900\r\nMessage-ID: " + msgID + "\r\nIn-Reply-To: " + inReplyTo + "\r\n\r\nhonbun\r\n")
}

// TestChildBeforeParentGetsLinked は、子（相手の返信）が親（こちらが送ったメール）より先に取り込まれても、親が入ったあとの
// FixRecordTags で子に 親ページID が書き足されること・編集中の子は飛ばして次の回で書き足すこと・2回目は何もしないことを
// 固定します（受信の箱を先に読むので、実際に起きる順）。
func TestChildBeforeParentGetsLinked(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)
	ctx := &IntakeContext{InboxID: inbox, Uploader: "alice"}
	child, _, err := emlIntake{}.OnFile(ctx, "c.eml", replyEml("<child@example.jp>", "<parent@example.jp>", "Re: 元"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readRecord(t, child), ParentPageTag) {
		t.Fatalf("親がまだ無いのに 親ページID を書いています")
	}
	parent, _, err := emlIntake{}.OnFile(ctx, "p.eml", []byte(buildEml("<parent@example.jp>", "元")))
	if err != nil {
		t.Fatal(err)
	}
	// 子を誰かが開いている——飛ばす。
	n, _ := strconv.Atoi(child)
	if r := editlock.Locks.TryAcquire(n, "carol", ""); !r.Acquired {
		t.Fatal("ロックを取れません")
	}
	if _, linked, err := FixRecordTags("alice"); err != nil || linked != 0 {
		t.Errorf("編集中の子に書き足しています: %d %v", linked, err)
	}
	editlock.Locks.ForceRelease(n)
	if _, linked, err := FixRecordTags("alice"); err != nil || linked != 1 {
		t.Fatalf("親があとから入った子に書き足しません: %d %v", linked, err)
	}
	if !strings.Contains(readRecord(t, child), "<dt>"+ParentPageTag+"</dt><dd>"+parent+"</dd>") {
		t.Errorf("子の 親ページID が親を指していません:\n%s", readRecord(t, child))
	}
	if renamed, linked, _ := FixRecordTags("alice"); renamed+linked != 0 {
		t.Errorf("2回目も直しています: %d %d", renamed, linked)
	}
	// 前後は 親ページID でたどる。
	pInt, _ := strconv.Atoi(parent)
	cInt, _ := strconv.Atoi(child)
	u := &auth.User{Username: "alice"}
	if prev, _, err := ThreadOf(u, cInt); err != nil || prev == nil || prev.PageID != parent {
		t.Errorf("子の前が親ではありません: %+v %v", prev, err)
	}
	if _, next, err := ThreadOf(u, pInt); err != nil || len(next) != 1 || next[0].PageID != child {
		t.Errorf("親の次が子ではありません: %+v %v", next, err)
	}
	// 「この記録への返信」はこちらが送ったものだけ——受信の子は出ない。
	if rs, err := RepliesTo(u, parent); err != nil || len(rs) != 0 {
		t.Errorf("受信の子を「この記録への返信」に出しています: %+v %v", rs, err)
	}
}

// TestFixRecordTagsRenamesLegacy は、取り込み済みの記録の古い名前（メッセージID・返信元メッセージID・返信元）を新しい名前
// （Message-ID・In-Reply-To・親ページID）へ直し、直したあとに子へ親を書き足すこと・直す前の記録も重複と見分けることを
// 固定します。
func TestFixRecordTagsRenamesLegacy(t *testing.T) {
	setupSaveTest(t)
	inbox := setupInbox(t)
	old := func(tags string) string {
		id, err := cms.CreateChildPage(inbox, "alice", `<h1>古い記録</h1><dl data-type="tags">`+tags+`</dl><p>本文</p>`)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	parent := old(`<dt>向き</dt><dd>送信</dd><dt>メッセージID</dt><dd>&lt;old-p@example.jp&gt;</dd>`)
	child := old(`<dt>向き</dt><dd>受信</dd><dt>メッセージID</dt><dd>&lt;old-c@example.jp&gt;</dd><dt>返信元メッセージID</dt><dd>&lt;old-p@example.jp&gt;</dd>`)
	// エディタで保存した形（見出しの中に空白・字下げ）も直す。
	ours := old("\n  <dt> 返信元 </dt>\n  <dd>" + parent + "</dd>\n  <dt>向き</dt><dd>送信</dd>")
	// 直す前でも、古い名前の記録を重複と見分ける。
	if id, dup := ExistingMailRecord("<old-c@example.jp>"); !dup || id != child {
		t.Errorf("直す前の記録を重複と見分けません: %q %v", id, dup)
	}
	renamed, linked, err := FixRecordTags("alice")
	if err != nil || renamed != 3 || linked != 1 {
		t.Fatalf("直した数が違います: 名前 %d・親 %d（3 と 1 のはず） %v", renamed, linked, err)
	}
	cb := readRecord(t, child)
	for _, want := range []string{"<dt>Message-ID</dt>", "<dt>In-Reply-To</dt><dd>&lt;old-p@example.jp&gt;</dd>",
		"<dt>" + ParentPageTag + "</dt><dd>" + parent + "</dd>"} {
		if !strings.Contains(cb, want) {
			t.Errorf("子に %q がありません:\n%s", want, cb)
		}
	}
	if strings.Contains(cb, "メッセージID") {
		t.Errorf("古い名前が残っています:\n%s", cb)
	}
	if ob := readRecord(t, ours); !strings.Contains(ob, "<dt> "+ParentPageTag+" </dt>") || strings.Contains(ob, "返信元") {
		t.Errorf("返信元が 親ページID になっていません:\n%s", ob)
	}
	// 索引も新しい名前で引ける（RewriteBody が索引を直す）。
	if id, dup := ExistingMailRecord("<old-p@example.jp>"); !dup || id != parent {
		t.Errorf("直したあとの記録を引けません: %q %v", id, dup)
	}
}
