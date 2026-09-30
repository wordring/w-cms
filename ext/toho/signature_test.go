package toho

import (
	"os"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// 発注書の差出人＝連絡帳の担当者ページの署名（2026-09-22）。
//
// ⚠ **署名は「項目」ではなく「文面」です**（ユーザー決定）。だから行の並びとして
// 読み、**書いたとおりの順で**紙に刷ります。

// TestDefaultSignerMatchesLoginName は、⚠ **ログイン名と同じ題の人を初期値にする**ことを
// 固定します（2026-09-22 ユーザーの案）。
//
// ⚠ **当たらないことは異常ではありません**——そのときは人が選びます。だから
// 「当たること」と「当たらないこと」の**両方**を見ます。
func TestDefaultSignerMatchesLoginName(t *testing.T) {
	list := []Signer{
		{PageID: 11, Title: "山田 花子", Lines: []string{"あ"}},
		{PageID: 12, Title: "南 康一", Lines: []string{"い"}},
	}
	if s, ok := DefaultSigner(&auth.User{Username: "南 康一"}, list); !ok || s.PageID != 12 {
		t.Errorf("ログイン名で引けていません: %#v ok=%v", s, ok)
	}
	// ⚠ **畳んで比べます**（全角・半角・前後の空白）。
	if s, ok := DefaultSigner(&auth.User{Username: " 南 康一 "}, list); !ok || s.PageID != 12 {
		t.Errorf("⚠ 前後の空白で引けなくなっています: %#v ok=%v", s, ok)
	}
	// 当たらないときは空（`a` のようなログイン名）。
	if _, ok := DefaultSigner(&auth.User{Username: "a"}, list); ok {
		t.Error("⚠ 当たらないはずのログイン名で当たっています")
	}
	// ⚠ **誰でもない利用者に誰かを当てないこと。**
	if _, ok := DefaultSigner(nil, list); ok {
		t.Error("⚠ 利用者が居ないのに差出人が決まりました")
	}
}

// TestBuildOurOrderWritesSigner は、⚠ **誰が出したかを発注書ページに残す**ことを
// 固定します（2026-09-22）。
//
// ⚠ **署名の文面は焼き込みません**——出した紙の正本はPDF（このページの添付）で、
// 本文へ写すと**あとから署名が変わったときに紙と食い違います**。
func TestBuildOurOrderWritesSigner(t *testing.T) {
	body := testOurOrder("000138", "みなと商店", "2026-09-22", "", "", "000012",
		[]ourOrderLine{{ProductID: "000080", Material: "鉄"}})
	if !strings.Contains(body, "<dt>"+OrderSignerTag+"</dt><dd>000012</dd>") {
		t.Errorf("⚠ 差出人が残っていません:\n%s", body)
	}
	// 選ばれていなければ値を書かない。⚠ **欄を出すかはテンプレートが決めます**（2026-09-27〜
	// テンプレート駆動）——それまでは「タグごと出さない」でしたが、いまはテンプレートの空欄の
	// まま残ります（空の参照は薄赤になり、選んでいないことが見える）。
	none := testOurOrder("000138", "みなと商店", "2026-09-22", "", "", "",
		[]ourOrderLine{{ProductID: "000080", Material: "鉄"}})
	if strings.Contains(none, "<dd>000012</dd>") || !strings.Contains(none, "<dt>"+OrderSignerTag+"</dt><dd><br/></dd>") {
		t.Errorf("⚠ 選んでいない差出人の欄がテンプレートのままではありません:\n%s", none)
	}
}

// TestSenderLinesPrefersTheSignature は、⚠ **担当者の署名が設定より先**であることを
// 固定します。
//
// ⚠ **後ろ盾（設定の `company`）を消さないこと**も一緒に見ます——連絡帳をまだ
// 作っていない環境で、差出人が丸ごと空の紙が出るのを避けるためです。
func TestSenderLinesPrefersTheSignature(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 12, 0, "南 康一", "root", "302", true)
	writeBodyFile(t, 12, `<h1>南 康一</h1>`+
		`<section><h2>`+OrderSignatureHeading+`</h2>`+
		`<p>みらい産業</p><p>担当： 南 康一</p></section>`)

	root := &auth.User{Username: "root", IsAdmin: true}
	got := senderLines(map[string]string{OrderSignerTag: "000012"}, root)
	if len(got) != 2 || got[0] != "みらい産業" {
		t.Errorf("署名が刷られていません: %v", got)
	}
	// 名指しが無ければ後ろ盾へ（設定が空の実データでは0行＝紙の右上が空になるだけ）。
	if got := senderLines(map[string]string{}, root); len(got) != 0 {
		t.Logf("後ろ盾から %d 行（設定に company があれば出ます）: %v", len(got), got)
	}
	// ⚠ **署名の無いページを名指しされても、黙って後ろ盾へ落ちること。**
	addPage(t, 13, 0, "署名の無い人", "root", "302", true)
	writeBodyFile(t, 13, `<h1>署名の無い人</h1><p>ただのページ</p>`)
	if got := senderLines(map[string]string{OrderSignerTag: "000013"}, root); len(got) != 0 {
		t.Errorf("⚠ 署名が無いのに何か刷っています: %v", got)
	}
}

// writeBodyFile は本文を**ファイルに**書きます（索引にも通します）。
//
// ⚠ **`syncBody` では足りません。** あちらは `cms.SyncIndex` を呼ぶだけで、
// **本文ファイルを書きません**——索引を読む集計はそれで足りますが、**署名は索引に
// 入らない**（「タグと表だけがDBに入る」）ので、`cms.ReadPageBody` がファイルを読みます。
// ⚠ **この違いで最初に空が返りました。** 本文をファイルから読む口を試験するときは、
// こちらを使うこと。
func writeBodyFile(t *testing.T, id int, body string) {
	t.Helper()
	pid := page.FormatID(id)
	if err := os.MkdirAll(page.GetPageDir(pid), 0755); err != nil {
		t.Fatalf("ページのフォルダを作れません: %v", err)
	}
	if err := page.WriteFileAtomic(page.BodyPath(pid), []byte(body), 0644); err != nil {
		t.Fatalf("本文を書けません: %v", err)
	}
	syncBody(t, id, body)
}

// TestSignersOnlyListsPeopleWithSignatures は、⚠ **署名を持つ人だけが候補になる**ことを
// 固定します。
//
// ⚠ **署名の在ることが、その人が差出人になれる印**です（印を別に持たない）。
// 署名の無い人を候補に出すと、選べてしまい、**紙の差出人が空のまま出ます**。
//
// ⚠ **「誰も出ないこと」だけを見ません**——それでは「そもそも連絡帳を読めていない」と
// 区別が付かないので、**同じ種まきで署名のある人が1件出ること**も一緒に見ます。
func TestSignersOnlyListsPeopleWithSignatures(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 3, 0, "連絡帳", "root", "302", true)
	addPage(t, 20, 3, "みらい産業", "root", "302", true)
	addPage(t, 21, 20, "南 康一", "root", "302", true)
	addPage(t, 22, 20, "山田 花子", "root", "302", true)
	writeBodyFile(t, 21, `<h1>南 康一</h1><section><h2>`+OrderSignatureHeading+
		`</h2><p>みらい産業</p><p>担当： 南 康一</p></section>`)
	writeBodyFile(t, 22, `<h1>山田 花子</h1><p>まだ署名を書いていません</p>`)

	root := &auth.User{Username: "root", IsAdmin: true}
	list := Signers(root)
	if len(list) != 1 {
		t.Fatalf("候補が %d 件です（署名のある1人だけのはず）: %#v", len(list), list)
	}
	if list[0].PageID != 21 || list[0].Title != "南 康一" {
		t.Errorf("候補が違います: %#v", list[0])
	}
	// ⚠ **組織の題も添えること**——同姓の人が別の会社に居ても見分けられるように。
	if list[0].Org != "みらい産業" {
		t.Errorf("組織が添えられていません: %#v", list[0])
	}
	// ⚠ **署名の中身も一緒に読めていること**（`<select>` に出すだけでなく紙に刷る）。
	if len(list[0].Lines) != 2 || list[0].Lines[0] != "みらい産業" {
		t.Errorf("署名が読めていません: %#v", list[0].Lines)
	}
}

// TestSignersHidesUnreadablePeople は、⚠ **読めない人は候補にしない**ことを固定します。
//
// ⚠ 署名には自社の住所と電話が入ります——読めない人のそれを `<select>` に並べると、
// **ページを開けない相手の情報が画面に出ます**。
func TestSignersHidesUnreadablePeople(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 3, 0, "連絡帳", "root", "302", true)
	addPage(t, 20, 3, "みらい産業", "root", "302", true)
	addPage(t, 21, 20, "南 康一", "alice", "300", false) // ⚠ alice 専有
	writeBodyFile(t, 21, `<h1>南 康一</h1><section><h2>`+OrderSignatureHeading+
		`</h2><p>みらい産業</p></section>`)

	// まず、引けていることを確かめる（静けさと成功を区別するため）。
	if list := Signers(&auth.User{Username: "alice"}); len(list) != 1 {
		t.Fatalf("前提が崩れています: 持ち主には1件出るはずです: %#v", list)
	}
	if list := Signers(&auth.User{Username: "mallory"}); len(list) != 0 {
		t.Fatalf("⚠ 読めない人が候補に出ています: %#v", list)
	}
}

// TestSenderLinesHidesUnreadableSignature は、⚠ **読めない人の署名を紙に刷らない**ことを
// 固定します。
//
// ⚠ **これが最後の砦です。** 口（`NewOurOrderAPIHandler`）も差出人を検めますが、
// **`発注担当` のタグは人が本文で書き換えられます**——write 権限のある人が任意の
// ページIDを書けば、口を通らずにここへ届きます。
func TestSenderLinesHidesUnreadableSignature(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 12, 0, "南 康一", "alice", "300", false) // ⚠ alice 専有
	writeBodyFile(t, 12, `<h1>南 康一</h1><section><h2>`+OrderSignatureHeading+
		`</h2><p>みらい産業</p><p>TEL： 000-000-0000</p></section>`)

	head := map[string]string{OrderSignerTag: "000012"}
	// まず、持ち主には出ることを確かめる（静けさと成功を区別するため）。
	if got := senderLines(head, &auth.User{Username: "alice"}); len(got) != 2 {
		t.Fatalf("前提が崩れています: 持ち主には署名が出るはずです: %v", got)
	}
	if got := senderLines(head, &auth.User{Username: "mallory"}); len(got) != 0 {
		t.Fatalf("⚠ 読めない人の署名が紙に出ています: %v", got)
	}
}
