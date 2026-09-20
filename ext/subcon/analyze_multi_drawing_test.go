package subcon

import (
	"encoding/json"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// **1つのPDFに複数の図面が入っていたら、1枚につき1ページ作ります**
// （2026-09-20 ユーザー:「一つのPDFに複数の図面が入っている場合もあるようです」
// 「3ページ作って人が整理で1ページにまとめるほうが良いと思います。**一つのPDFに
// 複数の製造製品が入っている場合があるからです**」）。
//
// ⚠ **それまでは1枚ぶんしか作れませんでした**——プロンプトが `drawing_no` を単数で
// 聞いていたので、3枚入ったPDFでも Gemini は1枚ぶんだけ返し、**残りはどこにも
// 記録されません**。エラーも出ないので、気づくのは後から「あの図面どこ？」と
// なったときです。
//
// ⚠ **最初から1ページにまとめてはいけません。** 別々の製造製品だったとき、いまは
// **人が切り離せません**（分ける操作がない）。まとめるのは整理で人が決めます。

// analyzeResponse は解析の応答（複数ページ版）です。
type analyzeResponse struct {
	Success bool `json:"success"`
	DocType string `json:"doc_type"`
	PageID  string `json:"page_id"`
	Pages   []struct {
		PageID string `json:"page_id"`
		Title  string `json:"title"`
	} `json:"pages"`
}

// TestAnalyzeMakesOnePagePerDrawing は、**3枚入ったPDFから3ページできる**ことを
// 固定します。
func TestAnalyzeMakesOnePagePerDrawing(t *testing.T) {
	const id = "000071"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	putAttachment(t, id, "pdf001.pdf", []byte("%PDF-1.4 fake"))
	stubJudge(t, func([]byte) (*orderJudgment, error) {
		return &orderJudgment{DocType: "drawing", Drawings: []drawingJudgment{
			{DrawingNo: "A-1", DrawingName: "ブラケット", MachineName: "装置X", Customer: "客A"},
			{DrawingNo: "A-2", DrawingName: "カバー", MachineName: "装置X", Customer: "客A"},
			{DrawingNo: "A-3", DrawingName: "軸受け", MachineName: "装置X", Customer: "客A"},
		}}, nil
	})

	rr := postAnalyze(t, &auth.User{Username: "alice", IsAdmin: true},
		map[string]string{"page_id": id, "file": "pdf001.pdf"})
	if rr.Code != 200 {
		t.Fatalf("解析が失敗しました: %d %s", rr.Code, rr.Body.String())
	}
	var resp analyzeResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if len(resp.Pages) != 3 {
		t.Fatalf("3ページできていません: %+v", resp)
	}
	// **どれも中身が違う**（同じ図面を3回作っていない）。
	want := map[string]string{"A-1": "ブラケット", "A-2": "カバー", "A-3": "軸受け"}
	for _, pg := range resp.Pages {
		body, err := cms.ReadPageBody(pg.PageID)
		if err != nil {
			t.Fatalf("本文を読めません: %v", err)
		}
		found := ""
		for no, name := range want {
			if strings.Contains(body, "<dd>"+no+"</dd>") {
				if !strings.Contains(body, "<dd>"+name+"</dd>") {
					t.Errorf("図面番号 %s のページに名称 %s がありません:\n%s", no, name, body)
				}
				found = no
			}
		}
		if found == "" {
			t.Errorf("どの図面でもないページができています:\n%s", body)
			continue
		}
		delete(want, found)
	}
	if len(want) != 0 {
		t.Errorf("作られなかった図面があります: %+v", want)
	}
}

// TestAnalyzeKeepsSingleDrawingShape は、**1枚のときは今までどおり**であることを
// 固定します。
//
// ⚠ 画面は `page_id` を読んでいるので、応答の形が変わっただけで表示が壊れては
// いけません。古い形（単数の `drawing_no`）の判定も、これまでどおり1枚として
// 扱えること（Gemini が `drawings` を返さなかったときの受け皿）も見ます。
func TestAnalyzeKeepsSingleDrawingShape(t *testing.T) {
	const id = "000072"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	putAttachment(t, id, "pdf001.pdf", []byte("%PDF-1.4 fake"))
	// **単数の形**（`drawings` は空）で返す。
	stubJudge(t, func([]byte) (*orderJudgment, error) {
		return &orderJudgment{DocType: "drawing", DrawingNo: "B-1",
			DrawingName: "台座", MachineName: "装置Y", Customer: "客B"}, nil
	})

	rr := postAnalyze(t, &auth.User{Username: "alice", IsAdmin: true},
		map[string]string{"page_id": id, "file": "pdf001.pdf"})
	if rr.Code != 200 {
		t.Fatalf("解析が失敗しました: %d %s", rr.Code, rr.Body.String())
	}
	var resp analyzeResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if len(resp.Pages) != 1 {
		t.Fatalf("1ページになっていません: %+v", resp)
	}
	if resp.PageID == "" || resp.PageID != resp.Pages[0].PageID {
		t.Errorf("今までの形（page_id）が返っていません: %+v", resp)
	}
	body, _ := cms.ReadPageBody(resp.PageID)
	if !strings.Contains(body, "<dd>B-1</dd>") || !strings.Contains(body, "<dd>台座</dd>") {
		t.Errorf("中身が入っていません:\n%s", body)
	}
}

// TestAnalyzeRefusesDrawingWithNothingRead は、**図面と判定したのに1枚も取れなかった
// ときに黙って0ページで終わらない**ことを固定します。
//
// ⚠ 成功と返して0ページだと、人は「解析したのに何も出ない」とだけ見えます——
// 何が起きたのか分かりません。
func TestAnalyzeRefusesDrawingWithNothingRead(t *testing.T) {
	const id = "000073"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	putAttachment(t, id, "pdf001.pdf", []byte("%PDF-1.4 fake"))
	stubJudge(t, func([]byte) (*orderJudgment, error) {
		return &orderJudgment{DocType: "drawing"}, nil // 番号も名称も空
	})

	rr := postAnalyze(t, &auth.User{Username: "alice", IsAdmin: true},
		map[string]string{"page_id": id, "file": "pdf001.pdf"})
	if rr.Code == 200 {
		t.Errorf("黙って成功にしています: %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "読み取れませんでした") {
		t.Errorf("理由が書かれていません: %s", rr.Body.String())
	}
}
