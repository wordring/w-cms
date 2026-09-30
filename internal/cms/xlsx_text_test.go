package cms

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildXLSX は最小の .xlsx を組みます（部品をリポジトリへ置かないため）。
func buildXLSX(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

// TestXLSXText は表計算の中身を文字にすることを固定します（2026-09-30——Excel の注文リストを解析に渡す）。
// 文字のセル（共有の表・その場の文字）・数・シートの名前と並び・空のセルを書かないこと。
func TestXLSXText(t *testing.T) {
	b := buildXLSX(t, map[string]string{
		"xl/workbook.xml": `<workbook><sheets><sheet name="注文リスト" sheetId="1"/><sheet name="メモ" sheetId="2"/></sheets></workbook>`,
		"xl/sharedStrings.xml": `<sst><si><t>図面名称</t></si><si><t>今回製作個数</t></si>` +
			`<si><r><t>取付</t></r><r><t>ベース</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData>` +
			`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>` +
			`<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>5</v></c><c r="C2"/></row>` +
			`<row r="3"></row></sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml": `<worksheet><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>塗装ありで</t></is></c></row></sheetData></worksheet>`,
	})
	got, err := XLSXText(b)
	if err != nil {
		t.Fatal(err)
	}
	want := "=== シート 注文リスト\nA=図面名称 | B=今回製作個数\nA=取付ベース | B=5\n=== シート メモ\nA=塗装ありで\n"
	if got != want {
		t.Errorf("文字にした結果が違います:\n%q\n%q", got, want)
	}
	if _, err := XLSXText([]byte("not a zip")); err == nil || !strings.Contains(err.Error(), "表計算") {
		t.Errorf("読めないものに理由を言っていません: %v", err)
	}
}
