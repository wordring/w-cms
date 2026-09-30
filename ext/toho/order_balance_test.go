package toho

import (
	"testing"

	"w-cms/internal/cms/htmldoc"
)

// balanceTable は受注明細の表を1つ組んで返します。
func balanceTable(t *testing.T, rows string) orderBalance {
	t.Helper()
	nodes, err := htmldoc.ParseFragment(`<table><caption>受注明細</caption><tbody>` +
		`<tr><th>品番</th><th>数量</th><th>単価</th><th>出荷済み</th><th>状態</th></tr>` + rows + `</tbody></table>`)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := orderBalanceOf(nodes[0])
	if !ok {
		t.Fatal("数えられません")
	}
	return b
}

// TestOrderBalance は受注残高の数え方を固定します（2026-09-30 利用者:「受注ページに受注残高を出したい」）。
//
//   - 残数 × 単価の和（出荷済みを引く・出しすぎた行は0）
//   - 完了の行は入れない（受注残表と同じ）
//   - 単価の読めない行は金額に入れない（全部読めないなら 0円——2026-09-30 深夜 利用者:「シンプルに0円で大丈夫です」）
func TestOrderBalance(t *testing.T) {
	b := balanceTable(t,
		`<tr><td>A-1</td><td>20</td><td>1,500</td><td>2</td><td>未着手</td></tr>`+ // 18 × 1500 = 27000
			`<tr><td>A-2</td><td>10</td><td>300</td><td>10</td><td>納品済</td></tr>`+ // 残 0
			`<tr><td>A-3</td><td>5</td><td>100</td><td>7</td><td></td></tr>`+ // 出しすぎ → 0
			`<tr><td>A-4</td><td>4</td><td>999</td><td></td><td>完了</td></tr>`+ // 完了は入れない
			`<tr><td>A-5</td><td>3</td><td></td><td></td><td></td></tr>`) // 単価なし
	if b.Amount != 27000 || b.Open != 2 || b.Rows != 5 || b.NoPrice != 1 || b.Done != 1 {
		t.Fatalf("数え方が違います: %+v", b)
	}
	// 出すのは「受注残高: N円」だけ（2026-09-30 深夜 利用者:「（残のある行…）というのも無くて良いです」）。
	if msg := balanceMessage(b); msg != "受注残高: 27,000円" {
		t.Errorf("文が違います: %s", msg)
	}
	all := balanceTable(t, `<tr><td>A-1</td><td>5</td><td>100</td><td>5</td><td>納品済</td></tr>`)
	if msg := balanceMessage(all); msg != "受注残高: 0円" {
		t.Errorf("全部納めた受注の文: %s", msg)
	}
	none := balanceTable(t, `<tr><td>A-1</td><td>5</td><td></td><td></td><td></td></tr>`)
	if msg := balanceMessage(none); msg != "受注残高: 0円" {
		t.Errorf("単価の無い受注の文: %s", msg)
	}
}
