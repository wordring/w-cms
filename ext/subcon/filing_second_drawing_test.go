package subcon

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// **同じ品物の二つ目の図面**（部品図と溶接図など）を、既にあるページへ並べます
// （2026-09-20 ユーザー:「図面が複数あるのは、部品図と溶接図などがあるからで、
// 品物としては一つです」）。
//
// ⚠ **それまでは黙って「改定」にしていました。** 行き先に同じ題のページがあるとき、
// 図面番号が違えば改定として合流していました——ところが**部品図と溶接図も「図面番号が
// 違う」**ので、片方が旧版として子ページへ押し込まれます。エラーも確認も出ません。

// secondRow は整理の1行を作ります（`Merge` だけ差し替えて使う）。
func secondRow(pageID, name, merge string) filingRequest {
	return filingRequest{
		PageID: pageID, Customer: "南北スポーツ", Stage: "現行",
		MachineName: "標準2輪", DrawingName: name, Merge: merge,
	}
}

// TestFileDrawingsNeedsChoiceWhenPageExists は、**未選択なら動かさない**ことを
// 固定します。
//
// ⚠ **これがこの変更の本体です。** どちらかを既定にすると、見ないまま押した人が
// その既定に従います——溶接図が黙って旧版になるのが、それまでの振る舞いでした。
func TestFileDrawingsNeedsChoiceWhenPageExists(t *testing.T) {
	const inbox = "000051"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(first, "取付ベース", "")})

	second := makeDrawingPageFrom(t, inbox, "pdf002", "K120-2", "取付ベース", "標準2輪", "南北スポーツ")
	results := postFiling(t, u, []filingRequest{secondRow(second, "取付ベース", "")})

	if len(results) != 1 || results[0].Outcome != "needs_choice" {
		t.Fatalf("選ばせていません（黙って改定にしていませんか）: %+v", results)
	}
	if results[0].TargetID != first {
		t.Errorf("行き先が示されていません: %+v", results[0])
	}
	// **動いていない**——通信箱に置いたまま。
	if meta, _ := page.ReadSidecar(second); meta.ParentID != inbox {
		t.Errorf("未選択なのに動いています: 親=%s", meta.ParentID)
	}
	// **旧版ページを作っていない**（黙って改定にしていない証拠）。
	if body := readPageBody(t, first); strings.Contains(body, "旧版") {
		t.Errorf("勝手に改定として扱っています:\n%s", body)
	}
}

// TestFileDrawingsAddsSecondDrawing は、**二つ目の図面として並ぶ**ことを固定します。
func TestFileDrawingsAddsSecondDrawing(t *testing.T) {
	const inbox = "000052"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(first, "取付ベース", "")})

	// 溶接図が届いた。**品物は同じなので、同じページに並べたい。**
	second := makeDrawingPageFrom(t, inbox, "pdf002", "K120-1W", "取付ベース溶接", "標準2輪", "南北スポーツ")
	results := postFiling(t, u, []filingRequest{secondRow(second, "取付ベース", "drawing")})

	if len(results) != 1 || results[0].Outcome != "added" {
		t.Fatalf("二つ目の図面として並んでいません: %+v", results)
	}

	body := readPageBody(t, first)
	// **2枚とも載っている。**
	if n := strings.Count(body, "<h2>図面</h2>"); n != 2 {
		t.Errorf("図面ブロックが2つになっていません: %d個\n%s", n, body)
	}
	for _, no := range []string{"K120-1", "K120-1W"} {
		if !strings.Contains(body, "<dd>"+no+"</dd>") {
			t.Errorf("図面番号 %s が載っていません:\n%s", no, body)
		}
	}
	// ⚠ **溶接図の図面名称が潰れていない。** 整理の欄に打つ「取付ベース」は**行き先を
	// 決めるため**の値で、運ぶブロックの名前ではありません。書き戻すと2枚目の名前が
	// 1枚目に化けます。
	if !strings.Contains(body, "<dd>取付ベース溶接</dd>") {
		t.Errorf("二つ目の図面の名称が潰れています:\n%s", body)
	}
	// **旧版ページを作っていない**（改定ではない）。
	if strings.Contains(body, "旧版") {
		t.Errorf("旧版ページを作っています:\n%s", body)
	}
	// **構造が壊れていない**——改訂履歴が図面ブロックに飲み込まれていない。
	if strings.Count(body, "<section") != strings.Count(body, "</section>") {
		t.Errorf("⚠ 本文が釣り合っていません:\n%s", body)
	}
	first1 := cms.FirstBlockHTML(body)
	if strings.Contains(first1, "改訂履歴") {
		t.Errorf("⚠ 改訂履歴が図面ブロックに飲み込まれています:\n%s", body)
	}
	// ⚠ **2枚が入れ子になっていないこと。** 足す位置を入れ子を数えずに探すと、
	// 2枚目が1枚目の**中**（ファイル表示の直後）へ入ります——釣り合いも枚数も
	// 名前も全部通るので、**これを見ないと気づけません**（変異試験で空振りしました）。
	// 入れ子になると、**1枚目を消したとき2枚目も一緒に消えます**。
	if n := strings.Count(first1, "<h2>図面</h2>"); n != 1 {
		t.Errorf("⚠ 2枚目が1枚目の中に入れ子になっています（1枚目を消すと両方消えます）:\n%s", first1)
	}
	// 仮のページは片付いている。
	if meta, _ := page.ReadSidecar(second); meta.ParentID == inbox {
		t.Errorf("仮のページが残っています")
	}
}

// TestFileDrawingsRefusesSameAttachmentEitherWay は、**同じ添付からの重複は
// どちらを選んでも止まる**ことを固定します。
//
// ⚠ 「二つ目の図面」を選べば通ってしまうと、同じ図面を2度整理しただけで**同じ図面が
// 2枚並びます**——改定の側だけ守っていても意味がありません。
func TestFileDrawingsRefusesSameAttachmentEitherWay(t *testing.T) {
	const inbox = "000053"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(first, "取付ベース", "")})

	// **同じ添付**から作られたページ（同じ図面を2度整理した）。
	again := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	for _, merge := range []string{"drawing", "revision"} {
		results := postFiling(t, u, []filingRequest{secondRow(again, "取付ベース", merge)})
		if len(results) != 1 || results[0].Outcome != "skipped" {
			t.Errorf("merge=%q で重複を止めていません: %+v", merge, results)
		}
	}
	if n := strings.Count(readPageBody(t, first), "<h2>図面</h2>"); n != 1 {
		t.Errorf("重複なのに図面が増えています: %d個", n)
	}
}
