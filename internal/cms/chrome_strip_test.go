package cms

import (
	"strings"
	"testing"
)

// 表示専用クロームを本文へ入れない（2026-09-21）。
//
// ⚠ **これは実際の事故の番人です。** `/api/load` の出力を `/api/save` へ書き戻して、
// 移植した6枚の本文に鏡が焼き込まれました。種まきは**そのときの実データの形**です。

// TestSanitizeDropsChromeFromFileView は、⚠ **ファイル表示の節の中身は本文にしない**
// ことを固定します。
//
// ⚠ **`Sanitize` を通すのが肝**です——`StripChrome` を直に呼ぶ試験では、
// **順序（クローム落とし → サニタイズ）が守られているか**を見られません。
// サニタイズが `class` を落としてしまえば、後では二度と見分けられません。
func TestSanitizeDropsChromeFromFileView(t *testing.T) {
	// 実データそのまま（2026-09-21 の事故）。
	body := `<section data-type="file-view" data-ref="000077-9us4">` +
		`<div class="vocab-chrome" contenteditable="false"><div class="file-view">` +
		`<p>📄 <a href="/000077#9us4">図面(印刷)1.pdf</a> を開いています</p></div></div>` +
		`</section>`
	got := Sanitize(body)
	if strings.Contains(got, "を開いています") {
		t.Errorf("⚠ 鏡が本文に残りました:\n%s", got)
	}
	// ⚠ **器そのものは残ること。** 節ごと落とすと、その添付を開く手段が画面から
	//    消えます（配線は `data-ref` にあるので、節が本文です）。
	for _, want := range []string{`data-type="file-view"`, `data-ref="000077-9us4"`} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ 器まで落としています（%q が消えました）:\n%s", want, got)
		}
	}
}

// TestSanitizeDropsChromeInTable は、⚠ **表に足した鏡も落ちる**ことを固定します。
//
// ⚠ **表は入れ子が深く、`<tfoot>` はサニタイズが並べ替えます**——ここが通れば、
// 他の鏡（検算・参考単価の列）も同じ道で落ちます。
func TestSanitizeDropsChromeInTable(t *testing.T) {
	body := `<table data-type="part-materials"><tbody>` +
		`<tr><th>材質</th><th>寸法</th></tr>` +
		`<tr><td>鉄</td><td>t3.2</td></tr>` +
		`</tbody><tfoot class="vocab-chrome"><tr><td colspan="2">` +
		`⚠ 移行の確認前なので、参考単価は引いていません</td></tr></tfoot></table>`
	got := Sanitize(body)
	if strings.Contains(got, "移行の確認前") {
		t.Errorf("⚠ 表の鏡が本文に残りました:\n%s", got)
	}
	// 本文の行は1つも消えないこと。
	for _, want := range []string{"<td>鉄</td>", "<td>t3.2</td>", "<th>材質</th>"} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ 本文の行まで落としています（%q）:\n%s", want, got)
		}
	}
}

// TestSanitizeReportTellsWhenChromeWasDropped は、⚠ **黙って消さない**ことを
// 固定します（消えた理由が誰にも分からなくなるため）。
func TestSanitizeReportTellsWhenChromeWasDropped(t *testing.T) {
	_, changed := SanitizeReport(`<p>本文</p><div class="vocab-chrome">鏡</div>`)
	if !changed {
		t.Error("⚠ クロームを落としたのに「変化なし」と答えています")
	}
	// ⚠ **落とすものが無いときは「変化なし」のまま**であること
	//    ——ここが崩れると、保存のたびにエディタへ嘘の告知が出ます。
	if _, changed := SanitizeReport(`<p>本文</p>`); changed {
		t.Error("⚠ 何も落としていないのに「変化あり」と答えています")
	}
}

// TestStripChromeLeavesCleanBodyByteForByte は、⚠ **印が無ければ1バイトも触らない**
// ことを固定します。
//
// ⚠ パースして描き直す往復は本文をわずかに変えます（属性の順・空要素の書き方）。
// **この関数のせいで変わったのか**が分からなくなるのを避けます。
func TestStripChromeLeavesCleanBodyByteForByte(t *testing.T) {
	for _, body := range []string{
		`<p>ふつうの本文</p>`,
		`<table><tbody><tr><td>鉄</td></tr></tbody></table>`,
		// ⚠ **語が本文のテキストとして出てくる場合**（この文書自身がそうです）。
		`<p>クロームの印は vocab-chrome です</p>`,
		// ⚠ **往復で必ず変わる形を混ぜます。** これが無いと番人は空振りします
		//    ——最初そう書いて、早期戻りを外しても**1本も落ちませんでした**
		//    （往復しても同じ文字列に戻る種ばかりだったため）。
		`<p>vocab-chrome の話<br>と改行</p>`,                         // <br> → <br/>
		`<img src="/a.jpg" alt="vocab-chrome">`,                 // <img> → <img/>
		`<p>vocab-chrome</p><table><tr><td>a</td></tr></table>`, // tbody が挿さる
	} {
		got, dropped := StripChrome(body)
		if dropped != 0 || got != body {
			t.Errorf("触らないはずが変わりました（落とした数 %d）:\n元: %s\n後: %s",
				dropped, body, got)
		}
	}
}

// TestStripChromeDropsNestedChrome は、⚠ **入れ子の中の鏡も落ちる**ことを
// 固定します。
//
// ⚠ **`DropChrome`（直下だけ）との違いがここです。** 事故で焼き込まれたのは
// **節の中**にある鏡でした——直下しか見ない実装では1つも落ちません。
func TestStripChromeDropsNestedChrome(t *testing.T) {
	body := `<section><div><p>本文</p>` +
		`<span class="vocab-chrome">深いところの鏡</span></div></section>`
	got, dropped := StripChrome(body)
	if dropped != 1 {
		t.Errorf("落とした数が %d です（1のはず）: %s", dropped, got)
	}
	if strings.Contains(got, "深いところの鏡") {
		t.Errorf("⚠ 入れ子の鏡が残りました:\n%s", got)
	}
	if !strings.Contains(got, "<p>本文</p>") {
		t.Errorf("⚠ 本文まで落としています:\n%s", got)
	}
}

// TestStripChromeDropsTopLevelChrome は、⚠ **いちばん外側の鏡も落ちる**ことを
// 固定します（子孫だけを見る実装では、根に居るものが残ります）。
func TestStripChromeDropsTopLevelChrome(t *testing.T) {
	got, dropped := StripChrome(`<div class="vocab-chrome">鏡</div><p>本文</p>`)
	if dropped != 1 || strings.Contains(got, "鏡") {
		t.Errorf("⚠ 最上位の鏡が残りました（落とした数 %d）:\n%s", dropped, got)
	}
	if !strings.Contains(got, "<p>本文</p>") {
		t.Errorf("⚠ 本文まで落としています:\n%s", got)
	}
}

// TestStripChromeMatchesWalkersView は、⚠ **落とす判定が配送係と同じ**ことを
// 固定します。
//
// ⚠ **2つの判定がずれると、「配送係は歩かないのに本文には残る」**という、
// いちばん気づきにくい形になります——索引には入らないのに紙には出ます。
func TestStripChromeMatchesWalkersView(t *testing.T) {
	// `class` に他の語が並んでいても鏡（`isChrome` は含有で見る）。
	//
	// ⚠ **セルは表の中に置きます。** `<td>` を単独でパースすると**パーサが黙って
	//    捨てる**ので、「落とせた」のか「最初から無かった」のか見分けが付きません
	//    （最初そう書いて、番人が空振りしました）。
	body := `<table><tbody><tr><td>鉄</td>` +
		`<td class="vocab-chrome mat-price">860円</td></tr></tbody></table>`
	got, dropped := StripChrome(body)
	if dropped != 1 || strings.Contains(got, "860円") {
		t.Errorf("⚠ 複合クラスの鏡を見落としました（落とした数 %d）:\n%s", dropped, got)
	}
	if !strings.Contains(got, "<td>鉄</td>") {
		t.Errorf("⚠ 隣の本文のセルまで落としています:\n%s", got)
	}
}
