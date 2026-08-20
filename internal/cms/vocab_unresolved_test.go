package cms

import (
	"reflect"
	"testing"
)

// TestUnresolvedVocabFields は「見出しの改名で③計算プラグインが読めなくなった列」の
// 告知を固定します。data-field（機械キー）を撤去すると鍵は見出しの表示文字だけになるため、
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
			name: "data-field が残っていれば改名しても報告しない",
			html: `<table data-type="part-materials"><tbody>
<tr><th>部材名</th><th data-field="cost">単価（税抜）</th><th>仕入先</th><th>数量</th></tr>
</tbody></table>`,
			want: nil,
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
