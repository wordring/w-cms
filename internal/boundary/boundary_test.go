// Package boundary は、コアと拡張の境（docs/開発方針.md §0）を機械で守る番人の試験だけを持ちます（2026-10-20）。
//
// 2026-10-20 利用者:「コアと拡張を分離することを守らせるために設定に何か書いておくことはできますか？」
// 「両方お願いします。開発方針にも書いたほうが良いと思いうます」（番人の試験と Claude Code の hook の両方）。
//
// 見ること（決まりの表は rules.json）:
//   - コアの Go（internal/）が拡張（w-cms/ext/...）を import しない。
//   - コアの Go のコード（コメントを除く・文字列と名前）に、業務の言葉が無い。
//   - コアの画面（assets/ の JS・CSS・HTML。コメントを除く）に、業務の言葉が無い。
//   - コアの画面が、拡張だけが作る class を名指ししない（app.css の規則・app.js の選び方）。
//     「拡張だけが作る」は、拡張の Go が描く class="…" にあって、コアの Go と assets/ の HTML に無いもの。
//
// 業務の言葉がコアに要るように見えたら、口（登録の仕組み）をコアに作り、言葉は拡張から渡します（開発方針 §0）。
// どうしても要る例外は rules.json の allow に、理由つきで書きます。
package boundary

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type rules struct {
	Words []string `json:"words"`
	Allow []struct {
		File     string `json:"file"`
		Word     string `json:"word"`
		Contains string `json:"contains"` // その行・文字列に含まれる文字（空なら、そのファイルのその言葉は全部）
		Why      string `json:"why"`
	} `json:"allow"`
}

// root はリポジトリの根です（この試験は internal/boundary で走る）。BOUNDARY_ROOT で替えられます
// （番人が効くかを、写しをわざと壊して確かめるため——ディスクの本物の assets は実運用の画面に配られている）。
var root = func() string {
	if r := os.Getenv("BOUNDARY_ROOT"); r != "" {
		return r
	}
	return "../.."
}()

func loadRules(t *testing.T) rules {
	t.Helper()
	b, err := os.ReadFile("rules.json")
	if err != nil {
		t.Fatal(err)
	}
	var r rules
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("rules.json が読めません: %v", err)
	}
	return r
}

func (r rules) allowed(file, word, text string) bool {
	for _, a := range r.Allow {
		if filepath.ToSlash(a.File) == file && a.Word == word && strings.TrimSpace(a.Why) != "" && strings.Contains(text, a.Contains) {
			return true
		}
	}
	return false
}

// walkFiles は根からの相対の道（/ 区切り）で、dir の下の、ext に合うファイルを返します。
func walkFiles(t *testing.T, dir string, ok func(rel string) bool) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if n := info.Name(); n == "node_modules" || n == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if ok(rel) {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func isCoreGo(rel string) bool {
	return strings.HasPrefix(rel, "internal/") && strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go")
}

// TestCoreGoDoesNotImportExtensions は、コアの Go が拡張を import しないことを固定します（向きは「拡張 → コア」の1つ）。
func TestCoreGoDoesNotImportExtensions(t *testing.T) {
	for _, rel := range walkFiles(t, "internal", isCoreGo) {
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, rel), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		for _, im := range f.Imports {
			if strings.HasPrefix(strings.Trim(im.Path.Value, `"`), "w-cms/ext") {
				t.Errorf("⚠ コアの %s が拡張 %s を import しています（コアは拡張を知らない——口をコアに作り、拡張から登録する）", rel, im.Path.Value)
			}
		}
	}
}

// TestCoreGoHasNoBusinessWords は、コアの Go のコード（文字列と名前・コメントは除く）に業務の言葉が無いことを固定します。
func TestCoreGoHasNoBusinessWords(t *testing.T) {
	r := loadRules(t)
	for _, rel := range walkFiles(t, "internal", isCoreGo) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			var s string
			switch x := n.(type) {
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					s = x.Value
				}
			case *ast.Ident:
				s = x.Name
			}
			for _, w := range r.Words {
				if s != "" && strings.Contains(s, w) && !r.allowed(rel, w, s) {
					t.Errorf("⚠ %s:%d に業務の言葉「%s」（%s）——業務の言葉は拡張から渡す（開発方針 §0）", rel, fset.Position(n.Pos()).Line, w, s)
				}
			}
			return true
		})
	}
}

func isCoreScreen(rel string) bool {
	if !strings.HasPrefix(rel, "assets/") || strings.Count(rel, "/") != 1 {
		return false // assets/ の直下だけ（拡張の画面は ext/<拡張>/assets/）
	}
	switch filepath.Ext(rel) {
	case ".js", ".css", ".html":
		return true
	}
	return false
}

var (
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	htmlComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	// 行のコメント（// …）——文字列の中の「://」（URL）は落とさない。
	lineComment = regexp.MustCompile(`(?m)(^|[^:'"\x60\\])//.*$`)
)

func stripComments(rel, s string) string {
	switch filepath.Ext(rel) {
	case ".html":
		return htmlComment.ReplaceAllString(s, "")
	case ".css":
		return blockComment.ReplaceAllString(s, "")
	}
	return lineComment.ReplaceAllString(blockComment.ReplaceAllString(s, ""), "$1")
}

// TestCoreScreenHasNoBusinessWords は、コアの画面（assets/ の JS・CSS・HTML。コメントは除く）に業務の言葉が無いことを固定します。
func TestCoreScreenHasNoBusinessWords(t *testing.T) {
	r := loadRules(t)
	for _, rel := range walkFiles(t, "assets", isCoreScreen) {
		for i, line := range strings.Split(stripComments(rel, read(t, rel)), "\n") {
			for _, w := range r.Words {
				if strings.Contains(line, w) && !r.allowed(rel, w, line) {
					t.Errorf("⚠ %s:%d に業務の言葉「%s」: %s", rel, i+1, w, strings.TrimSpace(clip(line, 120)))
				}
			}
		}
	}
}

func clip(s string, n int) string {
	if rs := []rune(s); len(rs) > n {
		return string(rs[:n]) + "…"
	}
	return s
}

// classAttr は Go の文字列の中の class="…"（サーバーが描く印）です。
var classAttr = regexp.MustCompile(`class=\\?"([^"\\]+)\\?"`)

// extensionOnlyClasses は、拡張の Go が描く class のうち、コアの Go と assets/ の HTML には出てこないものです。
func extensionOnlyClasses(t *testing.T) map[string]bool {
	t.Helper()
	ext := map[string]bool{}
	for _, rel := range walkFiles(t, "ext", func(rel string) bool { return strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") }) {
		for _, m := range classAttr.FindAllStringSubmatch(read(t, rel), -1) {
			for _, c := range strings.Fields(m[1]) {
				ext[c] = true
			}
		}
	}
	core := ""
	for _, rel := range walkFiles(t, "internal", isCoreGo) {
		core += read(t, rel)
	}
	// コアの JS が自分で付ける class（'cell-wrap' のように「.」の付かない形で出るもの）も、コアのもの。
	coreJS := ""
	for _, rel := range walkFiles(t, "assets", func(rel string) bool { return isCoreScreen(rel) && strings.HasSuffix(rel, ".js") }) {
		coreJS += stripComments(rel, read(t, rel))
	}
	for _, rel := range walkFiles(t, "assets", func(rel string) bool { return isCoreScreen(rel) && strings.HasSuffix(rel, ".html") }) {
		core += read(t, rel)
	}
	coreWords := map[string]bool{}
	for _, w := range word.FindAllString(core, -1) {
		coreWords[w] = true
	}
	for _, m := range bareWord.FindAllStringSubmatch(coreJS, -1) {
		coreWords[m[2]] = true // 「.」の付かない形——コアの JS が付ける
	}
	out := map[string]bool{}
	for c := range ext {
		if strings.HasPrefix(c, "w-") {
			continue // w- はコアの印（殻と、拡張が付けるコアの一般の印——w-no-print など）
		}
		if !coreWords[c] {
			out[c] = true
		}
	}
	return out
}

var (
	word     = regexp.MustCompile(`[\w-]+`)
	bareWord = regexp.MustCompile(`(^|[^\w.-])([\w-]+)`)
)

// selectorClass は CSS の選び方・JS の選び方の文字列の中の .class です。
var selectorClass = regexp.MustCompile(`\.([A-Za-z_][\w-]*)`)

// TestCoreScreenDoesNotNameExtensionClasses は、コアの app.css・app.js が、拡張だけが作る class を名指ししないことを固定します
// （2026-10-20 に .backlog-sheet・.draft-form-box を一般の印 w-no-table-menu・w-no-print に替え、拡張だけの CSS を各拡張へ移した）。
// 拡張の見た目は拡張の CSS に、コアが拡張の欄を見分けたいときは、拡張が付けるコアの一般の印（w-…）で。
func TestCoreScreenDoesNotNameExtensionClasses(t *testing.T) {
	extOnly := extensionOnlyClasses(t)
	if len(extOnly) == 0 {
		t.Fatal("拡張だけが作る class を1つも見つけられません（試験の前提が崩れている）")
	}
	for _, rel := range walkFiles(t, "assets", func(rel string) bool {
		return isCoreScreen(rel) && (strings.HasSuffix(rel, ".css") || strings.HasSuffix(rel, ".js"))
	}) {
		text := stripComments(rel, read(t, rel))
		for i, line := range strings.Split(text, "\n") {
			for _, m := range selectorClass.FindAllStringSubmatch(line, -1) {
				if extOnly[m[1]] {
					t.Errorf("⚠ %s:%d がコアの外の class「.%s」（拡張が描くもの）を名指ししています: %s", rel, i+1, m[1], strings.TrimSpace(clip(line, 120)))
				}
			}
		}
	}
}
