package cms

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestXLSXTextBombs は、小さな .xlsx で CPU とメモリを占められないことを固定します（2026-10-27 の見直し）。
//   - 同じシートのファイルを指す並びを数千入れても、そのファイルは1回だけ読む。
//   - 長い共有文字列を指すセルを1行に数千並べても、出力の上限で止まる（行の終わりまで組まない）。
func TestXLSXTextBombs(t *testing.T) {
	var sheetsXML strings.Builder
	for i := 0; i < 3000; i++ {
		sheetsXML.WriteString(`<sheet name="s" r:id="rId1"/>`)
	}
	var row strings.Builder
	for i := 0; i < 5000; i++ {
		row.WriteString(`<c r="A1" t="s"><v>0</v></c>`)
	}
	var big bytes.Buffer
	zw := zip.NewWriter(&big)
	add := func(name, body string) {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	add("xl/workbook.xml", `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`+sheetsXML.String()+`</sheets></workbook>`)
	add("xl/_rels/workbook.xml.rels", `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`)
	add("xl/sharedStrings.xml", `<sst><si><t>`+strings.Repeat("あ", 300000)+`</t></si></sst>`)
	add("xl/worksheets/sheet1.xml", `<worksheet><sheetData><row>`+row.String()+`</row></sheetData></worksheet>`)
	zw.Close()

	start := time.Now()
	out, err := XLSXText(big.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("⚠ 読むのに %v かかりました", d)
	}
	if len(out) > 4*xlsxTextLimit {
		t.Errorf("⚠ 出力が上限を大きく超えました（%d バイト）", len(out))
	}
	if strings.Count(out, "=== シート") != 1 {
		t.Errorf("⚠ 同じファイルを指すシートを何度も読みました（%d 回）", strings.Count(out, "=== シート"))
	}
}
