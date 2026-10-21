package boundary

import (
	"strings"
	"testing"
)

// stripJSComments は JS のコメントを空白に替えます（行の数は変えない）。文字列・正規表現の中の「//」「/*」は残す。
//
// ⚠ 正規表現で「/* … */」を先に消していた——行のコメントの中の「assets/*.js」「image/*」が囲みの始まりに見え、
// 次の本物の「*/」までの約320行が調べられずにいた（2026-10-22 の見直し）。
func stripJSComments(s string) string {
	b := []byte(s)
	out := make([]byte, 0, len(b))
	blank := func(c byte) byte {
		if c == '\n' {
			return c
		}
		return ' '
	}
	prev := byte(0) // 直前の空白でない字（正規表現が始まれる所かを見る）
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for ; i < len(b) && b[i] != '\n'; i++ {
				out = append(out, ' ')
			}
			if i < len(b) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			out = append(out, ' ', ' ')
			for i += 2; i < len(b) && !(b[i] == '*' && i+1 < len(b) && b[i+1] == '/'); i++ {
				out = append(out, blank(b[i]))
			}
			if i < len(b) {
				out = append(out, ' ', ' ')
				i++
			}
		case c == '\'' || c == '"' || c == '`':
			out = append(out, c)
			for i++; i < len(b) && b[i] != c; i++ {
				out = append(out, b[i])
				if b[i] == '\\' && i+1 < len(b) {
					i++
					out = append(out, b[i])
				}
			}
			if i < len(b) {
				out = append(out, b[i])
			}
			prev = c
		case c == '/' && (prev == 0 || strings.IndexByte("(,=:[!&|?{};+-*%<>~^", prev) >= 0):
			// 正規表現（/…/）——中の「//」「/*」をコメントと読まない。
			out = append(out, c)
			inClass := false
			for i++; i < len(b) && b[i] != '\n'; i++ {
				out = append(out, b[i])
				if b[i] == '\\' && i+1 < len(b) {
					i++
					out = append(out, b[i])
					continue
				}
				if b[i] == '[' {
					inClass = true
				} else if b[i] == ']' {
					inClass = false
				} else if b[i] == '/' && !inClass {
					break
				}
			}
			prev = '/'
		default:
			out = append(out, c)
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				prev = c
			}
		}
	}
	return string(out)
}

// 行のコメントの中の「/*」で、その後ろのコードを見落とさない。文字列・正規表現の中の「//」は残す。行の数は変えない。
func TestStripJSComments(t *testing.T) {
	src := "a(); // assets/*.js を読む\nconst x = '業務'; /* 本物の\nコメント */ b();\nconst u = 'https://x/y'; const r = /\\/\\//g;\n"
	got := stripJSComments(src)
	if !strings.Contains(got, "'業務'") {
		t.Errorf("⚠ 行のコメントの後ろのコードが消えました: %q", got)
	}
	if strings.Contains(got, "assets") || strings.Contains(got, "本物") {
		t.Errorf("⚠ コメントが残りました: %q", got)
	}
	if !strings.Contains(got, "'https://x/y'") || !strings.Contains(got, `/\/\//g`) {
		t.Errorf("⚠ 文字列か正規表現の中を消しました: %q", got)
	}
	if strings.Count(got, "\n") != strings.Count(src, "\n") {
		t.Errorf("⚠ 行の数が変わりました")
	}
}
