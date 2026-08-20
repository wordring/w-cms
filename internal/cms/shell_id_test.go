package cms

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"w-cms/internal/cms/htmldoc"
)

// idAttrRe は殻のHTMLから id 属性の値を抜き出します。
var idAttrRe = regexp.MustCompile(`\bid="([^"]*)"`)

// TestShellIDsAreNamespaced は**殻の id が接頭辞つきであること**を固定します。
//
// 本文は殻（assets/index.html）と同じDOMへ合成されるため、id の名前空間は共有されます。
// getElementById は文書順で最初の要素を返すので、本文に殻と同じ id を書かれると
// 権限UIの入力欄などを乗っ取れます（本文の挿入点 #w-editor-content より後ろに
// pp-mode・pp-owner-input 等が並ぶ）。
//
// そこで**殻の側が接頭辞を独占し、本文の id は自由**という分担にしました
// （2026-08-20 決定）。この分担が成り立つのは殻の全 id が接頭辞つきである間だけなので、
// 後戻り——接頭辞なしの id を殻へ足すこと——をここで検出します
// （CSP の後戻りを internal/auth/csp_test.go が検出しているのと同じ流儀）。
func TestShellIDsAreNamespaced(t *testing.T) {
	b, err := os.ReadFile("../../assets/index.html")
	if err != nil {
		t.Fatalf("殻を読めません: %v", err)
	}
	var bad []string
	for _, m := range idAttrRe.FindAllStringSubmatch(string(b), -1) {
		if !strings.HasPrefix(m[1], htmldoc.ShellIDPrefix) {
			bad = append(bad, m[1])
		}
	}
	if len(bad) > 0 {
		t.Errorf("接頭辞 %q の無い id が殻にあります（本文から乗っ取られます）: %v",
			htmldoc.ShellIDPrefix, bad)
	}
}
