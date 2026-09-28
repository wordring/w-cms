package mail

import "testing"

// 送信済みの箱は LIST の印で探す——名前は言語設定で変わる（修正 UTF-7 の和名のこともある）ので、
// 引用符つきの名前はそのまま EXAMINE へ渡す。
func TestListReFindsSentBox(t *testing.T) {
	cases := []struct {
		line, flags, name string
	}{
		{`* LIST (\HasNoChildren \Sent) "/" "Sent Items"`, `\HasNoChildren \Sent`, `"Sent Items"`},
		{`* LIST (\HasNoChildren \Sent) "/" "&kAFP4W4IMH8wojCkMMYw4A-"`, `\HasNoChildren \Sent`, `"&kAFP4W4IMH8wojCkMMYw4A-"`},
		{`* LIST (\Marked \HasChildren) "/" INBOX`, `\Marked \HasChildren`, `INBOX`},
		{`* LIST (\Noselect) NIL ""`, `\Noselect`, `""`},
	}
	for _, c := range cases {
		m := listRe.FindStringSubmatch(c.line)
		if m == nil {
			t.Fatalf("読めません: %s", c.line)
		}
		if m[1] != c.flags || m[2] != c.name {
			t.Errorf("%s → 印 %q・名前 %q（期待 %q・%q）", c.line, m[1], m[2], c.flags, c.name)
		}
	}
}

func TestImapQuote(t *testing.T) {
	for in, want := range map[string]string{
		"INBOX":        `"INBOX"`,
		`"Sent Items"`: `"Sent Items"`, // 既に引用符つき（LIST の答え）はそのまま
		`a"b\c`:        `"a\"b\\c"`,
	} {
		if got := imapQuote(in); got != want {
			t.Errorf("imapQuote(%q) = %q（期待 %q）", in, got, want)
		}
	}
}

func TestUIDValidityRe(t *testing.T) {
	m := uidValidityRe.FindStringSubmatch(`* OK [UIDVALIDITY 14] UIDs valid`)
	if m == nil || m[1] != "14" {
		t.Fatalf("UIDVALIDITY を読めません: %v", m)
	}
}
