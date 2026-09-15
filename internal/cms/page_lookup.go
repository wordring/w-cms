package cms

// ─────────────────────────────────────────────────────────────────────────
// ページの索引を「名前で引く」小さな口（2026-09-15）
//
// どれも `pages` 表への1行の問い合わせで、業務の匂いはありません。**拡張から使う
// 公開ヘルパ**なので1か所に集めました——もとは `webdav_write.go`・`intake.go` に
// 散っていて、アドレス帳を `ext/comm/contacts` へ出すときに公開したものです。
//
// 同じ問いを各所が生の SQL で書き写していました（題の引き方だけで6箇所）。
// ここへ寄せたのは行数のためではなく、**「引けなかった」の扱いを1つに揃える**ため
// です——ある所は空文字、ある所はエラー、ある所は NULL で Scan が落ちる、と
// 割れていると、同じページが画面ごとに違う顔をします。
//
// ⚠ **「行が無い」と「題が空」を区別したい所は使えません**。`breadcrumb.go` は
// 親の行が無ければ道を打ち切るので、生の SQL のままにしてあります。
// ─────────────────────────────────────────────────────────────────────────

import (
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// PageTitleByID は索引からページの題を引きます（引けなければ空）。
//
// 行が無くても題が NULL でも空を返します——呼ぶ側は「表示する題が無い」とだけ
// 扱えばよく、原因を区別しません。
func PageTitleByID(idInt int) string {
	var t string
	database.DB.QueryRow(`SELECT COALESCE(title, '') FROM pages WHERE id = ?`, idInt).Scan(&t)
	return t
}

// TopLevelPageByTitle はトップ直下の題一致ページを返します
// （通信箱・テンプレート置き場・取引先が共有する——「名前が機能」という同じ仕様）。
func TopLevelPageByTitle(title string) (string, bool) {
	var id int
	err := database.DB.QueryRow(
		`SELECT id FROM pages WHERE parent_id = 0 AND title = ? LIMIT 1`, title).Scan(&id)
	if err != nil {
		return "", false
	}
	return page.FormatID(id), true
}
