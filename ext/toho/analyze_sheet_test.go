package toho

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// TestAnalyzeSheetCreatesOrderPage は、表計算（.xlsx）の注文リストを解析すると、**文字にして判定へ渡し**、受注ページが
// できることを固定します（2026-09-30 利用者:「ハッキリと受注したとわかる場合は、受注ページにして良いと思います」）。
func TestAnalyzeSheetCreatesOrderPage(t *testing.T) {
	const id = "000014"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"xl/workbook.xml":      `<workbook><sheets><sheet name="注文リスト" sheetId="1"/></sheets></workbook>`,
		"xl/sharedStrings.xml": `<sst><si><t>図面番号</t></si><si><t>今回製作個数</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>` +
			`<row r="2"><c r="A2" t="inlineStr"><is><t>K120-3</t></is></c><c r="B2"><v>5</v></c></row></sheetData></worksheet>`,
	} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	putAttachment(t, id, "list.xlsx", buf.Bytes())

	var got string
	orig := judgeOrderSheet
	judgeOrderSheet = func(text string) (*orderJudgment, error) {
		got = text
		return &orderJudgment{IsClientOrder: true, DocType: "order", Customer: "南北スポーツ", OrderDate: "2024-09-19",
			Items: []orderPDFItem{{ItemNo: "K120-3", ItemName: "取付ベース", Quantity: "5"}}}, nil
	}
	t.Cleanup(func() { judgeOrderSheet = orig })
	stubJudge(t, func([]byte) (*orderJudgment, error) {
		t.Error("表計算を PDF の判定へ回しています")
		return nil, nil
	})

	rr := postAnalyze(t, &auth.User{Username: "alice"}, map[string]string{"page_id": id, "file": "list.xlsx"})
	if rr.Code != 200 {
		t.Fatalf("解析が失敗しました: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(got, "A=K120-3 | B=5") || !strings.Contains(got, "=== シート 注文リスト") {
		t.Errorf("表計算を文字にして渡していません: %q", got)
	}
	var res struct {
		IsClientOrder bool   `json:"is_client_order"`
		PageID        string `json:"page_id"`
	}
	json.Unmarshal(rr.Body.Bytes(), &res)
	if !res.IsClientOrder || res.PageID == "" {
		t.Fatalf("受注ページができていません: %s", rr.Body.String())
	}
	if body := readPageBody(t, res.PageID); !strings.Contains(body, "<td>K120-3</td>") || !strings.Contains(body, id+"-list") {
		t.Errorf("受注ページの明細・原本の参照が違います:\n%s", body)
	}
}
