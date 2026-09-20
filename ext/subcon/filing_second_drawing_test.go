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

// TestFileDrawingsAsksOnSameAttachmentEitherWay は、**同じ添付・同じ図面番号は
// どちらを選んでも確認を求める**ことを固定します。
//
// ⚠ **止めません**（2026-09-20 ユーザー:「実は、同じ添付同じPDFの中に**同じ図面番号で
// 別図面**が入っているものがありました」「本来は同じ図番で別の図面はあってはならない
// のですが、どういうわけか指摘しても理解できない人も居るようです」）。
// **本来あってはならない形が実在する**ので、機械が問答無用で止めると正しい仕事が
// できなくなります。既定は強く疑い、人が知っていれば通す形にしました。
//
// ⚠ 「二つ目の図面」を選べば素通りしてしまうと、同じ図面を2度整理しただけで
// **同じ図面が2枚並びます**——改定の側だけ守っていても意味がありません。
func TestFileDrawingsAsksOnSameAttachmentEitherWay(t *testing.T) {
	const inbox = "000053"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(first, "取付ベース", "")})

	// **同じ添付**から作られたページ（同じ図面を2度整理した）。
	again := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	for _, merge := range []string{"drawing", "revision"} {
		results := postFiling(t, u, []filingRequest{secondRow(again, "取付ベース", merge)})
		if len(results) != 1 || results[0].Outcome != "needs_confirm" {
			t.Errorf("merge=%q で確認を求めていません: %+v", merge, results)
		}
	}
	// **確認前は増えていない。**
	if n := strings.Count(readPageBody(t, first), "<h2>図面</h2>"); n != 1 {
		t.Errorf("確認前なのに図面が増えています: %d個", n)
	}

	// **人が「承知のうえで進める」を押したら通る**（先方の誤りで実際に別図面だった）。
	row := secondRow(again, "取付ベース", "drawing")
	row.ConfirmRevision = true
	if results := postFiling(t, u, []filingRequest{row}); len(results) != 1 || results[0].Outcome != "added" {
		t.Errorf("確認しても通りません: %+v", results)
	}
	if n := strings.Count(readPageBody(t, first), "<h2>図面</h2>"); n != 2 {
		t.Errorf("確認後に並んでいません: %d個", n)
	}
}

// TestFileDrawingsAllowsTwoDrawingsFromOnePDF は、**1つのPDFに入っていた2枚の図面**を
// 同じページへ並べられることを固定します（2026-09-20 ユーザー:「一つのPDFに複数の
// 図面が入っている場合もあるようです」）。
//
// ⚠ **これが塞がっていると「二つ目の図面」が使えません。** 部品図と溶接図が1つのPDFに
// 入っているのがまさにその場面で、2枚は**同じ `受信元`** を持ちます——「同じ添付なら
// 重複」としていたころは、2枚目が黙って断られていました。
func TestFileDrawingsAllowsTwoDrawingsFromOnePDF(t *testing.T) {
	const inbox = "000054"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	// **同じ添付**（pdf001）から2枚——部品図と溶接図が1つのPDFに入っていた形。
	first := makeDrawingPageFrom(t, inbox, "pdf001", "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(first, "取付ベース", "")})

	second := makeDrawingPageFrom(t, inbox, "pdf001", "K120-1W", "取付ベース溶接", "標準2輪", "南北スポーツ")
	results := postFiling(t, u, []filingRequest{secondRow(second, "取付ベース", "drawing")})

	if len(results) != 1 || results[0].Outcome != "added" {
		t.Fatalf("同じPDFの2枚目が断られています: %+v", results)
	}
	body := readPageBody(t, first)
	if n := strings.Count(body, "<h2>図面</h2>"); n != 2 {
		t.Errorf("2枚並んでいません: %d個\n%s", n, body)
	}

	// ⚠ **同じ添付・同じ図面番号は、黙って通さない**（確認を求める）。
	// 止めないのは「同じ図番で別図面」が実在するためですが、**素通りもさせません**。
	again := makeDrawingPageFrom(t, inbox, "pdf001", "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	res2 := postFiling(t, u, []filingRequest{secondRow(again, "取付ベース", "drawing")})
	if len(res2) != 1 || res2[0].Outcome != "needs_confirm" {
		t.Errorf("同じ番号が黙って通っています: %+v", res2)
	}
	if n := strings.Count(readPageBody(t, first), "<h2>図面</h2>"); n != 2 {
		t.Errorf("重複なのに3枚目が増えています: %d個", n)
	}
}

// TestFileDrawingsAsksWhenNumberUnknown は、**図面番号が読めないときも確認を求める**
// ことを固定します。
//
// ⚠ 同じ添付から来た2枚を区別する手掛かりは図面番号だけです。番号が空のまま黙って
// 通すと、**同じ図面が2枚並びます**——人に一度見てもらいます。
func TestFileDrawingsAsksWhenNumberUnknown(t *testing.T) {
	const inbox = "000055"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	// ⚠ **片方だけ番号が読めた形**にします（2026-09-20）。両方とも空だと
	// `"" == ""` が偶然一致して、**守りを外しても通る試験**になります（変異試験で
	// 空振りしました）。実際にもこの形が普通です——1枚のPDFの中で、Gemini が
	// 片方の表題欄だけ読めることがあります。
	first := makeDrawingPageFrom(t, inbox, "pdf001", "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(first, "取付ベース", "")})

	second := makeDrawingPageFrom(t, inbox, "pdf001", "", "取付ベース", "標準2輪", "南北スポーツ")
	results := postFiling(t, u, []filingRequest{secondRow(second, "取付ベース", "drawing")})

	if len(results) != 1 || results[0].Outcome != "needs_confirm" {
		t.Fatalf("番号が読めないのに黙って通しています: %+v", results)
	}
	if n := strings.Count(readPageBody(t, first), "<h2>図面</h2>"); n != 1 {
		t.Errorf("図面が増えています: %d個", n)
	}
}
