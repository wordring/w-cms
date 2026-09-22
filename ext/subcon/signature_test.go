package subcon

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

// TestSignatureLinesReadsTheSectionAsWritten は、⚠ **書いたとおりの行が、書いた順で
// 読めること**を固定します。
func TestSignatureLinesReadsTheSectionAsWritten(t *testing.T) {
	body := `<h1>南 康一</h1>` +
		`<section><h2>` + OrderSignatureHeading + `</h2>` +
		`<p>みらい産業</p><p>〒000-0000 ○○県○○市1-2-3</p>` +
		`<p>担当： 南 康一</p><p>TEL： 000-000-0000</p></section>`
	got := signatureLines(body, OrderSignatureHeading)
	want := []string{"みらい産業", "〒000-0000 ○○県○○市1-2-3", "担当： 南 康一", "TEL： 000-000-0000"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("署名が %v です（%v を期待）", got, want)
	}
	// ⚠ **見出しは紙に刷りません**——「発注書の署名」と印字された発注書が出ます。
	for _, ln := range got {
		if strings.Contains(ln, OrderSignatureHeading) {
			t.Errorf("⚠ 見出しが署名に混ざっています: %v", got)
		}
	}
}

// TestSignatureLinesSplitsOnBR は、⚠ **`<br>` も行の区切り**であることを固定します。
//
// ⚠ 署名を1つの段落に改行で書く人が居ます——エディタで Shift+Enter を押すと `<br>`
// になるので、**こちらのほうが普通の書き方**かもしれません。
func TestSignatureLinesSplitsOnBR(t *testing.T) {
	body := `<section><h2>` + OrderSignatureHeading + `</h2>` +
		`<p>みらい産業<br/>担当： 南 康一<br/>TEL： 000-000-0000</p></section>`
	got := signatureLines(body, OrderSignatureHeading)
	if len(got) != 3 {
		t.Fatalf("行が %d です（3のはず）: %v", len(got), got)
	}
	if got[1] != "担当： 南 康一" {
		t.Errorf("2行目が %q です: %v", got[1], got)
	}
}

// TestSignatureLinesPicksTheRightSection は、⚠ **別の節を拾わない**ことを固定します。
//
// ⚠ 同じページに「メールの署名」も置きます（ユーザーが並べて挙げた）。取り違えると、
// **発注書にメールの署名が刷られます**——どちらも署名なので、見ても気づきにくい。
func TestSignatureLinesPicksTheRightSection(t *testing.T) {
	body := `<section><h2>` + MailSignatureHeading + `</h2><p>メール用です</p></section>` +
		`<section><h2>` + OrderSignatureHeading + `</h2><p>発注書用です</p></section>`
	if got := signatureLines(body, OrderSignatureHeading); len(got) != 1 || got[0] != "発注書用です" {
		t.Errorf("発注書の署名が %v です", got)
	}
	if got := signatureLines(body, MailSignatureHeading); len(got) != 1 || got[0] != "メール用です" {
		t.Errorf("メールの署名が %v です", got)
	}
	// 無い見出しは空（「署名が無い」と「読めなかった」を同じに扱う——どちらも刷れない）。
	if got := signatureLines(body, "在りもしない見出し"); got != nil {
		t.Errorf("無い見出しで %v が返りました", got)
	}
}

// TestSignatureLinesIgnoresNestedHeadings は、⚠ **入れ子の節の見出しで当てない**ことを
// 固定します。
//
// ⚠ 当ててしまうと、**別の節の中身を署名として刷ります**（`sectionHeading` が直下だけを
// 見る理由）。
func TestSignatureLinesIgnoresNestedHeadings(t *testing.T) {
	body := `<section><h2>ただの節</h2><p>本文</p>` +
		`<section><h2>` + OrderSignatureHeading + `</h2><p>本物の署名</p></section></section>`
	got := signatureLines(body, OrderSignatureHeading)
	if len(got) != 1 || got[0] != "本物の署名" {
		t.Errorf("署名が %v です（本物の署名だけのはず）", got)
	}
}

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
	body := buildOurOrderHTML("000138", "みなと商店", "2026-09-22", "", "", "000012",
		[]ourOrderLine{{ProductID: "000080", Material: "鉄"}})
	if !strings.Contains(body, "<dt>"+OrderSignerTag+"</dt><dd>000012</dd>") {
		t.Errorf("⚠ 差出人が残っていません:\n%s", body)
	}
	// 選ばれていなければタグごと出さない（空の参照は薄赤になるだけ）。
	none := buildOurOrderHTML("000138", "みなと商店", "2026-09-22", "", "", "",
		[]ourOrderLine{{ProductID: "000080", Material: "鉄"}})
	if strings.Contains(none, OrderSignerTag) {
		t.Errorf("⚠ 空の差出人タグが書かれています:\n%s", none)
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
