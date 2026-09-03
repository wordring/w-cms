package sheetmetal

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// ZIP添付の中のDXFとの突き合わせのテスト。
//
// ユーザー:「PDFが沢山ある場合、ZIPされている場合があります」——実運用では
// 図面一式がZIPで届くので、**こちらが主経路**です。

// zipWith は指定の中身を持つZIPを作ります。
func zipWith(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("ZIP作成エラー: %v", err)
		}
		w.Write([]byte(content))
	}
	zw.Close()
	return buf.Bytes()
}

// dxfWithTitle は表題欄つきの最小のDXFを組みます。
func dxfWithTitle(t *testing.T, no, name string) string {
	t.Helper()
	return dxfEntity("TEXT", sjisBytes(t, "図面番号"), 100, 50, 2.5) +
		dxfEntity("TEXT", no, 100, 45, 2.5) +
		dxfEntity("TEXT", sjisBytes(t, "図面名称"), 100, 40, 2.5) +
		dxfEntity("TEXT", sjisBytes(t, name), 100, 35, 2.5)
}

// TestMatchDXFInsideZip は、ZIPの中のDXFが図面番号で見つかり、**中のパスも返る**
// ことを固定します。参照はZIPのリンクブロックを指すので、中のパスが無いと
// どのファイルか分かりません。
func TestMatchDXFInsideZip(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	putAttachment(t, id, "zip001.zip", zipWith(t, map[string]string{
		"図面一式/X008-135-4.dxf": dxfWithTitle(t, "X008-135-4", "架台Assy"),
		"図面一式/W240-001.dxf":   dxfWithTitle(t, "W240-001", "本体"),
		"図面一式/読んでね.txt":       "DXFではないので無視される",
	}))

	got := MatchDXFAttachments(id, "X008-135-4")
	if len(got) != 1 {
		t.Fatalf("ZIP内のDXFが1件見つかりません: %+v", got)
	}
	if got[0].AttachID != "zip001" {
		t.Errorf("参照先がZIPのリンクブロックになっていません: %+v", got[0])
	}
	if got[0].Entry != "図面一式/X008-135-4.dxf" {
		t.Errorf("ZIP内のパスが返っていません: %q", got[0].Entry)
	}
	if got[0].Name != "架台Assy" {
		t.Errorf("図面名称が読めていません: %q", got[0].Name)
	}
}

// TestMatchDXFZipAndLooseTogether は、ページ直下のDXFとZIPの中のDXFが
// **どちらも**拾われることを固定します（片方だけ見る作りに戻さない）。
func TestMatchDXFZipAndLooseTogether(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	putAttachment(t, id, "dxf001.dxf", []byte(dxfWithTitle(t, "X008-135-4", "架台Assy")))
	putAttachment(t, id, "zip001.zip", zipWith(t, map[string]string{
		"X008-135-4.dxf": dxfWithTitle(t, "X008-135-4", "架台Assy"),
	}))

	got := MatchDXFAttachments(id, "X008-135-4")
	if len(got) != 2 {
		t.Fatalf("直下とZIP内の両方が拾えていません: %+v", got)
	}
	var loose, inZip int
	for _, m := range got {
		if m.Entry == "" {
			loose++
		} else {
			inZip++
		}
	}
	if loose != 1 || inZip != 1 {
		t.Errorf("内訳が違います 直下=%d ZIP内=%d", loose, inZip)
	}
}

// TestMatchDXFZipIgnoresOversizedEntry は、申告サイズが上限を超える中身を
// **読まない**ことを固定します（ZIP爆弾への蓋——目録読みだけという原則の例外なので、
// 上限を外すと小さな入力で大量に読まされます）。
func TestMatchDXFZipIgnoresOversizedEntry(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	// 上限を超える大きさのDXF（中身は正しい表題欄）。
	big := dxfWithTitle(t, "X008-135-4", "架台Assy") +
		strings.Repeat("\r\n999\r\nfiller", zipEntryLimit/12+1)
	putAttachment(t, id, "zip001.zip", zipWith(t, map[string]string{"big.dxf": big}))

	if got := MatchDXFAttachments(id, "X008-135-4"); len(got) != 0 {
		t.Errorf("上限を超える中身を読んでいます: %+v", got)
	}
}
