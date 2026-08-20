package cms

import (
	"reflect"
	"strings"
	"testing"
)

// TestUnresolvedVocabFields は「見出しの改名で③計算プラグインが読めなくなった列」の
// 告知を固定します。鍵は見出しの表示文字だけなので（機械キーの属性は撤去済み）、
// 改名が**黙って**同期を止めないことがこの告知の存在意義です。
func TestUnresolvedVocabFields(t *testing.T) {
	tests := []struct {
		name string
		html string
		want []string
	}{
		{
			name: "骨格どおりの表は何も報告しない",
			html: `<table data-type="part-materials"><tbody>
<tr><th>部材名</th><th>単価</th><th>仕入先</th><th>数量</th></tr>
<tr><td>丸鋼</td><td>8000</td><td>山田鋼材</td><td>2</td></tr>
</tbody></table>`,
			want: nil,
		},
		{
			name: "見出しの改名を報告する",
			html: `<table data-type="part-materials"><tbody>
<tr><th>部材名</th><th>単価（税抜）</th><th>仕入先</th><th>数量</th></tr>
<tr><td>丸鋼</td><td>8000</td><td>山田鋼材</td><td>2</td></tr>
</tbody></table>`,
			want: []string{"部材定義: 単価"},
		},
		{
			name: "撤去した機械キーの属性が残っていても改名は報告する",
			html: `<table data-type="part-materials"><tbody>
<tr><th>部材名</th><th data-field="cost">単価（税抜）</th><th>仕入先</th><th>数量</th></tr>
</tbody></table>`,
			want: []string{"部材定義: 単価"},
		},
		{
			name: "列を消しただけでは報告しない",
			html: `<table data-type="part-materials"><tbody>
<tr><th>部材名</th><th>仕入先</th><th>数量</th></tr>
</tbody></table>`,
			want: nil,
		},
		{
			name: "独自の列を足しただけでは報告しない",
			html: `<table data-type="part-materials"><tbody>
<tr><th>部材名</th><th>単価</th><th>仕入先</th><th>数量</th><th>備考</th></tr>
</tbody></table>`,
			want: nil,
		},
		{
			name: "業務文書ブロックのヘッダ dl の改名を報告する",
			html: `<section data-type="client-order"><dl>
<dt>発注書番号</dt><dd>PO-A100</dd>
<dt>得意先</dt><dd>トーア</dd>
<dt>発注日</dt><dd>2026-08-20</dd>
</dl></section>`,
			want: []string{"顧客の発注書: 発注元"},
		},
		{
			name: "単独の dl 形式の改名を報告する",
			html: `<dl data-type="our-estimate">
<dt>品番</dt><dd>SHAFT-01</dd>
<dt>お客様</dt><dd>トーア</dd>
<dt>見積金額</dt><dd>120000</dd>
<dt>見積日</dt><dd>2026-08-20</dd>
</dl>`,
			want: []string{"弊社の見積もり: 顧客"},
		},
		{
			name: "機械キーを持たない形式は改名しても報告しない",
			html: `<dl data-type="tags"><dt>担当</dt><dd>山田</dd></dl>
<table data-type="inspection-record"><tbody>
<tr><th>品番</th><th>合否</th><th>検査写真</th><th>検査日</th></tr>
</tbody></table>`,
			want: nil,
		},
		{
			name: "未知の形式は報告しない（レジストリに宣言が無い）",
			html: `<table data-type="なにか"><tbody><tr><th>あ</th><th>い</th></tr></tbody></table>`,
			want: nil,
		},
		{
			name: "同じ改名が複数箇所にあっても1件にまとめる",
			html: `<table data-type="part-materials"><tbody>
<tr><th>部材名</th><th>単価（税抜）</th><th>仕入先</th><th>数量</th></tr></tbody></table>
<table data-type="part-materials"><tbody>
<tr><th>部材名</th><th>単価（税込）</th><th>仕入先</th><th>数量</th></tr></tbody></table>`,
			want: []string{"部材定義: 単価"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UnresolvedVocabFields(tt.html)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UnresolvedVocabFields() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// ── 部品番号タグの改名の告知 ───────────────────────────────────────────
// 部材手配計算は「部品番号」タグ（<dl data-type="tags"> の dt）を鍵に部材定義と
// 受注明細を突き合わせる。この見出しを改名すると part_id が空になり、集計が
// 丸ごと空になるのに告知はゼロだった（設計総点検⑤）。
//
// 「Field を持たない列は改名しても壊れない」という設計判断は、この1点でだけ
// 成り立たない。tags は自由語なので列宣言を足すと**全ページで誤検知**が出る
// （担当者しか書いていないページでも「部品番号が無い」と言い出す）ため、
// 「部材表があるのに部品番号が解決できないとき」に限って告知する。

// TestUnresolvedReportsRenamedPartIDTag は部品番号の改名が告知されることを固定します。
func TestUnresolvedReportsRenamedPartIDTag(t *testing.T) {
	const materials = `<table data-type="part-materials"><tbody>` +
		`<tr><th>部材名</th><th>単価</th><th>仕入先</th><th>数量</th></tr>` +
		`<tr><td>鋼板</td><td>800</td><td>A商事</td><td>2</td></tr></tbody></table>`

	// 改名前: 部品番号があるので告知しない
	ok := `<dl data-type="tags"><dt>部品番号</dt><dd>SHAFT-01</dd></dl>` + materials
	if got := UnresolvedVocabFields(ok); len(got) != 0 {
		t.Errorf("正しい本文で告知が出ました: %v", got)
	}

	// 改名後: 部材表はあるのに部品番号が解決できない → 告知する
	renamed := `<dl data-type="tags"><dt>部品No</dt><dd>SHAFT-01</dd></dl>` + materials
	got := UnresolvedVocabFields(renamed)
	joined := strings.Join(got, " / ")
	if !strings.Contains(joined, "部品番号") {
		t.Errorf("部品番号の改名が告知されません: %v", got)
	}

	// タグごと消した場合も、部材表がある以上は集計に載らないので告知する
	missing := `<dl data-type="tags"><dt>担当者</dt><dd>山田</dd></dl>` + materials
	if got := UnresolvedVocabFields(missing); !strings.Contains(strings.Join(got, " / "), "部品番号") {
		t.Errorf("部品番号の欠落が告知されません: %v", got)
	}
}

// TestUnresolvedDoesNotReportPartIDWithoutMaterials は誤検知が出ないことを固定します。
// tags は自由語なので、部材表の無いページで「部品番号が無い」と言ってはいけない
// （毎ページ鳴る告知は読まれなくなり、本当の改名を隠してしまう）。
func TestUnresolvedDoesNotReportPartIDWithoutMaterials(t *testing.T) {
	cases := map[string]string{
		"自由語のタグだけ":   `<dl data-type="tags"><dt>担当者</dt><dd>山田</dd></dl>`,
		"タグが空":       `<dl data-type="tags"></dl>`,
		"部材表の無い普通の本文": `<h1>ふつうのページ</h1><p>本文</p>`,
		"受注明細はあるが部材表は無い": `<dl data-type="tags"><dt>担当者</dt><dd>山田</dd></dl>` +
			`<table data-type="client-order-items"><tbody><tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>` +
			`<tr><td>A</td><td>B</td><td>1</td><td>1</td><td></td></tr></tbody></table>`,
	}
	for name, in := range cases {
		if got := UnresolvedVocabFields(in); strings.Contains(strings.Join(got, " / "), "部品番号") {
			t.Errorf("%s: 部材表が無いのに部品番号を告知しました: %v", name, got)
		}
	}
}
