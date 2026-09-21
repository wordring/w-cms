package cms

import (
	"strings"
	"testing"

	"w-cms/internal/database"
)

// mustPage は参照先として引けるページを索引へ1件作ります
// （`pageExists` は `pages` 表だけを見るので、行が1つあれば足ります）。
func mustPage(t *testing.T, id int) {
	t.Helper()
	if _, err := database.DB.Exec(
		`INSERT INTO pages (id, title, file_path) VALUES (?, '参照先', '')`, id); err != nil {
		t.Fatalf("ページ作成エラー: %v", err)
	}
}

// **表の参照の列もリンクになります**（2026-09-20）。
//
// `弊社品番`（加工製品ページのページ番号）を受注明細に足したためです——ユーザー:
// 「**加工中に番号を使うので**、受注の表に弊社品番として製造製品のページIDを入れたい」。
// 押して飛べないと意味が半分になります。
//
// ⚠ **列の宣言が `ref` のときだけ**です。可変タグの側で学んだことがそのまま効きます
// ——「**値の形だけでは参照と番号を見分けられない**」（実データの発注書番号が
// 「6桁＋ハイフン＋英数字」に当てはまり、全件が薄赤になった 2026-09-04 の事故）。

const orderItemsHeader = `<tr><th>弊社品番</th><th>品番</th><th>品名</th>` +
	`<th>単価</th><th>数量</th><th>納期</th><th>備考</th><th>状態</th></tr>`

// TestTableRefCellBecomesLink は、**参照の列が押せる**ことを固定します。
func TestTableRefCellBecomesLink(t *testing.T) {
	setupSaveTest(t)
	mustPage(t, 47)

	body := `<h1>受注</h1><table data-type="client-order-items"><tbody>` + orderItemsHeader +
		`<tr><td>000047</td><td>P103-227-6</td><td>ブラケット</td><td>390</td>` +
		`<td>100</td><td>2026-10-15</td><td>材質変更</td><td>未着手</td></tr>` +
		`</tbody></table>`

	got := RenderReferenceLinks(body)
	if !strings.Contains(got, `href="/000047"`) {
		t.Errorf("弊社品番がリンクになっていません:\n%s", got)
	}
	// ⚠ **他の列は触らない。** `品番` は `code` で、`P103-227-6` は参照の形に
	// 当てはまりませんが、**当てはまる形の品番が来ても触ってはいけません**——
	// 見分けるのは列の宣言です。
	if strings.Contains(got, `href="/P103`) {
		t.Errorf("品番までリンクにしています:\n%s", got)
	}
}

// TestTableRefCellMarksMissing は、**指す先が無ければ薄赤**になることを固定します。
func TestTableRefCellMarksMissing(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>受注</h1><table data-type="client-order-items"><tbody>` + orderItemsHeader +
		`<tr><td>000999</td><td>P103-227-6</td><td>ブラケット</td><td>390</td>` +
		`<td>100</td><td></td><td></td><td>未着手</td></tr>` +
		`</tbody></table>`

	got := RenderReferenceLinks(body)
	if !strings.Contains(got, `ref-missing`) {
		t.Errorf("宙ぶらりんの参照が知らされていません:\n%s", got)
	}
}

// TestTableRefCellIgnoresEmpty は、**空欄を薄赤にしない**ことを固定します。
//
// ⚠ 解析は弊社品番を**空で出します**（結ぶのは人）。空を宙ぶらりんとして扱うと、
// **まだ決めていない行が全部赤くなり**、本当の間違いが埋もれます。
func TestTableRefCellIgnoresEmpty(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>受注</h1><table data-type="client-order-items"><tbody>` + orderItemsHeader +
		`<tr><td></td><td>P103-227-6</td><td>ブラケット</td><td>390</td>` +
		`<td>100</td><td></td><td></td><td>未着手</td></tr>` +
		`</tbody></table>`

	if got := RenderReferenceLinks(body); strings.Contains(got, `ref-missing`) {
		t.Errorf("空欄を宙ぶらりん扱いしています:\n%s", got)
	}
}

// TestTableRefLeavesPlainTablesAlone は、**登録されていない表を触らない**ことを
// 固定します。
//
// ⚠ **これが 2026-09-04 の事故の再来を防ぎます。** 普通の文章の表に `000047` や
// `250401-203` のような数が並ぶのは珍しくありません——形だけで判断すると、
// **関係のない数字が全部リンクか薄赤になります**。
func TestTableRefLeavesPlainTablesAlone(t *testing.T) {
	setupSaveTest(t)
	mustPage(t, 47)

	// ⚠ **見出しは辞書で `ref` の語にします**（`受信元`）。`番号` のような語だと
	// 「列が参照でない」ほうの守りが先に効いてしまい、**この試験は登録の有無を
	// 測っていない**ことになります（変異試験で空振りしました）。
	// 人が普通の文章に `受信元` という列の表を書くのは珍しくありません。
	body := `<h1>メモ</h1><table><tbody>` +
		`<tr><th>受信元</th><th>内容</th></tr>` +
		`<tr><td>000047</td><td>これはただの数</td></tr>` +
		`<tr><td>250401-203</td><td>発注書番号</td></tr>` +
		`</tbody></table>`

	got := RenderReferenceLinks(body)
	if strings.Contains(got, "ref-link") || strings.Contains(got, "ref-missing") {
		t.Errorf("登録されていない表に手を入れています:\n%s", got)
	}
}

// TestTableRefIgnoresNonRefColumns は、**参照でない列は形が合っても触らない**ことを
// 固定します。
//
// ⚠ 実データの発注書番号 `250401-203` は「6桁＋ハイフン＋英数字」で、**参照の文法に
// そのまま当てはまります**。2026-09-04 に素の `dl` まで見ていたころ、これで全件が
// 薄赤になりました。表では**列の宣言**がその歯止めです。
func TestTableRefIgnoresNonRefColumns(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>受注</h1><table data-type="client-order-items"><tbody>` + orderItemsHeader +
		`<tr><td></td><td>250401-203</td><td>ブラケット</td><td>390</td>` +
		`<td>100</td><td></td><td></td><td>未着手</td></tr>` +
		`</tbody></table>`

	got := RenderReferenceLinks(body)
	if strings.Contains(got, "ref-link") || strings.Contains(got, "ref-missing") {
		t.Errorf("⚠ 参照でない列を参照として扱っています（2026-09-04 の事故の再来）:\n%s", got)
	}
}

// TestTableRefSkipsChromeRows は、⚠ **鏡が描いた行を本文データとして読まない**ことを
// 固定します。
//
// ⚠ **2026-09-21 に実データで踏みました。** 検算の鏡が `<tfoot>` へ足す行は
// `colspan` で横いっぱいのセル1つなので、**1列目（`弊社品番`＝`ref`）の値**と
// 読まれ、**「✓ 合っています」の行が宙ぶらりんの参照の薄赤で塗られて**いました
// ——**合格を警告の色で見せる**、いちばん誤解を生む壊れ方です。
//
// ⚠ **順番がこの罠を作ります**——`RenderReferenceLinks` は `RenderComputedViews` の
// **後**に走るので、**鏡が描いたものを本文として読みます**。クロームを足す鏡が
// 増えるたびに同じ穴が開くので、番人をここに置きます。
func TestTableRefSkipsChromeRows(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>受注</h1><table data-type="client-order-items"><tbody>` + orderItemsHeader +
		`<tr><td></td><td>P103-227-6</td><td>ブラケット</td><td>390</td>` +
		`<td>100</td><td></td><td></td><td>未着手</td></tr>` +
		`</tbody><tfoot class="vocab-chrome">` +
		`<tr class="order-checksum checksum-ok"><td colspan="8">✓ 検算: 合っています</td></tr>` +
		`</tfoot></table>`

	got := RenderReferenceLinks(body)
	if strings.Contains(got, "ref-missing") {
		t.Errorf("⚠ 鏡の行を宙ぶらりんの参照として塗っています（合格が警告の色になります）:\n%s", got)
	}
	// 本文の行はこれまでどおり扱われること（空欄なので薄赤にはならない）。
	if !strings.Contains(got, "✓ 検算: 合っています") {
		t.Errorf("鏡の行が消えています:\n%s", got)
	}
}
