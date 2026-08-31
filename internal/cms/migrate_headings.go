package cms

// ─────────────────────────────────────────────────────────────────────────
// 見出し形（D-2）への一度きり変換ツール（2026-08-31 ユーザー指示
// 「既存のHTMLファイルデータをクリーンアップしてください」）
//
// 既存ページの属性マーカーを「見える文字が宣言する形」へ書き換えます。
//
//	<section data-type="client-order">…            → <section><h2>顧客の発注書</h2>…
//	  <table data-type="client-order-items">…      →   <table>…（素の明細。Items 宣言で解釈）
//	<section data-type="file" data-src>＋受発注    → 容器を溶かし、受発注セクション自身が
//	                                                  ファイル名リンクを持つ（data-id は容器から継承）
//	<dl data-type="our-estimate">…                 → <section><h2>弊社の見積もり</h2><dl>…</dl></section>
//	<table data-type="inspection-record">…         → <section><h2>検査記録</h2><table>…</table></section>
//
// 変換しないもの:
//   - 可変タグ <dl data-type="tags">（チップUIの前提。見出し形の対象外）
//   - 受発注を含まない file 容器（単独のPDF添付ブロックとして現役）
//   - **最初の見出しが既にあり、表示名と違う言葉のセクション**——data-type を外すと
//     その言葉が機能を宣言してしまい、意味が変わる。安全側に倒して残す（件数を報告）
//   - 未知の data-type（レジストリに無い形式は見出し語を決められない）
//
// 方式は語彙モデル移行（2026-08-19 の migrate_vocab.go・撤去済み）と同じ:
// data/master 全体をバックアップ → 変換 → Sanitize → SyncIndex 再同期。
// 検証は「変換前後で索引の中身（形式・鍵・値）が一致する」ことをテストで固定
// （migrate_headings_test.go——正本がファイルであることの利点）。
//
// 処理は冪等です（変換後のHTMLに対象の data-type は残らないため、再実行しても
// 変化しない）。役目を終えたらこのファイルごと撤去します（migrate_vocab と同じ運命）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// ConvertHeadingHTML は本文HTML中の属性マーカーを見出し形へ変換します。
// 変換が起きなければ入力をそのまま返します（changed=false）。
func ConvertHeadingHTML(htmlStr string) (out string, changed bool, skipped int) {
	nodes, err := htmldoc.ParseFragment(htmlStr)
	if err != nil {
		return htmlStr, false, 0
	}
	// 最上位ノードには親が無く、wrap（親の InsertBefore）が書けないので、
	// 仮の容れ物に載せてから変換し、終わったら中身だけを書き出す。
	holder := &html.Node{Type: html.ElementNode, Data: "div"}
	for _, n := range nodes {
		holder.AppendChild(n)
	}
	convertHeadingNode(holder, &changed, &skipped)
	if !changed {
		return htmlStr, false, skipped
	}
	var sb strings.Builder
	for c := holder.FirstChild; c != nil; c = c.NextSibling {
		html.Render(&sb, c)
	}
	return sb.String(), true, skipped
}

// convertHeadingNode は木を深さ優先で変換します（子から先に変換すると、
// 容器の溶解時に中身が変換済みで運べる）。
func convertHeadingNode(n *html.Node, changed *bool, skipped *int) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling // 変換で c が差し替わっても走査が続くよう先に取る
		convertHeadingNode(c, changed, skipped)
		c = next
	}
	if n.Type != html.ElementNode {
		return
	}

	dt := Attr(n, "data-type")
	if dt == "" || dt == "tags" {
		return
	}
	def, ok := VocabDefByType(dt)
	if !ok {
		return // 未知の形式は見出し語を決められない（そのまま索引に載り続ける）
	}

	switch {
	case dt == "file" && n.Data == "section":
		dissolveFileContainer(n, changed)
	case def.Element == "section" && n.Data == "section":
		convertSectionForm(n, def, changed, skipped)
	case (def.Element == "dl" && n.Data == "dl") || (def.Element == "table" && n.Data == "table"):
		if def.Hidden {
			return // 明細表は親セクションの変換（convertSectionForm）が素にする
		}
		wrapInHeadingSection(n, def, changed)
	}
}

// convertSectionForm は業務文書ブロック・計算ビューのセクションを見出し形へ変えます。
func convertSectionForm(n *html.Node, def VocabDef, changed *bool, skipped *int) {
	if w := functionHeading(n); w != "" && w != def.DisplayName {
		// 既にある見出しが別の言葉——data-type を外すとその言葉が機能を宣言して
		// 意味が変わる。安全側に倒してこのセクションは残す。
		*skipped++
		return
	}
	removeAttr(n, "data-type")
	if functionHeading(n) == "" {
		h := &html.Node{Type: html.ElementNode, Data: "h2"}
		h.AppendChild(&html.Node{Type: html.TextNode, Data: def.DisplayName})
		n.InsertBefore(h, n.FirstChild)
	}
	// マーカー付きの明細表を素にする（Items 宣言で解釈される。素の巡回と同じ範囲）。
	if def.Items != "" {
		eachMarkedChild(n, func(c *html.Node) {
			if c.Data == "table" && Attr(c, "data-type") == def.Items {
				removeAttr(c, "data-type")
				*changed = true
			}
		})
	}
	*changed = true
}

// wrapInHeadingSection は単体の dl / table 形式を <section><h2>表示名</h2>…</section> で包みます。
// トップレベルのブロック識別子（data-id）は包んだ section が引き継ぎます。
func wrapInHeadingSection(n *html.Node, def VocabDef, changed *bool) {
	sec := &html.Node{Type: html.ElementNode, Data: "section"}
	if id := Attr(n, "data-id"); id != "" {
		sec.Attr = append(sec.Attr, html.Attribute{Key: "data-id", Val: id})
		removeAttr(n, "data-id")
	}
	h := &html.Node{Type: html.ElementNode, Data: "h2"}
	h.AppendChild(&html.Node{Type: html.TextNode, Data: def.DisplayName})
	sec.AppendChild(h)

	parent := n.Parent
	parent.InsertBefore(sec, n)
	parent.RemoveChild(n)
	removeAttr(n, "data-type")
	sec.AppendChild(n)
	*changed = true
}

// dissolveFileContainer は受発注を包んでいる file 容器を溶かします。
// 容器の中身（ファイル名リンク等）は受発注セクションの見出しの直後へ移り、
// トップレベルのブロック識別子（data-id）も受発注セクションが引き継ぎます。
// 受発注を含まない容器（単独のPDF添付）はそのまま残します。
func dissolveFileContainer(n *html.Node, changed *bool) {
	var order *html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "section" {
			if d, ok := VocabDefByType(vocabTypeOf(c)); ok && d.File {
				if order != nil {
					return // 受発注が2つ以上——想定外の形は触らない
				}
				order = c
			}
		}
	}
	if order == nil {
		return // 単独のPDF添付ブロック（現役の形）
	}

	// 容器の中身（order 以外）を order の見出しの直後へ、文書順のまま移す。
	insertAfter := order.FirstChild // 見出し（h1〜h6）が先頭に居るはず
	if insertAfter != nil && insertAfter.Type == html.ElementNode &&
		len(insertAfter.Data) == 2 && insertAfter.Data[0] == 'h' {
		insertAfter = insertAfter.NextSibling
	} else {
		insertAfter = order.FirstChild
	}
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c != order {
			n.RemoveChild(c)
			order.InsertBefore(c, insertAfter)
		}
		c = next
	}

	// ブロック識別子は容器から引き継ぐ（参照の宛先が変わらないように）。
	if id := Attr(n, "data-id"); id != "" && Attr(order, "data-id") == "" {
		order.Attr = append(order.Attr, html.Attribute{Key: "data-id", Val: id})
	}

	parent := n.Parent
	n.RemoveChild(order)
	parent.InsertBefore(order, n)
	parent.RemoveChild(n)
	*changed = true
}

// eachMarkedChild は section の中のマーカー付き要素を、入れ子の section へ降りずに渡します。
func eachMarkedChild(section *html.Node, fn func(*html.Node)) {
	walkSkippingNested(section, map[string]bool{"section": true}, func(c *html.Node) {
		if Attr(c, "data-type") != "" {
			fn(c)
		}
	})
}

// removeAttr は属性を取り除きます。
func removeAttr(n *html.Node, key string) {
	out := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Key != key {
			out = append(out, a)
		}
	}
	n.Attr = out
}

// MigrateHeadings は data/master の全ページを見出し形へ変換します。
// 実行前に全体をバックアップし、変換したページは SyncIndex で再同期します。
func MigrateHeadings() (converted, skipped int, backupDir string, err error) {
	backupDir = filepath.Join("data", "heading-migrate-backup-"+time.Now().Format("20060102-150405"))
	if err = copyDir(page.MasterDir, backupDir); err != nil {
		return 0, 0, "", err
	}

	err = filepath.Walk(page.MasterDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".html") {
			return walkErr
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, changed, skip := ConvertHeadingHTML(string(content))
		skipped += skip
		if !changed {
			return nil
		}
		// 保存経路と同じく、正本は清書された状態で書く（tbody 補完などの正規化を
		// 保存経路と揃えるため）。
		out = Sanitize(out)
		if err := page.WriteFileAtomic(path, []byte(out), 0644); err != nil {
			return err
		}
		id := strings.TrimSuffix(info.Name(), ".html")
		if err := SyncIndex(id, out); err != nil {
			return err
		}
		converted++
		return nil
	})
	return converted, skipped, backupDir, err
}

// copyDir は dir を dst へ再帰コピーします（バックアップ用）。
func copyDir(dir, dst string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		w, err := os.Create(target)
		if err != nil {
			return err
		}
		defer w.Close()
		_, err = io.Copy(w, src)
		return err
	})
}

// MigrateHeadingsAPIHandler は POST /api/migrate-headings（admin のみ）です。
func MigrateHeadingsAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !page.RequireAdmin(w, r) {
		return
	}
	converted, skipped, backupDir, err := MigrateHeadings()
	if err != nil {
		http.Error(w, "変換エラー: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if u := auth.CurrentUser(r); u != nil {
		auth.Audit(u.Username, "migrate-headings",
			strconv.Itoa(converted)+"ページ変換 / 見送り"+strconv.Itoa(skipped)+"箇所")
	}
	log.Printf("見出し形へ変換しました: %dページ（見送り %d 箇所）バックアップ: %s", converted, skipped, backupDir)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "converted": converted, "skipped": skipped, "backup": backupDir,
	})
}
