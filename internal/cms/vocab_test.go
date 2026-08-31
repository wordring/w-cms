package cms

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestVocabRegistryIsWellFormed はレジストリの宣言が規約
// （docs/【考察】語彙モデル.md §9 決定ログ）を満たすことを検証します。
//   - data-type はレジストリ全体で一意・kebab-case
//   - element は table / dl のどちらか
//   - 列は1つ以上・表示ラベル必須・(data-type, 機械キー Field) のセットで一意
//   - enum 列は選択肢を持つ
func TestVocabRegistryIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range VocabDefs() {
		if !kebabCaseRe.MatchString(d.Type) {
			t.Errorf("data-type %q が kebab-case ではありません", d.Type)
		}
		if seen[d.Type] {
			t.Errorf("data-type %q が重複しています", d.Type)
		}
		seen[d.Type] = true

		if d.Element != "table" && d.Element != "dl" && d.Element != "section" {
			t.Errorf("%s: element %q は table / dl / section のいずれかにしてください", d.Type, d.Element)
		}
		if d.DisplayName == "" {
			t.Errorf("%s: 表示名がありません", d.Type)
		}
		// 列は table / dl では必須。section（業務文書ブロック・ファイル容器）は
		// ヘッダ項目を持たない形式（file）もあるため任意。
		if len(d.Columns) == 0 && d.Element != "section" {
			t.Errorf("%s: 列が1つもありません", d.Type)
		}
		// Items はレジストリ内の実在する table 形式を指すこと
		if d.Items != "" {
			if ref, ok := VocabDefByType(d.Items); !ok || ref.Element != "table" {
				t.Errorf("%s: items %q が table 形式として定義されていません", d.Type, d.Items)
			}
		}

		fields := map[string]bool{}
		for _, c := range d.Columns {
			if c.Label == "" {
				t.Errorf("%s: 表示ラベルの無い列があります", d.Type)
			}
			if !validColumnTypes[c.Type] {
				t.Errorf("%s: 列 %q の型 %q が不正です", d.Type, c.Label, c.Type)
			}
			if c.Type == ColEnum && len(c.Enum) == 0 {
				t.Errorf("%s: enum 列 %q に選択肢がありません", d.Type, c.Label)
			}
			if c.Field != "" {
				if !kebabCaseRe.MatchString(c.Field) {
					t.Errorf("%s: 機械キー %q が kebab-case ではありません", d.Type, c.Field)
				}
				if fields[c.Field] {
					t.Errorf("%s: 機械キー %q が形式内で重複しています", d.Type, c.Field)
				}
				fields[c.Field] = true
			}
		}
	}
}

// TestVocabDefsIsSorted は /api/tag-schema の応答が呼び出しごとに変わらないよう、
// 形式定義が Type 順で返ることを検証します。
func TestVocabDefsIsSorted(t *testing.T) {
	defs := VocabDefs()
	for i := 1; i < len(defs); i++ {
		if defs[i-1].Type > defs[i].Type {
			t.Errorf("形式定義がソートされていません: %q の後に %q", defs[i-1].Type, defs[i].Type)
		}
	}
}

// TestInferColumnType は語→型推論辞書の代表例を検証します。
func TestInferColumnType(t *testing.T) {
	cases := []struct {
		label string
		want  ColumnType
	}{
		{"単価", ColNumber},
		{"数量", ColNumber},
		{"納期", ColDate},
		{"検査日", ColDate},
		{"写真", ColImage},
		{"品番", ColText},   // 辞書に無い語は text
		{" 数量 ", ColNumber}, // trim してから引く
	}
	for _, c := range cases {
		if got := InferColumnType(c.label); got != c.want {
			t.Errorf("InferColumnType(%q) = %q, want %q", c.label, got, c.want)
		}
	}
}

// TestNormalizeValue は正規化（併記用）の代表例を検証します。
// 生テキストが正本であり、解釈できない値は ok=false で併記しない（拒否もしない）。
func TestNormalizeValue(t *testing.T) {
	cases := []struct {
		typ  ColumnType
		raw  string
		want string
		ok   bool
	}{
		{ColNumber, "8000", "8000", true},
		{ColNumber, "¥8,000", "8000", true},
		{ColNumber, "８０００円", "8000", true},
		{ColNumber, "12.5", "12.5", true},
		{ColNumber, "-3", "-3", true},
		{ColNumber, "約8000", "", false},
		{ColNumber, "", "", false},
		{ColDate, "2026-08-10", "2026-08-10", true},
		{ColDate, "2026/8/1", "2026-08-01", true},
		{ColDate, "2026年8月1日", "2026-08-01", true},
		{ColDate, "２０２６／０８／１０", "2026-08-10", true},
		{ColDate, "2026-13-01", "", false}, // 実在しない日付
		{ColDate, "来週", "", false},
		{ColText, "8000", "", false},  // text は正規化しない
		{ColEnum, "合格", "", false},   // enum も正規化しない
		{ColImage, "p.jpg", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeValue(c.typ, c.raw)
		if got != c.want || ok != c.ok {
			t.Errorf("NormalizeValue(%q, %q) = (%q, %v), want (%q, %v)", c.typ, c.raw, got, ok, c.want, c.ok)
		}
	}
}

// TestSanitizeKeepsVocabMarkers は data-type が決定ログの許可範囲
// （data-type→table・dl・section・th）で保存されることを検証します。
func TestSanitizeKeepsVocabMarkers(t *testing.T) {
	in := `<table data-type="inspection-record"><tr><th data-type="date">納期</th></tr><tr><td>x</td></tr></table>` +
		`<dl data-type="tags"><dt>希望納期</dt><dd>2026-07-10</dd></dl>`
	out := Sanitize(in)

	for _, want := range []string{
		`<table data-type="inspection-record">`,
		`<th data-type="date">`,
		`<dl data-type="tags">`,
		`<dd>2026-07-10</dd>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("マーカー属性が失われています: %q が見つかりません\nout=%s", want, out)
		}
	}
}

// TestSanitizeDropsDataField は撤去した機械キーの属性が**どの要素からも**除去される
// ことを検証します。かつては th・dd で許可していたが、項目の鍵は見出しの表示文字が
// 運ぶ（語彙モデル §5.1「見える文字がすべて」）ため 2026-08-20 に撤去した。
func TestSanitizeDropsDataField(t *testing.T) {
	in := `<table data-type="part-materials"><tr><th data-field="cost">単価</th></tr></table>` +
		`<dl data-type="tags"><dt>担当</dt><dd data-field="owner">田中</dd></dl>`
	out := Sanitize(in)

	if strings.Contains(out, "data-field") {
		t.Errorf("撤去した data-field が残っています: %s", out)
	}
	// 属性だけが落ち、見える文字（＝鍵と値）は残る
	for _, want := range []string{`<th>単価</th>`, `<dd>田中</dd>`} {
		if !strings.Contains(out, want) {
			t.Errorf("表示文字が失われています: %q / out=%s", want, out)
		}
	}
}

// TestSanitizeDropsVocabMarkersOnOtherElements は許可範囲**外**の要素に付いた
// data-type が除去されることを検証します（「属性は厳格」の維持。
// 全要素共通の例外は data-id のみ）。
func TestSanitizeDropsVocabMarkersOnOtherElements(t *testing.T) {
	// section は論点A採用（2026-08-19）で data-type の許可範囲に入ったため対象外
	in := `<div data-type="x">a</div><td data-field="f">b</td><p data-type="y">c</p><blockquote data-type="z">d</blockquote>`
	out := Sanitize(in)

	if strings.Contains(out, "data-type") {
		t.Errorf("許可範囲外の data-type が残っています: %s", out)
	}
	if strings.Contains(out, "data-field") {
		t.Errorf("許可範囲外の data-field が残っています: %s", out)
	}
	// 要素そのものは残る（属性だけ落ちる）
	if !strings.Contains(out, "<div>a</div>") {
		t.Errorf("div 要素まで落ちています: %s", out)
	}
}

// TestUnknownVocabTypes は未知の data-type の検出（保存時の告知の材料）を検証します。
func TestUnknownVocabTypes(t *testing.T) {
	in := `<table data-type="inspection-record"><tr><th>品番</th></tr></table>` + // レジストリ定義済み
		`<table data-type="mystery-type"><tr><th>x</th></tr></table>` + // 未定義
		`<dl data-type="another-mystery"><dt>a</dt><dd>b</dd></dl>` + // 未定義
		`<table data-type="mystery-type"><tr><th>y</th></tr></table>` + // 重複は1回だけ
		`<table><tr><th>plain</th></tr></table>` // data-type 無しは対象外

	got := UnknownVocabTypes(in)
	want := []string{"another-mystery", "mystery-type"}
	if len(got) != len(want) {
		t.Fatalf("未知の種別の検出結果が違います: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("未知の種別の検出結果が違います: got %v want %v", got, want)
			break
		}
	}
}

// TestTagSchemaIncludesVocab は /api/tag-schema がレジストリの形式定義（vocab）を
// 返すことを検証します。エディタはこれからスラッシュメニューと挿入骨格を生成します。
func TestTagSchemaIncludesVocab(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/tag-schema", nil)
	rr := httptest.NewRecorder()
	TagSchemaAPIHandler(rr, req)

	if rr.Code != 200 {
		t.Fatalf("tag-schema が失敗しました: status=%d", rr.Code)
	}
	var got struct {
		Vocab []VocabDef `json:"vocab"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("JSONの解析に失敗: %v", err)
	}
	if len(got.Vocab) == 0 {
		t.Fatal("vocab が空です（レジストリの形式定義が応答に含まれていません）")
	}
	found := false
	for _, d := range got.Vocab {
		if d.Type == "inspection-record" {
			found = true
			if d.Element != "table" || len(d.Columns) != 4 {
				t.Errorf("検査記録の定義が期待と違います: %+v", d)
			}
		}
	}
	if !found {
		t.Error("サンプル語彙 inspection-record が応答にありません")
	}
}

// TestTagSchemaIncludesTypeInference は /api/tag-schema が語→型の推論辞書を返すことを
// 検証します。エディタは手書きの辞書を持たず、これで型不一致を通知します（語彙モデル §7）。
func TestTagSchemaIncludesTypeInference(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/tag-schema", nil)
	rr := httptest.NewRecorder()
	TagSchemaAPIHandler(rr, req)

	var got struct {
		TypeInference map[string]ColumnType `json:"type_inference"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("JSONの解析に失敗: %v", err)
	}
	if len(got.TypeInference) == 0 {
		t.Fatal("type_inference が空です（推論辞書が応答に含まれていません）")
	}
	// サーバー側の辞書（InferColumnType）と応答が一致すること
	for k, v := range got.TypeInference {
		if InferColumnType(k) != v {
			t.Errorf("推論辞書の不一致: %q → 応答=%s / サーバー=%s", k, v, InferColumnType(k))
		}
	}
	if got.TypeInference["数量"] != ColNumber || got.TypeInference["検査日"] != ColDate {
		t.Errorf("代表語の型が期待と違います: %+v", got.TypeInference)
	}
}

// TestUnknownVocabTypesCoversSection は section のタイポが告知されることを固定します。
//
// 業務文書ブロック（受発注・ファイル容器・計算ビュー）の担い手は section なので、
// ここが告知対象外だと <section data-type="cliet-order"> のような綴り違いが
// 完全に無症状になる——サニタイズは値を検査せず通し、明細表は正しいので画面は
// 普通に見え、それでいて ③計算テーブルからは受注が丸ごと消える。
// 「見える文字がすべて」の代償を告知で補う、という語彙モデルの前提が崩れる箇所。
func TestUnknownVocabTypesCoversSection(t *testing.T) {
	// section だけ綴り違い（明細表は正しい）＝最も自然なタイポの形
	in := `<section data-type="cliet-order"><dl><dt>発注書番号</dt><dd>PO-1</dd></dl>` +
		`<table data-type="client-order-items"><tr><th>品番</th></tr><tr><td>A</td></tr></table></section>`

	got := UnknownVocabTypes(in)
	found := false
	for _, g := range got {
		if g == "cliet-order" {
			found = true
		}
	}
	if !found {
		t.Errorf("section の未知 data-type が告知されません: got %v", got)
	}

	// 定義済みの section は告知しない（誤検知が出ると告知が信用されなくなる）
	if types := UnknownVocabTypes(`<section data-type="client-order"></section>`); len(types) != 0 {
		t.Errorf("定義済みの section を未知として告知しました: %v", types)
	}
	// data-type の無い素の section は対象外（貼り付けた文書の構造を保つため許可されている）
	if types := UnknownVocabTypes(`<section><p>ただの節</p></section>`); len(types) != 0 {
		t.Errorf("素の section を未知として告知しました: %v", types)
	}
}
