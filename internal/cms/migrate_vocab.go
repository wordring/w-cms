package cms

// ─────────────────────────────────────────────────────────────────────────
// 語彙モデルへの一度きり変換ツール（docs/【考察】語彙モデル.md §8.3・移行第2段）
//
// 既存ページの旧カスタム要素を「マーカー付き標準HTML」へ書き換えます。
//
//   <m-tag name value>（連続する並び）    → <dl data-type="tags"> 1つ（dt/dd の組）
//   <m-material ...>（連続する並び）      → <table data-type="part-materials"> 1つ
//
// 方式は「サーバー側で全ページを走査し、変換 → SyncIndex 再同期」（DB再構築と
// 同じ流儀）。実行前に data/master 全体をバックアップします。検証は
// 「変換前後で Sync() の抽出結果が一致する」ことをテストで固定してあります
// （migrate_vocab_test.go——正本がファイルであることの利点）。
//
// 変換しないもの:
//   - 配線の <m-tag>（受信元・前版）——表示されない参照であり dl へ移せない。
//     移行第3段の参照マーカーが引き受ける（§8.1）。
//   - 旧方式の <m-tag name="親ページID">——サイドカーへ移行済みの遺物。dl に変換すると
//     可視のタグとして復活してしまうため、そのまま残す（同期からは従来どおり除外）。
//   - 名前が空の <m-tag>——鍵が決まらないため触らない。
//
// 処理は冪等です（変換後のHTMLに旧要素は残らないため、再実行しても変化しない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// wiringTagNames は dl へ移せない「配線」の m-tag 名です（移行第3段の参照マーカー行き）。
var wiringTagNames = map[string]bool{"受信元": true, "前版": true}

// convertibleMTag は dl へ変換してよい <m-tag> かを返します。
func convertibleMTag(n *html.Node) bool {
	if n.Type != html.ElementNode || n.Data != "m-tag" {
		return false
	}
	name := Attr(n, "name")
	return name != "" && name != legacyParentTagName && !wiringTagNames[name]
}

func isElementNamed(n *html.Node, name string) bool {
	return n.Type == html.ElementNode && n.Data == name
}

// ConvertVocabHTML は本文HTML中の旧要素を新形式へ変換します。
// 変換が起きなければ入力をそのまま返します（changed=false）。
func ConvertVocabHTML(htmlStr string) (out string, changed bool) {
	nodes, err := htmldoc.ParseFragment(htmlStr)
	if err != nil {
		return htmlStr, false
	}
	converted := convertNodeList(nodes, &changed)
	if !changed {
		return htmlStr, false
	}
	var sb strings.Builder
	for _, n := range converted {
		if err := html.Render(&sb, n); err != nil {
			return htmlStr, false // 描画できないなら安全側（無変換）に倒す
		}
	}
	return sb.String(), true
}

// convertNodeList は兄弟ノード列を変換した新しい列を返します。
// **連続する**変換対象（間の空白テキストは無視）を1つの dl / table へまとめるため、
// 個々のノードではなく列に対して働きます。対象以外の要素は子リストだけ再帰します。
func convertNodeList(nodes []*html.Node, changed *bool) []*html.Node {
	var out []*html.Node
	i := 0
	for i < len(nodes) {
		n := nodes[i]

		if convertibleMTag(n) {
			run := collectRun(nodes, &i, convertibleMTag)
			out = append(out, buildTagsDL(run))
			*changed = true
			continue
		}
		if isElementNamed(n, "m-material") {
			run := collectRun(nodes, &i, func(n *html.Node) bool { return isElementNamed(n, "m-material") })
			out = append(out, buildMaterialsTable(run))
			*changed = true
			continue
		}

		// それ以外の要素は子リストを再帰的に変換して差し替える
		// （m-file の中の m-material など、入れ子でも同じ規則が効く）。
		if n.Type == html.ElementNode && n.FirstChild != nil {
			kids := childNodesOf(n)
			for _, c := range kids {
				n.RemoveChild(c)
			}
			for _, c := range convertNodeList(kids, changed) {
				n.AppendChild(c)
			}
		}
		out = append(out, n)
		i++
	}
	return out
}

// collectRun は nodes[*i] から始まる「match が続く並び」を集めて返します。
// 間の空白だけのテキストノードは読み飛ばします（整形由来のため落とす）。
func collectRun(nodes []*html.Node, i *int, match func(*html.Node) bool) []*html.Node {
	var run []*html.Node
	for *i < len(nodes) {
		n := nodes[*i]
		if match(n) {
			run = append(run, n)
			*i++
			continue
		}
		if n.Type == html.TextNode && strings.TrimSpace(n.Data) == "" {
			// 空白を挟んで対象が続くなら、まとめて1ブロックにする
			if *i+1 < len(nodes) && match(nodes[*i+1]) {
				*i++
				continue
			}
		}
		break
	}
	return run
}

func childNodesOf(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, c)
	}
	return out
}

func newElement(name string, attrs ...html.Attribute) *html.Node {
	return &html.Node{Type: html.ElementNode, Data: name, Attr: attrs}
}

func newText(s string) *html.Node {
	return &html.Node{Type: html.TextNode, Data: s}
}

// carryBlockID は変換元の先頭ブロックの data-id を変換先へ引き継ぎます
// （ブロックの同一性を保ち、以後もブロック単位保存が効くように）。
func carryBlockID(dst *html.Node, run []*html.Node) {
	if id := Attr(run[0], htmldoc.BlockIDAttr); id != "" {
		dst.Attr = append(dst.Attr, html.Attribute{Key: htmldoc.BlockIDAttr, Val: id})
	}
}

// buildTagsDL は連続する <m-tag> を <dl data-type="tags"> 1つへまとめます。
// 名前も値も**表示される実テキスト**になる（語彙モデル §2: 値を属性に隠さない）。
func buildTagsDL(run []*html.Node) *html.Node {
	dl := newElement("dl", html.Attribute{Key: "data-type", Val: "tags"})
	carryBlockID(dl, run)
	for _, m := range run {
		dt := newElement("dt")
		dt.AppendChild(newText(Attr(m, "name")))
		dd := newElement("dd")
		dd.AppendChild(newText(Attr(m, "value")))
		dl.AppendChild(dt)
		dl.AppendChild(dd)
	}
	return dl
}

// buildMaterialsTable は連続する <m-material> を <table data-type="part-materials">
// 1つへまとめます。見出し行（鍵と型）はレジストリの宣言から生成し、エディタの
// 骨格生成と同じ形になります（③計算形式なので data-field を付与）。
func buildMaterialsTable(run []*html.Node) *html.Node {
	def, _ := VocabDefByType("part-materials")

	table := newElement("table", html.Attribute{Key: "data-type", Val: "part-materials"})
	carryBlockID(table, run)
	tbody := newElement("tbody")
	table.AppendChild(tbody)

	head := newElement("tr")
	for _, col := range def.Columns {
		th := newElement("th", html.Attribute{Key: "data-field", Val: col.Field})
		th.AppendChild(newText(col.Label))
		head.AppendChild(th)
	}
	tbody.AppendChild(head)

	for _, m := range run {
		tr := newElement("tr")
		for _, col := range def.Columns {
			td := newElement("td")
			// 属性の生の値をそのままセルの文字へ（quantity の空は空のまま——
			// 同期側の既定値 1 が新旧で同じに効く）。
			if v := Attr(m, col.Field); v != "" {
				td.AppendChild(newText(v))
			}
			tr.AppendChild(td)
		}
		tbody.AppendChild(tr)
	}
	return table
}

// MigrateVocab は data/master の全ページを走査して旧要素を新形式へ変換し、
// 変換したページを SyncIndex で再同期します。実行前に data/master 全体を
// バックアップします（戻すときはバックアップを data/master に戻して再起動
// →RebuildIfEmpty、または「DB再構築」）。
func MigrateVocab() (converted int, backupDir string, err error) {
	backupDir = filepath.Join("data", "vocab-migrate-backup-"+time.Now().Format("20060102-150405"))
	if err = copyDir(page.MasterDir, backupDir); err != nil {
		return 0, "", err
	}

	err = filepath.Walk(page.MasterDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".html") {
			return walkErr
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, changed := ConvertVocabHTML(string(content))
		if !changed {
			return nil
		}
		// 保存経路と同じく、正本は清書された状態で書く（変換結果は許可リストの
		// 範囲内だが、tbody 補完などの正規化を保存経路と揃えるため）。
		out = Sanitize(out)
		if err := os.WriteFile(path, []byte(out), 0644); err != nil {
			return err
		}
		id := strings.TrimSuffix(info.Name(), ".html")
		if err := SyncIndex(id, out); err != nil {
			return err
		}
		converted++
		return nil
	})
	return converted, backupDir, err
}

// copyDir は src ディレクトリを dst へ再帰コピーします（バックアップ用）。
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir // data/master がまだ無い環境では何もしない
			}
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

// MigrateVocabAPIHandler は語彙モデルへの一度きり変換を実行します（admin のみ）。
func MigrateVocabAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !page.RequireAdmin(w, r) {
		return
	}

	converted, backupDir, err := MigrateVocab()
	if err != nil {
		http.Error(w, "変換に失敗しました: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if u := auth.CurrentUser(r); u != nil {
		auth.Audit(u.Username, "migrate-vocab", "")
	}
	log.Printf("語彙モデルへの変換: %d ページを変換（バックアップ: %s）", converted, backupDir)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"converted": converted,
		"backup":    backupDir,
	})
}
