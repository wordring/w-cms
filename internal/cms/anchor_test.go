package cms

import (
	"strings"
	"testing"
)

// TestRenderAnchors は、描画時に合成するページ内アンカーの規則を固定します。
// **保存形式は変えず**、ページを返すときだけ id を足す（本文サニタイズ設計 5.3）。
func TestRenderAnchors(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		want       []string
		wantNotHas []string
	}{
		{
			name: "見出しは表示文字がアンカー名になる",
			in:   `<h2 data-id="a7k2m9">発注書A</h2>`,
			want: []string{`id="発注書A"`},
		},
		{
			name: "見出し以外のブロックはブロックIDがアンカー名",
			in:   `<p data-id="a7k2m9">本文</p>`,
			want: []string{`id="a7k2m9"`},
		},
		{
			name:       "書き手が付けた id は上書きしない",
			in:         `<h2 id="mine" data-id="a7k2m9">発注書A</h2>`,
			want:       []string{`id="mine"`},
			wantNotHas: []string{`id="発注書A"`},
		},
		{
			name: "同じ見出しが複数あれば連番",
			in:   `<h2 data-id="a1">発注書A</h2><h2 data-id="a2">発注書A</h2>`,
			want: []string{`id="発注書A"`, `id="発注書A-2"`},
		},
		{
			name: "書き手の id と衝突する合成は避ける",
			in:   `<p id="発注書A" data-id="a1">先に使われている</p><h2 data-id="a2">発注書A</h2>`,
			want: []string{`id="発注書A"`, `id="発注書A-2"`},
		},
		{
			name: "空白は - へ寄せる",
			in:   `<h2 data-id="a1">発注 書  A</h2>`,
			want: []string{`id="発注-書-A"`},
		},
		{
			name: "殻の接頭辞は剥がす（名前空間を侵さない）",
			in:   `<h2 data-id="a1">w-html-preview</h2>`,
			want: []string{`id="html-preview"`},
		},
		{
			name:       "空の見出しには付けない",
			in:         `<h2>   </h2>`,
			wantNotHas: []string{`id=`},
		},
		{
			name:       "ブロックIDも見出しテキストも無ければ付けない",
			in:         `<p>本文</p>`,
			wantNotHas: []string{`id=`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderAnchors(tt.in)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("%q が含まれていません: %s", w, got)
				}
			}
			for _, w := range tt.wantNotHas {
				if strings.Contains(got, w) {
					t.Errorf("%q が含まれています: %s", w, got)
				}
			}
		})
	}
}

// TestRenderAnchorsIsIdempotent は2度通しても結果が変わらないことを確かめます。
// 描画のたびに走るので、合成結果の上へ再度合成しても増殖しないこと。
func TestRenderAnchorsIsIdempotent(t *testing.T) {
	in := `<h2 data-id="a1">発注書A</h2><p data-id="a2">本文</p><h2 data-id="a3">発注書A</h2>`
	once := RenderAnchors(in)
	if twice := RenderAnchors(once); twice != once {
		t.Errorf("冪等でない:\n1回目=%s\n2回目=%s", once, twice)
	}
}
