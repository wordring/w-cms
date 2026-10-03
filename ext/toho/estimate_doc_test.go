package toho

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 顧客へ出す見積書（2026-10-01・estimate_doc.go・estimate_pdf.go・estimate_mail.go）。
// 利用者:「顧客へので良いです」→「見積計算表から」・宛名は「会社＋担当者」。紙は利用者の見本（御見積書）の欄。

func postEstimateAdd(t *testing.T, u *auth.User, req map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(req)
	r := httptest.NewRequest("POST", "/api/estimate/add", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r = auth.WithUser(r, u)
	rr := httptest.NewRecorder()
	AddToEstimateAPIHandler(rr, r)
	var got map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if rr.Code != 200 {
		t.Fatalf("見積書に入れられません: %d %s", rr.Code, rr.Body.String())
	}
	return got
}

// seedEstimateProduct は見積計算表を持つ加工製品ページ（客先 南北スポーツ・品番 K-1・品名 カバー）を置きます。
func seedEstimateProduct(t *testing.T, id string) {
	t.Helper()
	seedBody(t, id, `<h1>カバー</h1><dl data-type="tags"><dt>品番</dt><dd>K-1</dd><dt>品名</dt><dd>カバー</dd>`+
		`<dt>客先</dt><dd>南北スポーツ</dd></dl>`+
		estimateTable([3]string{"ロット", "20", "個"}, [3]string{"材料", "55", "円"}, [3]string{"板金", "310", "円"})+
		`<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>数</th><th>単位</th><th>備考</th></tr>`+
		`<tr><td>ロット</td><td>20</td><td>個</td><td></td></tr>`+
		`<tr><td>単価</td><td>500</td><td>円</td><td>塗装あり</td></tr></tbody></table>`)
}

// TestEstimateFromCalcTable は、見積計算表から新しい見積書を作り（見積／年／月・タグの既定値）、2枚目の表を同じ見積書へ
// 足せることを固定します——単価は確定単価、数量はロット、備考は単価の行の備考。
func TestEstimateFromCalcTable(t *testing.T) {
	const product = "000021"
	setupExtTest(t, product, page.PageMeta{Owner: "root", Mode: "330"})
	seedEstimateProduct(t, product)
	root := &auth.User{Username: "root", IsAdmin: true}

	got := postEstimateAdd(t, root, map[string]any{"product": product, "index": 0, "client": "南北スポーツ", "person": "潮田"})
	est, _ := got["page_id"].(string)
	if got["new"] != true || est == "" {
		t.Fatalf("新しい見積書ができていません: %v", got)
	}
	body := readPageBody(t, est)
	for _, want := range []string{
		"<h1>見積　南北スポーツ</h1>",
		"<dt>" + EstimateNoTag + "</dt><dd>" + est + "</dd>",
		"<dt>" + EstimateClientTag + "</dt><dd>南北スポーツ</dd>",
		"<dt>" + EstimatePersonTag + "</dt><dd>潮田</dd>",
		"<dt>" + EstimateDateTag + "</dt><dd>" + time.Now().Format("2006-01-02") + "</dd>",
		"<dt>" + EstimateTradeTag + "</dt><dd>従来通り</dd>",
		"<dt>" + EstimateValidTag + "</dt><dd>1カ月</dd>",
		// 単価は確定単価（365 × 1.1 = 401.5 → 利益 37・確定 402）、数量はロット、備考は単価の行の備考（空）。
		"<td>" + product + "</td><td>K-1</td><td>カバー</td><td>20</td><td>個</td><td>402</td>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("見積書に %q がありません:\n%s", want, body)
		}
	}
	// 置き場は 見積／年／月。
	meta, _ := page.ReadSidecar(est)
	month := cms.PageTitleByID(pageNum(meta.ParentID))
	if month != time.Now().Format("01")+"月" {
		t.Errorf("見積書の置き場が年月のフォルダではありません: 親の題 %q", month)
	}

	// 2枚目の表（単価 500・塗装あり）を同じ見積書へ。
	got = postEstimateAdd(t, root, map[string]any{"product": product, "index": 1, "into": est})
	if got["new"] != false || got["page_id"] != est {
		t.Fatalf("作りかけの見積書へ足していません: %v", got)
	}
	body = readPageBody(t, est)
	if !strings.Contains(body, "<td>K-1</td><td>カバー</td><td>20</td><td>個</td><td>550</td><td>塗装あり</td>") ||
		strings.Count(body, "<td>カバー</td>") != 2 {
		t.Errorf("2行目（550円・塗装あり）が足されていません:\n%s", body)
	}
	if list := estimatesFor(root, "南北スポーツ", 5); len(list) != 1 || page.FormatID(list[0].ID) != est {
		t.Errorf("客先の見積書が選ぶ欄に出ません: %+v", list)
	}

	// メールの初期値——件名に見積番号・宛先は連絡帳に無いので空と言う。
	d, err := estimateMailDefaults(root, est)
	if err != nil || !strings.Contains(d.Subject, "御見積書（№ "+est+"）") || !strings.Contains(d.Body, "潮田 様") {
		t.Errorf("メールの初期値が違います: %v %+v", err, d)
	}
}

// TestEstimatePDFRoundTrips は見積書の紙に見本の欄が刷られることを、読み返して確かめます。
func TestEstimatePDFRoundTrips(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	body := `<h1>見積　みなと商店</h1>` +
		`<dl data-type="tags"><dt>見積番号</dt><dd>000300</dd><dt>見積先</dt><dd>みなと商店</dd><dt>見積先担当</dt><dd>山川</dd>` +
		`<dt>見積日</dt><dd>2026-10-01</dd><dt>受渡期日</dt><dd>受注後2週間</dd><dt>受渡場所</dt><dd>貴社</dd>` +
		`<dt>取引方法</dt><dd>従来通り</dd><dt>有効期限</dt><dd>1カ月</dd></dl>` +
		`<table><caption>見積明細</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>単価</th><th>備考</th></tr>` +
		`<tr><td>000080</td><td>A100-B01-001</td><td>取付ブラケット</td><td>20</td><td>個</td><td>402</td><td>塗装無し</td></tr>` +
		`</tbody></table><section><h2>備考</h2><p>レーザー加工穴でのお見積もりとなります。</p></section>`
	pdf, err := buildEstimatePDF(body, nil)
	if err != nil {
		t.Fatalf("PDFを作れません: %v", err)
	}
	got := pdfTextOf(t, pdf)
	for _, want := range []string{
		"2026年10月1日", "000300", "みなと商店", "山川", "様", "下記のとおり御見積申し上げます",
		"受注後2週間", "貴社", "従来通り", "1カ月", "税率", "取付ブラケット", "A100-B01-001", "塗装無し",
		"レーザー加工穴でのお見積もりとなります。",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ PDFに %q が入っていません。読み返した中身:\n%s", want, got)
		}
	}
	// 金額は明細と合計で2回（402 × 20 = 8,040）。
	if n := strings.Count(got, "8,040"); n != 2 {
		t.Errorf("⚠ 金額 8,040 が %d 回です（明細と合計で2回）:\n%s", n, got)
	}
}

// TestEstimateFromFolder は「見積書を作る」（装置フォルダから選ぶ・2026-10-03・estimate_folder.go）を固定します——
// ① 装置フォルダの下の加工製品が見積計算表ごとに並ぶ（確定単価・ロット・確定単価の出ない表は理由・表の無い加工製品も並ぶ）
// ② 送った表の順に見積明細が入った見積書ページができる（単価は確定単価・備考は単価の行の備考）
// ③ 選んだ表の1つでも確定単価が出なければ作らない（顧客へ出す紙が黙って欠けないように）
func TestEstimateFromFolder(t *testing.T) {
	const folder = "000030"
	setupExtTest(t, folder, page.PageMeta{Owner: "root", Mode: "330"})
	seedBody(t, folder, `<h1>標準2輪</h1><dl data-type="tags"><dt>`+ClientNameTag+`</dt><dd>みなと商店</dd></dl>`)
	mk := func(id, body string) {
		t.Helper()
		if err := page.WriteSidecar(id, page.PageMeta{Owner: "root", Mode: "330", ParentID: folder}); err != nil {
			t.Fatal(err)
		}
		seedBody(t, id, body)
	}
	tag := func(no string) string { return `<dl data-type="tags"><dt>品番</dt><dd>` + no + `</dd></dl>` }
	mk("000031", `<h1>取付ベース</h1>`+tag("A100-B01-1")+
		estimateTable([3]string{"ロット", "20", "個"}, [3]string{"材料", "55", "円"}, [3]string{"板金", "310", "円"})+
		`<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>数</th><th>単位</th><th>備考</th></tr>`+
		`<tr><td>ロット</td><td>40</td><td>個</td><td></td></tr>`+
		`<tr><td>単価</td><td>500</td><td>円</td><td>塗装あり</td></tr></tbody></table>`)
	mk("000032", `<h1>ステー</h1>`+tag("A100-B01-2")+ // 列にロットを並べた形——計算できない
		`<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>ロット20</th><th>ロット40</th></tr>`+
		`<tr><td>単価</td><td>500</td><td>450</td></tr></tbody></table>`)
	mk("000033", `<h1>補強板</h1>`+tag("A100-B01-3"))
	root := &auth.User{Username: "root", IsAdmin: true}

	// ①
	req := auth.WithUser(httptest.NewRequest("GET", "/api/estimate/folder-products?folder="+folder, nil), root)
	rr := httptest.NewRecorder()
	EstimateFolderProductsAPIHandler(rr, req)
	var listed struct {
		Client   string `json:"client"`
		Products []struct {
			ID     string `json:"id"`
			Tables []struct {
				Index           int
				Quantity, Price string
				Note, Why       string
			} `json:"tables"`
		} `json:"products"`
	}
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &listed) != nil {
		t.Fatalf("一覧が出ません: %d %s", rr.Code, rr.Body.String())
	}
	var got []string
	for _, p := range listed.Products {
		s := p.ID + ":"
		for _, tb := range p.Tables {
			if tb.Why != "" {
				s += "[理由あり]"
			} else {
				s += "[" + tb.Quantity + "個" + tb.Price + "円" + tb.Note + "]"
			}
		}
		got = append(got, s)
	}
	if strings.Join(got, " ") != "000032:[理由あり] 000031:[20個402円][40個550円塗装あり] 000033:" || listed.Client != "みなと商店" {
		t.Errorf("一覧が違います: %v 見積先 %q", got, listed.Client)
	}

	post := func(body map[string]any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		r := auth.WithUser(httptest.NewRequest("POST", "/api/estimate/from-folder", bytes.NewReader(b)), root)
		r.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		EstimateFromFolderAPIHandler(rr, r)
		var out map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, out
	}
	// ② 送った順（40個の表 → 20個の表——画面は一覧の順で送る）。
	code, out := post(map[string]any{"client": "みなと商店", "items": []map[string]any{
		{"product": "000031", "index": 1}, {"product": "000031", "index": 0}}})
	est, _ := out["page_id"].(string)
	if code != 200 || est == "" {
		t.Fatalf("見積書を作れません: %d %v", code, out)
	}
	body := readPageBody(t, est)
	first := strings.Index(body, "<td>000031</td><td>A100-B01-1</td><td>取付ベース</td><td>40</td><td>個</td><td>550</td><td>塗装あり</td>")
	second := strings.Index(body, "<td>000031</td><td>A100-B01-1</td><td>取付ベース</td><td>20</td><td>個</td><td>402</td>")
	if first < 0 || second < 0 || first > second {
		t.Errorf("見積明細が送った順に入っていません:\n%s", body)
	}
	if !strings.Contains(body, "<dt>"+EstimateClientTag+"</dt><dd>みなと商店</dd>") {
		t.Errorf("見積先が入っていません:\n%s", body)
	}
	// ③
	if code, out := post(map[string]any{"items": []map[string]any{{"product": "000031", "index": 0}, {"product": "000032", "index": 0}}}); code != 409 ||
		!strings.Contains(out["message"].(string), "000032") {
		t.Errorf("確定単価の出ない表を選んだのに %d %v", code, out)
	}
}
