package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 加工製品の一覧は `ページ種類：加工製品` のページを、置いた場所に関係なく集めます（2026-10-16 利用者:
// 「加工製品の一覧のビューはどこにおいても機能するようにフォルダではなくDBから一覧を作ってはどうでしょう」）。

// seedKindPage はページを1枚作り、可変タグ（名前と値の組）を本文に入れて索引に写します。
func seedKindPage(t *testing.T, id, parent int, title string, tags ...string) {
	t.Helper()
	addPage(t, id, parent, title, "alice", "302", true)
	var b strings.Builder
	b.WriteString(`<h1>` + title + `</h1>`)
	if len(tags) > 0 {
		b.WriteString(`<dl data-type="tags">`)
		for i := 0; i+1 < len(tags); i += 2 {
			b.WriteString(`<dt>` + tags[i] + `</dt><dd>` + tags[i+1] + `</dd>`)
		}
		b.WriteString(`</dl>`)
	}
	if err := cms.SyncIndex(page.FormatID(id), b.String()); err != nil {
		t.Fatalf("SyncIndex(%d): %v", id, err)
	}
}

// TestProductListCollectsByPageKind は、一覧が `ページ種類` だけで加工製品を選び、社名と装置名称を行に添えることを
// 固定します。
//
//   - 別の会社・整理を待つ通信記録の下の加工製品も並ぶ（置いた場所に依存しない）。
//   - 旧版（`加工製品の旧版`）と、図面番号を持っていても `ページ種類` の無いページは並ばない。
//   - 社名と装置名称: フォルダの種類で読む。種類の無いフォルダは木の形で読む（トップ直下の「製造」の子が社名）。
//   - どのページに置いても、同じ一覧が出る。
func TestProductListCollectsByPageKind(t *testing.T) {
	setupExtTest(t, "000700", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 701, -1, CustomerBoxTitle, "alice", "302", true)
	// 社名 A（種類あり）／装置（種類あり）／加工製品・その旧版。
	seedKindPage(t, 702, 701, "みなと商店", PageKindTag, PageKindCompany)
	seedKindPage(t, 703, 702, "装置X", PageKindTag, PageKindMachine)
	seedKindPage(t, 704, 703, "ブラケット", PageKindTag, PageKindProduct, DrawingNoTag, "A100-1")
	seedKindPage(t, 705, 704, "旧版 A100-0 ブラケット", PageKindTag, PageKindProductOld, DrawingNoTag, "A100-0")
	// 社名 B（種類なし——機械が新しく作ったフォルダ）／装置（種類なし）／加工製品。装置の無い加工製品も。
	seedKindPage(t, 712, 701, "かなめ商会")
	seedKindPage(t, 713, 712, "装置Y")
	seedKindPage(t, 714, 713, "カバー", PageKindTag, PageKindProduct, DrawingNoTag, "K120-1")
	seedKindPage(t, 715, 712, "シャフト", PageKindTag, PageKindProduct, DrawingNoTag, "K120-2")
	// 図面番号はあるが、ページ種類の無いページ。
	seedKindPage(t, 716, 712, "種類なし", DrawingNoTag, "K120-9")
	// 整理を待つ加工製品（よその箱の下）。
	addPage(t, 721, -1, "通信箱", "alice", "302", true)
	seedKindPage(t, 722, 721, "見積のメール", "ページ種類", "通信記録")
	seedKindPage(t, 723, 722, "プレート", PageKindTag, PageKindProduct, DrawingNoTag, "P1")

	rows, err := productListAll(adminUser())
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, r := range rows {
		got = append(got, r.Company+"／"+r.Machine+"／"+r.Title)
	}
	// 社名の順（かなめ商会 → みなと商店）・その中は装置名称の順（装置の無いものは最後）・整理を待つものは最後。
	want := []string{"かなめ商会／装置Y／カバー", "かなめ商会／／シャフト", "みなと商店／装置X／ブラケット", "／／プレート"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("一覧が違います\n got: %v\nwant: %v", got, want)
	}

	// どこに置いても同じ——製造の根でも、社名の下でも、通信箱でも。
	a, b, c := productListViewHTML(adminUser(), 701), productListViewHTML(adminUser(), 702), productListViewHTML(adminUser(), 721)
	if a != b || a != c {
		t.Errorf("⚠ 置いたページで一覧が変わっています")
	}
	for _, want := range []string{
		`data-plist-company="1"`, `<option value="みなと商店">`, `<option value="かなめ商会">`,
		`<option value="-">（整理を待つもの）</option>`, `data-company="-"`, `4 件`,
	} {
		if !strings.Contains(a, want) {
			t.Errorf("一覧に %q がありません:\n%s", want, a)
		}
	}
	if strings.Contains(a, "種類なし") || strings.Contains(a, "旧版 A100-0") {
		t.Errorf("⚠ ページ種類の無いページか旧版が一覧に出ています:\n%s", a)
	}
}
