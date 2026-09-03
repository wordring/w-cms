package cms

// ─────────────────────────────────────────────────────────────────────────
// パンくずリスト（2026-09-03）
//
// 「左レールの親ページを無くして、本文より上の帯にパンくずリストを付けるとどうなる
// でしょう」（ユーザー）。左レールの `↑ 親ページへ` は**一段しか上がれず**、
// 木が深くなると「戻る」を繰り返すことになる。パンくずは道の全体を1行で見せる。
//
// **サーバーで組んで殻へ埋めます**。JS で `/api/page-meta` を辿ると先祖の数だけ
// 往復が要り、描いたあとに増える（ちらつく）——閲覧はゼロJSで完結させる、という
// 既存の線（計算ビューのサーバー事前描画と同じ）に合わせました。
//
// **現在のページは入れません**。真下に h1 があるので、入れると同じ言葉が2度出ます。
// パンくずの仕事は「いまどこの中に居るか」を示すことで、それは先祖だけで足ります。
//
// 認可: 先祖1つずつに [page.CanView] を掛け、読めないページは**題を伏せて `…`**
// にします（リンクにもしない）。
//   - 匿名では起きません——実効公開は親チェーンの AND なので、見えるページの
//     先祖は必ず見えます（page.EffectivePublic）。
//   - ログイン済みでは起き得ます（read は per-page）。伏せても「そこに何かある」
//     ことは伝わりますが、それは 403 が既に伝えている事実と同じ粒度です。
//
// サニタイズの**後**にHTMLを足す関数の1つです（RenderComputedViews・
// RenderReferenceLinks・RenderAnchors・RenderPageShell・RenderPublicShell と同じ
// エスケープ責任）——題は利用者が書いた文字なので、必ず EscapeString を通します。
// ─────────────────────────────────────────────────────────────────────────

import (
	"database/sql"
	"fmt"
	"html"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// breadcrumbMaxDepth は辿る先祖の上限です（循環と異常データでの暴走を止める）。
const breadcrumbMaxDepth = 64

// crumb はパンくず1つ分です。
type crumb struct {
	id     string
	title  string
	canSee bool
}

// RenderBreadcrumb は先祖の道を根に近い順で組み立てて返します。
// 先祖が無いページ（トップ）では空文字を返し、帯は CSS の :empty で畳まれます。
func RenderBreadcrumb(user *auth.User, pageID int) string {
	crumbs := ancestorCrumbs(user, pageID)
	if len(crumbs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, c := range crumbs {
		if i > 0 {
			// 区切りは装飾なので読み上げから外す（題だけが読まれる）。
			b.WriteString(`<span class="crumb-sep" aria-hidden="true">›</span>`)
		}
		if !c.canSee {
			b.WriteString(`<span class="crumb-hidden" title="読む権限がありません">…</span>`)
			continue
		}
		b.WriteString(`<a class="crumb" href="/` + c.id + `">` + html.EscapeString(c.title) + `</a>`)
	}
	return b.String()
}

// ancestorCrumbs は現在のページの先祖を根に近い順で返します。
// 題は派生索引（pages.title）から引きます——描画のたびに呼ぶので索引で足ります
// （参照リンクの pageExists と同じ理由）。
func ancestorCrumbs(user *auth.User, pageID int) []crumb {
	var rev []crumb
	seen := map[int]bool{pageID: true}
	cur := pageID
	for i := 0; i < breadcrumbMaxDepth; i++ {
		var parent sql.NullInt64
		if err := database.DB.QueryRow(
			`SELECT parent_id FROM pages WHERE id = ?`, cur).Scan(&parent); err != nil {
			break
		}
		if !parent.Valid {
			break // トップレベルまで来た
		}
		pid := int(parent.Int64)
		if seen[pid] {
			break // 万一の循環
		}
		seen[pid] = true

		var title string
		if err := database.DB.QueryRow(
			`SELECT title FROM pages WHERE id = ?`, pid).Scan(&title); err != nil {
			break // 親の行が無い（索引の欠け）——道が途切れるので、そこで止める
		}
		c := crumb{id: fmt.Sprintf("%0*d", page.IDLength, pid), canSee: page.CanView(user, pid)}
		if c.canSee {
			c.title = title
		}
		rev = append(rev, c)
		cur = pid
	}

	// 根に近い順へ入れ替える（辿りは子→親の向きなので逆順に溜まっている）。
	out := make([]crumb, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		out = append(out, rev[i])
	}
	return out
}
