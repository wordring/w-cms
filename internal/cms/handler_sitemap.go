package cms

// ─────────────────────────────────────────────────────────────────────────
// sitemap.xml と robots.txt（要件定義書 §4.4）
//
// どちらも**匿名の訪問者とクローラ向け**なので、載せてよいのは実効公開の
// ページだけです。実効公開＝自分と全先祖が public（パスゲート。認証認可設計 §10.2）で、
// 判定は認可と同じ `page.EffectivePublic` を通します——ここに独自の判定を書くと、
// 認可とずれた瞬間に**非公開ページのアドレスを sitemap が外へ配る**ことになります。
//
// robots.txt は**サイト全体が非公開なら全面拒否**を返します。業務インスタンスは
// 匿名に何も見せない運用（要件 §1.4）なので、クローラにも入らせないのが筋です。
// 判定はトップページの実効公開で行います——トップが非公開ならパスゲートにより
// 配下も全部非公開になるため、これが「サイトが閉じているか」と同義になります。
// ─────────────────────────────────────────────────────────────────────────

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// publicCacheSeconds は公開応答をキャッシュしてよい秒数です。
//
// **10分**（2026-08-21 ユーザー決定「キャッシュにとって必要な時間を知りませんが、
// 10分程度でどうでしょう？」）。この値は公開↔非公開の切り替えが外から見えるまでの
// 許容遅延でもあり、**非公開へ戻した側にも同じだけ効きます**——いったん配った応答は
// 取り消せないので、「公開→非公開だけ即時」という非対称は原理的に作れません
// （要件 §4.4 の「実装時に判断すること」への回答）。即時性が要るなら、この値を 0 に
// して `no-cache` 相当（毎回再検証・ETag で 304）へ倒すのが現実的な選択肢です。
const publicCacheSeconds = 600

// SitemapHandler は実効公開のページだけを載せた sitemap.xml を返します。
func SitemapHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	base := requestBaseURL(r)

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, p := range publicPages() {
		b.WriteString("  <url>\n")
		b.WriteString("    <loc>" + xmlEscape(base+"/"+p.ID) + "</loc>\n")
		if p.LastMod != "" {
			b.WriteString("    <lastmod>" + xmlEscape(p.LastMod) + "</lastmod>\n")
		}
		b.WriteString("  </url>\n")
	}
	b.WriteString("</urlset>\n")

	body := b.String()
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// 中身は公開ページの一覧なので、公開ページ本体と同じだけキャッシュしてよい。
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", publicCacheSeconds))
	w.Header().Set("Vary", "Cookie")
	w.Write([]byte(body))
}

// RobotsHandler は robots.txt を返します。
func RobotsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", publicCacheSeconds))

	// トップが非公開＝サイトが閉じている（パスゲートで配下も全部非公開）。
	// 索引が使えないときも閉じている扱いにする（フェイルクローズ。分からないなら招かない）。
	if database.DB == nil || !page.EffectivePublic(0) {
		w.Write([]byte("User-agent: *\nDisallow: /\n"))
		return
	}

	base := requestBaseURL(r)
	// アプリの口（/api/・/login）はクロールさせない。中身が無いか、
	// 認証を要求するだけで検索結果に載る価値が無い。
	// 添付（/data/）は本文の画像がここから配られるので閉じない。
	w.Write([]byte("User-agent: *\n" +
		"Disallow: /api/\n" +
		"Disallow: /login\n" +
		"Allow: /\n\n" +
		"Sitemap: " + base + "/sitemap.xml\n"))
}

// publicPageEntry は sitemap の1行です。
type publicPageEntry struct {
	ID      string
	LastMod string // W3C Datetime（RFC3339）。取れなければ空
}

// publicPages は実効公開のページをID順に返します。
//
// 判定は1ページずつ `page.EffectivePublic`（先祖を辿る）で行います。ページ数が
// 増えたらここが効いてきますが、応答自体をキャッシュするので実測で困るまでは
// 素直な実装のままにします——**先に非正規化すると、認可の正本とずれる余地を作る**
// ほうが害が大きいためです。
func publicPages() []publicPageEntry {
	if database.DB == nil {
		return nil // 索引が使えないなら何も案内しない（フェイルクローズ）
	}
	rows, err := database.DB.Query(`SELECT id, COALESCE(updated_at, '') FROM pages ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	type row struct {
		id        int
		updatedAt string
	}
	var all []row
	for rows.Next() {
		var rw row
		if err := rows.Scan(&rw.id, &rw.updatedAt); err == nil {
			all = append(all, rw)
		}
	}

	var out []publicPageEntry
	for _, rw := range all {
		if !page.EffectivePublic(rw.id) {
			continue
		}
		out = append(out, publicPageEntry{
			ID:      fmt.Sprintf("%0*d", page.IDLength, rw.id),
			LastMod: normalizeLastMod(rw.updatedAt),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// normalizeLastMod は updated_at の表記ゆれを W3C Datetime へ寄せます。
// サイドカー由来は RFC3339、フォールバックは SQLite の CURRENT_TIMESTAMP
// （`2006-01-02 15:04:05` UTC）という2通りがあります。
func normalizeLastMod(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return ""
}

// xmlEscape は XML のテキストとして安全な形にします。
// URL にはページIDしか入らないとはいえ、組み立てた文字列をそのまま流さない
// （エスケープ責任を経路ごとに負う、という本プロジェクトの規律）。
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}
