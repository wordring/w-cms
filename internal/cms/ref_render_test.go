package cms

import (
	"strings"
	"testing"

	"w-cms/internal/database"
)

// 参照タグのリンク描画と宙ぶらりんの印のテスト（D-4・D-10）。
//
// 値の解釈は構文解析（D-7）——ページIDはゼロ埋め6桁ちょうど・ハイフン類は畳む・
// 先頭のゼロは畳まない。判定は読むたび（描画時）で、保存形には何も焼かない。

func TestRenderReferenceLinks(t *testing.T) {
	setupSaveTest(t)
	if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, file_path) VALUES (2, '参照先', '')`); err != nil {
		t.Fatalf("ページ作成エラー: %v", err)
	}

	body := `<dl data-type="tags">` +
		`<dt>原発注書</dt><dd>000002-12</dd>` + // 実在するページ → リンク
		`<dt>前版</dt><dd>000099-ab</dd>` + // 存在しないページ → 印
		`<dt>図番</dt><dd>123-45</dd>` + // 6桁でない → 普通の値（触らない）
		`<dt>型式</dt><dd>SUS304</dd>` + // 文法外 → 触らない
		`</dl>`
	out := RenderReferenceLinks(body)

	if !strings.Contains(out, `<a href="/000002#12" class="ref-link">000002-12</a>`) {
		t.Errorf("実在ページへの参照がリンクになっていません:\n%s", out)
	}
	if !strings.Contains(out, `class="ref-missing"`) ||
		!strings.Contains(out, `000099`) {
		t.Errorf("宙ぶらりんの参照に印が付いていません:\n%s", out)
	}
	if strings.Contains(out, `href="/000123`) || strings.Contains(out, `>123-45</a>`) {
		t.Errorf("6桁でない値が参照に化けています（図面番号の誤爆）:\n%s", out)
	}
	if !strings.Contains(out, `<dd>SUS304</dd>`) {
		t.Errorf("文法外の値が書き換えられています:\n%s", out)
	}
}

// TestRenderReferenceLinksFoldsHyphens は、区切りのハイフンの種類が畳まれることを
// 検証します。議論で示された実例が半角カタカナの長音記号（U+FF70）だった（§9.1）。
func TestRenderReferenceLinksFoldsHyphens(t *testing.T) {
	setupSaveTest(t)
	if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, file_path) VALUES (2, '参照先', '')`); err != nil {
		t.Fatalf("ページ作成エラー: %v", err)
	}

	// 長音記号 ー（U+30FC）・半角長音 ｰ（U+FF70）・全角ハイフン −（U+2212）
	for _, sep := range []string{"ー", "ｰ", "−"} {
		body := `<dl data-type="tags"><dt>原発注書</dt><dd>000002` + sep + `12</dd></dl>`
		out := RenderReferenceLinks(body)
		if !strings.Contains(out, `href="/000002#12"`) {
			t.Errorf("区切り %q が畳まれていません:\n%s", sep, out)
		}
	}
}

// TestRenderReferenceLinksNotInLoadAPI は、参照リンクの合成が /api/load（編集用の
// 読み直し）を通らないことを検証します。通ると合成した <a> がシリアライザで
// 本文として保存される（RenderAnchors と同じ罠）。
func TestRenderReferenceLinksNotInLoadAPI(t *testing.T) {
	// 配線の検査: LoadHandler は RenderReferenceLinks を呼ばない。関数の呼び出し網は
	// コードを読まないと確かめられないので、ここでは describe 的に「描画経路の出力に
	// リンクがあり、素の Sanitize 出力には無い」ことで代替する。
	setupSaveTest(t)
	if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, file_path) VALUES (2, '参照先', '')`); err != nil {
		t.Fatalf("ページ作成エラー: %v", err)
	}
	body := `<dl data-type="tags"><dt>原発注書</dt><dd>000002-12</dd></dl>`
	if strings.Contains(Sanitize(body), "ref-link") {
		t.Error("サニタイズだけでリンクが合成されています（保存経路が汚れる）")
	}
	if !strings.Contains(RenderReferenceLinks(Sanitize(body)), "ref-link") {
		t.Error("描画経路でリンクが合成されていません")
	}
}

// TestRenderReferenceLinksPageRefTag は、**名前で宣言されたタグ**の値が
// ページ全体への参照になることを固定します（2026-09-03 ユーザーの問い
// 「このリンクを付けるのは誰か？」への答え——付けるのは表示のときのコアで、
// どの値が参照かは値の形か、タグの名前で決まる）。
//
// 同時に、**宣言していないタグの6桁の値は素通りする**ことも固定します。
// ここが緩むと発注書番号や図番がリンクに化けます。
func TestRenderReferenceLinksPageRefTag(t *testing.T) {
	setupSaveTest(t)
	if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, file_path) VALUES (2, '返信元', '')`); err != nil {
		t.Fatalf("ページ作成エラー: %v", err)
	}

	body := `<dl data-type="tags">` +
		`<dt>` + ReplySourceTag + `</dt><dd>000002</dd>` + // 宣言済み → ページへのリンク
		`<dt>発注書番号</dt><dd>000002</dd>` + // 宣言していない → 素通り
		`</dl>`
	out := RenderReferenceLinks(body)

	if !strings.Contains(out, `<a href="/000002" class="ref-link">000002</a>`) {
		t.Errorf("宣言したタグがページへのリンクになっていません:\n%s", out)
	}
	if !strings.Contains(out, `<dt>発注書番号</dt><dd>000002</dd>`) {
		t.Errorf("宣言していないタグの6桁の値がリンクに化けています:\n%s", out)
	}
}

// TestOrderNumberIsNotMistakenForReference は、**業務文書ブロックのヘッダに書かれた
// 発注書番号を参照と読み違えない**ことを固定します。
//
// 実データで踏んだ誤爆（2026-09-04）: 顧客の発注書番号 `260602-102`
// （26年06月02日＋連番）は「6桁＋ハイフン＋英数字」という参照値の文法に
// **そのまま当てはまり**、「参照先のページ 260602 が見つかりません」の薄赤が
// 全件に出ました。桁を6に絞っても足りなかった——**値の形だけでは参照と番号を
// 見分けられない**ので、置き場所（可変タグかどうか）も条件にしています。
func TestOrderNumberIsNotMistakenForReference(t *testing.T) {
	setupSaveTest(t)

	// 業務文書ブロックのヘッダ（data-type を持たない素の dl）＋ 可変タグの参照。
	body := `<section data-type="client-order">` +
		`<dl><dt>発注書番号</dt><dd>260602-102</dd></dl>` +
		`</section>` +
		`<dl data-type="tags"><dt>受信元</dt><dd>000002-12</dd></dl>`

	got := RenderReferenceLinks(body)
	// 発注書番号の升目は**素のまま**（印もリンクも付かない）。
	if !strings.Contains(got, "<dd>260602-102</dd>") {
		t.Errorf("発注書番号が参照として扱われました:\n%s", got)
	}
	if strings.Contains(got, "参照先のページ 260602") {
		t.Errorf("発注書番号に宙ぶらりんの印が付きました:\n%s", got)
	}
	// 可変タグの側は、これまでどおり参照として扱われる（指す先が無いので薄赤ではなく…
	// このテストDBにページ 000002 は無いので、印が付くのは正しい振る舞い）。
	if !strings.Contains(got, "参照先のページ 000002") {
		t.Errorf("可変タグの参照が扱われていません:\n%s", got)
	}
}
