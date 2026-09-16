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
//
// **2枚あったら、いちばん古いものを返します**（2026-09-16 に `ORDER BY id` を足した）。
// それまで `LIMIT 1` だけだったので、**どちらが返るかは決まっていませんでした**
// ——同じ題のページが2枚できると、原理上は問い合わせのたびに別のページが
// 「通信箱」になりえます。⚠ **2枚あること自体は防げません**（題は人が自由に
// 付けられる）。**管理画面の「置き場」が知らせます**（`RequiredPageStatus.Duplicates`）
// ——2026-09-16 に取引先が実際に2枚になり、**片方が見えないまま残りました**。
func TopLevelPageByTitle(title string) (string, bool) {
	ids := TopLevelPagesByTitle(title)
	if len(ids) == 0 {
		return "", false
	}
	return ids[0], true
}

// TopLevelPagesByTitle は題の一致するトップ直下のページを**全部**古い順に返します。
//
// 分けてあるのは、「どれを使うか」（上）と「いくつあるか」（ここ）が別の問いだからです
// ——使う側は1枚として扱ってよく、**重複に気づく仕事は画面の側**が持ちます。
func TopLevelPagesByTitle(title string) []string {
	rows, err := database.DB.Query(
		`SELECT id FROM pages WHERE parent_id = 0 AND title = ? ORDER BY id`, title)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return out
		}
		out = append(out, page.FormatID(id))
	}
	return out
}
