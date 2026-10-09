package contacts

// 署名の読み方（2026-09-22 に東邦の拡張で書き、2026-09-30 にここへ移した試験）。
//
// ⚠ **署名は「項目」ではなく「文面」です**（利用者決定）。だから行の並びとして読み、
// **書いたとおりの順で**刷り・書きます。

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// orderSig は試験で使う2つ目の見出しです（発注書の署名——東邦の言葉なのでここでは文字で持つ）。
const orderSig = "発注書の署名"

// TestSignatureLinesReadsTheSectionAsWritten は、⚠ **書いたとおりの行が、書いた順で読めること**を固定します。
func TestSignatureLinesReadsTheSectionAsWritten(t *testing.T) {
	body := `<h1>山田 太郎</h1>` +
		`<section><h2>` + orderSig + `</h2>` +
		`<p>みらい産業</p><p>〒000-0000 ○○県○○市1-2-3</p>` +
		`<p>担当： 山田 太郎</p><p>TEL： 000-000-0000</p></section>`
	got := SignatureLines(body, orderSig)
	want := []string{"みらい産業", "〒000-0000 ○○県○○市1-2-3", "担当： 山田 太郎", "TEL： 000-000-0000"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("署名が %v です（%v を期待）", got, want)
	}
	// ⚠ **見出しは書きません**——「発注書の署名」と印字された紙が出ます。
	for _, ln := range got {
		if strings.Contains(ln, orderSig) {
			t.Errorf("⚠ 見出しが署名に混ざっています: %v", got)
		}
	}
}

// TestSignatureLinesSplitsOnBR は、⚠ **`<br>` も行の区切り**であることを固定します
// （エディタで Shift+Enter を押すと `<br>` になるので、こちらのほうが普通の書き方かもしれません）。
func TestSignatureLinesSplitsOnBR(t *testing.T) {
	body := `<section><h2>` + orderSig + `</h2>` +
		`<p>みらい産業<br/>担当： 山田 太郎<br/>TEL： 000-000-0000</p></section>`
	got := SignatureLines(body, orderSig)
	if len(got) != 3 {
		t.Fatalf("行が %d です（3のはず）: %v", len(got), got)
	}
	if got[1] != "担当： 山田 太郎" {
		t.Errorf("2行目が %q です: %v", got[1], got)
	}
}

// TestSignatureLinesPicksTheRightSection は、⚠ **別の節を拾わない**ことを固定します
// （取り違えると、発注書にメールの署名が刷られます——どちらも署名なので、見ても気づきにくい）。
func TestSignatureLinesPicksTheRightSection(t *testing.T) {
	body := `<section><h2>` + MailSignatureHeading + `</h2><p>メール用です</p></section>` +
		`<section><h2>` + orderSig + `</h2><p>発注書用です</p></section>`
	if got := SignatureLines(body, orderSig); len(got) != 1 || got[0] != "発注書用です" {
		t.Errorf("発注書の署名が %v です", got)
	}
	if got := SignatureLines(body, MailSignatureHeading); len(got) != 1 || got[0] != "メール用です" {
		t.Errorf("メールの署名が %v です", got)
	}
	if got := SignatureLines(body, "在りもしない見出し"); got != nil {
		t.Errorf("無い見出しで %v が返りました", got)
	}
}

// TestSignatureLinesIgnoresNestedHeadings は、⚠ **入れ子の節の見出しで当てない**ことを固定します。
func TestSignatureLinesIgnoresNestedHeadings(t *testing.T) {
	body := `<section><h2>ただの節</h2><p>本文</p>` +
		`<section><h2>` + orderSig + `</h2><p>本物の署名</p></section></section>`
	got := SignatureLines(body, orderSig)
	if len(got) != 1 || got[0] != "本物の署名" {
		t.Errorf("署名が %v です（本物の署名だけのはず）", got)
	}
}

// TestSignatureLinesReadsPlainHeadings は、⚠ **ふつうの見出し（節で包まない `<h2>`）の下も読む**ことを固定します
// （2026-10-02——利用者がエディタの「見出し2」で書いたら効かなかった。下の本文はそのとき保存された形と同じ作り）。
func TestSignatureLinesReadsPlainHeadings(t *testing.T) {
	body := `<h1 data-id="z1ql">山田 太郎</h1>` +
		`<dl data-id="nj5j" data-type="tags"><dt>メールアドレス</dt><dd>yamada@example.com</dd></dl>` +
		`<p data-id="3y10"><br/></p>` +
		`<h2 data-id="3l6c"><strong>` + orderSig + `</strong></h2>` +
		`<p data-id="r8xu">みらい産業</p><p>担当： 山田 太郎</p>` +
		`<p data-id="jgob"></p>` +
		`<h2 data-id="ukio">` + MailSignatureHeading + `</h2>` +
		`<p data-id="b0vs">メール用です</p>`
	if got := SignatureLines(body, orderSig); strings.Join(got, "|") != "みらい産業|担当： 山田 太郎" {
		t.Errorf("発注書の署名が %v です（次の見出しの手前まで・見出しの太字は外して当てる）", got)
	}
	if got := SignatureLines(body, MailSignatureHeading); strings.Join(got, "|") != "メール用です" {
		t.Errorf("メールの署名が %v です", got)
	}
}

// TestSignatureLinesPlainHeadingStops は、⚠ **ふつうの見出しの中身がどこで終わるか**を固定します——同じか上の段の
// 見出し・節の手前で止まり、下の段の見出しは書かない（署名に別の話が混ざると、紙にそのまま刷られます）。
func TestSignatureLinesPlainHeadingStops(t *testing.T) {
	body := `<h2>` + orderSig + `</h2><p>一行目</p><h3>小見出し</h3><p>二行目</p>` +
		`<section data-type="file-view" data-ref="000001-abcd">図面.pdf</section><p>節の後ろ</p>`
	if got := SignatureLines(body, orderSig); strings.Join(got, "|") != "一行目|二行目" {
		t.Errorf("署名が %v です（一行目|二行目 のはず——節の手前で止まる・下の段の見出しは書かない）", got)
	}
	body = `<h3>` + orderSig + `</h3><p>一行目</p><h2>別の話</h2><p>別の行</p>`
	if got := SignatureLines(body, orderSig); strings.Join(got, "|") != "一行目" {
		t.Errorf("署名が %v です（上の段の見出しで止まるはず）", got)
	}
	// 節の見出しは節として読む（後ろの兄弟まで読まない）。
	body = `<section><h2>` + orderSig + `</h2><p>節の中</p></section><p>節の外</p>`
	if got := SignatureLines(body, orderSig); strings.Join(got, "|") != "節の中" {
		t.Errorf("署名が %v です（節の中だけのはず）", got)
	}
}

// TestMySignatureFindsTheLoginNamePage は、⚠ **ログイン名と同じ題の人のページのメールの署名**を
// 返すこと・当たらなければ空であることを固定します（2026-09-30・送る欄の初期値）。
func TestMySignatureFindsTheLoginNamePage(t *testing.T) {
	user, companyID := setupPartnerTree(t)
	other, err := EnsureContactPerson(user, companyID, "山田 太郎")
	if err != nil {
		t.Fatal(err)
	}
	mine, err := EnsureContactPerson(user, companyID, "ａｌｉｃｅ") // 全角——畳んで比べる
	if err != nil {
		t.Fatal(err)
	}
	add := func(id, text string) {
		t.Helper()
		if err := cms.RewriteBody(id, "alice", func(cur string) string {
			return cur + `<section><h2>` + MailSignatureHeading + `</h2><p>` + text + `</p></section>`
		}); err != nil {
			t.Fatal(err)
		}
	}
	add(other, "他人の署名")
	add(mine, "みらい産業<br/>alice")

	got := MySignature(user, MailSignatureHeading)
	if strings.Join(got, "|") != "みらい産業|alice" {
		t.Errorf("自分の署名が %v です", got)
	}
	// 当たらない人は空（異常ではない——署名なしで出して、人が書き足す）。
	if got := MySignature(&auth.User{Username: "bob", IsAdmin: true}, MailSignatureHeading); got != nil {
		t.Errorf("当たらない人で %v が返りました", got)
	}
}

// TestAppendLines は、署名の行を拾うとき前後の空白を落とし、空行を足さないことを固定します（2026-10-09 に同じファイルの
// 2つの読み方の写しを寄せた口の番人）。
func TestAppendLines(t *testing.T) {
	got := appendLines([]string{"前"}, "  みらい産業 \n\n\t担当： 南 \n ")
	if strings.Join(got, "|") != "前|みらい産業|担当： 南" {
		t.Errorf("appendLines = %q", got)
	}
}
