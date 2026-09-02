package cms

// ─────────────────────────────────────────────────────────────────────────
// 公開専用ビューの合成（要件定義書 §4.4・認証認可設計 §10.5）
//
// 匿名の訪問者へは**編集用クロームを含まない体裁**を返します。編集用の殻を CSS で
// 隠す従来方式は、匿名にも不要なマークアップと `/assets/app.js` を配ってしまい、
// 開いた瞬間のちらつきの元でもありました（2026-08-25 の応急処置＝サーバーが
// `<body class="anonymous">` を刻む、はその場しのぎ。ここが本筋）。
//
// 体裁を分ける基準は**認証済みかどうか**の1点だけです（実効公開の判定は別途
// requirePageViewable が済ませている）。同じページでも、社員には編集できる殻、
// 訪問者には読むためのページが届きます。
//
// SEO/SNS共有のメタ情報（`description`・OGP・canonical）はここで組み立てます。
// **サニタイズを通ったあとに文字列を連結する経路**なので、埋める値は必ず
// `html.EscapeString` を通すこと（この規律が破れると保存型XSSになります——
// RenderComputedViews・RenderReferenceLinks・RenderAnchors・RenderPageShell と同じ責任）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"crypto/sha256"
	"encoding/hex"
	"html"
	"net/http"
	"os"
	"strings"

	stdhtml "golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/htmldoc"
)

const (
	// publicShellPath は公開専用ビューの殻です。
	publicShellPath = "assets/public.html"

	// headPlaceholder は title・description・OGP・canonical を差し込む位置です。
	headPlaceholder = "<!--WCMS_HEAD-->"

	// descriptionMaxRunes は description に載せる最大文字数です。
	// 検索結果に出る長さの目安（日本語で 120 字前後）に合わせています。
	descriptionMaxRunes = 120
)

// publicShellCache は公開用の殻です（読み込み機構は shellFile＝編集用と共通）。
var publicShellCache = &shellFile{path: publicShellPath}

// PageSEO は公開ページの見出し情報です。値は**未エスケープの生テキスト**で渡し、
// エスケープは RenderPublicShell が一手に引き受けます（呼び出し側ごとの
// 「掛け忘れ」を作らないため）。
type PageSEO struct {
	Title        string // ページ名（本文の h1 由来）
	Description  string // 本文の最初の段落から作った要約
	CanonicalURL string // 正規URL（絶対）
}

// RenderPublicShell は公開専用ビューの完成HTMLを組み立てます。
// bodyHTML はサニタイズ済みであること。
func RenderPublicShell(bodyHTML string, seo PageSEO) (string, error) {
	shell, err := publicShellCache.load()
	if err != nil {
		return "", err
	}
	out := strings.Replace(shell, contentPlaceholder, bodyHTML, 1)
	return strings.Replace(out, headPlaceholder, buildHeadTags(seo), 1), nil
}

// buildHeadTags は head へ差し込むメタ情報を組み立てます。
// 値はすべてエスケープします（利用者が本文へ書いた文字がそのまま来るため）。
func buildHeadTags(seo PageSEO) string {
	title := strings.TrimSpace(seo.Title)
	if title == "" {
		title = "w-cms"
	}
	var b strings.Builder
	b.WriteString("<title>" + html.EscapeString(title) + " - w-cms</title>\n")
	if d := strings.TrimSpace(seo.Description); d != "" {
		b.WriteString(`    <meta name="description" content="` + html.EscapeString(d) + "\">\n")
		b.WriteString(`    <meta property="og:description" content="` + html.EscapeString(d) + "\">\n")
	}
	b.WriteString(`    <meta property="og:title" content="` + html.EscapeString(title) + "\">\n")
	b.WriteString(`    <meta property="og:type" content="article">` + "\n")
	b.WriteString(`    <meta property="og:site_name" content="w-cms">` + "\n")
	if u := strings.TrimSpace(seo.CanonicalURL); u != "" {
		b.WriteString(`    <link rel="canonical" href="` + html.EscapeString(u) + "\">\n")
		b.WriteString(`    <meta property="og:url" content="` + html.EscapeString(u) + "\">")
	}
	return b.String()
}

// ExtractDescription は本文HTMLから description に使う要約を作ります。
//
// 人が別途書かなくても成立させるのが狙いで、**最初の段落**（h1 のあとの本文）から
// 取ります。見出しは題名と重複するので使いません。表や定義リストしか無いページは
// 空文字になり、そのときは description を出しません（中身の無いタグを出すより良い）。
func ExtractDescription(bodyHTML string) string {
	nodes, err := htmldoc.ParseFragment(bodyHTML)
	if err != nil {
		return ""
	}
	var found string
	for _, n := range nodes {
		if found != "" {
			break
		}
		WalkElements(n, func(el *stdhtml.Node) {
			if found != "" || el.Data != "p" {
				return
			}
			// 計算ビューの中身（サーバーが埋めたクローム）は本文ではない。
			if hasChromeAncestor(el) {
				return
			}
			if t := strings.TrimSpace(nodeText(el)); t != "" {
				found = t
			}
		})
	}
	return truncateRunes(collapseSpaces(found), descriptionMaxRunes)
}

// hasChromeAncestor は要素が .vocab-chrome の中にあるかを返します。
func hasChromeAncestor(el *stdhtml.Node) bool {
	for p := el.Parent; p != nil; p = p.Parent {
		if p.Type == stdhtml.ElementNode && strings.Contains(Attr(p, "class"), "vocab-chrome") {
			return true
		}
	}
	return false
}

// collapseSpaces は改行と連続空白を1つの空白へ潰します（メタ情報は1行なので）。
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateRunes は文字数（バイトではない）で切り詰め、切ったことを示す記号を足します。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// etagOf は本文から弱くない ETag を作ります。
// 内容が1バイトでも変われば値が変わるので、公開フラグの変化でも本文の変化でも
// 再検証が正しく働きます。
func etagOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// requestBaseURL はリクエストからサイトの基底URL（`https://example.com`）を作ります。
//
// `WCMS_BASE_URL` が設定されていればそれを最優先します——canonical と sitemap は
// **1つの正規表記**でなければ意味が薄く（www 有無・http/https の揺れが別URL扱いになる）、
// 運用者が明示できるようにしておくのが正解だからです。
//
// 設定が無ければリクエストから組み立てます。本番はリバースプロキシの背後なので
// `X-Forwarded-Proto` を見ますが、**前段がプロキシのときだけ**採用します
// （監査記録の接続元と同じ理由——無条件に信じると外から表記を操作できる）。
func requestBaseURL(r *http.Request) string {
	if base := strings.TrimRight(os.Getenv("WCMS_BASE_URL"), "/"); base != "" {
		return base
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" && auth.IsFromTrustedProxy(r) {
		if p := strings.TrimSpace(strings.Split(proto, ",")[0]); p == "http" || p == "https" {
			scheme = p
		}
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" && auth.IsFromTrustedProxy(r) {
		host = strings.TrimSpace(strings.Split(fh, ",")[0])
	}
	return scheme + "://" + host
}
