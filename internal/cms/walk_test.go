package cms

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

// 配送係（Walker）のテスト。設計の正本は [docs/【考察】パーサとプラグイン.md] §3。
//
// 本文HTMLを処理する3経路（保存時の索引・表示時の計算ビュー・新規作成時の新規化）を
// 1つの回覧機構へ寄せるための土台。ここで固定するのは配送係の約束ごとです:
//
//   - 文書順に、**引き金のある要素だけ**へイベントを配る（全要素への一斉回覧ではない）
//   - `.vocab-chrome` の中は**そもそも歩かない**（除外が「各自が覚える規約」から
//     「観測できない事実」へ変わるのが本設計の最大の利益）
//   - 差し替えた部分木へは配らない（再回覧なし＝停止する）
//   - 引き金が本文に1つも無ければ木を歩かない（早道）

// parseFrag はテスト用にHTML断片を木へ変えます。
func parseFrag(t *testing.T, s string) []*html.Node {
	t.Helper()
	nodes, err := htmldoc.ParseFragment(s)
	if err != nil {
		t.Fatalf("ParseFragmentエラー: %v", err)
	}
	return nodes
}

// collectFunc は訪問した要素の data-type を順に記録するハンドラを作ります。
// ObserveHandlerFunc は関数を ObserveHandler にします（テスト専用のアダプタ。
// 本番の観察係は Register（plugin.go）経由でしか登録されないため、ここに置く）。
type ObserveHandlerFunc func(ctx *ObserveContext, el *html.Node) (bool, error)

func (f ObserveHandlerFunc) OnElement(ctx *ObserveContext, el *html.Node) (bool, error) {
	return f(ctx, el)
}

func collectFunc(seen *[]string, descend bool) ObserveHandlerFunc {
	return func(ctx *ObserveContext, el *html.Node) (bool, error) {
		*seen = append(*seen, Attr(el, "data-type"))
		return descend, nil
	}
}

// TestWalkDeliversOnlyToTriggeredElements は、引き金のある要素にだけ配ること
// （＝ p や h2 では誰も呼ばれないこと）を検証します。
func TestWalkDeliversOnlyToTriggeredElements(t *testing.T) {
	reg := newWalkRegistry()
	var seen []string
	reg.observe("client-order", collectFunc(&seen, true))

	nodes := parseFrag(t, `<h1>題</h1><p>本文</p>`+
		`<section data-type="client-order"><p>中</p></section>`+
		`<table data-type="part-materials"><tr><td>x</td></tr></table>`)

	if err := reg.walkObserve(&ObserveContext{}, nodes); err != nil {
		t.Fatalf("walkObserveエラー: %v", err)
	}
	if len(seen) != 1 || seen[0] != "client-order" {
		t.Errorf("担当の要素だけに配られていません: %v", seen)
	}
}

// TestWalkStarTriggerSeesAllMarkers は trigger "*" が全マーカー要素を受けることを
// 検証します（②汎用索引がこの形で動く。未知の data-type も索引する現行仕様）。
func TestWalkStarTriggerSeesAllMarkers(t *testing.T) {
	reg := newWalkRegistry()
	var seen []string
	reg.observe("*", collectFunc(&seen, true))

	nodes := parseFrag(t, `<p>素の段落</p><table><tr><td>素の表</td></tr></table>`+
		`<dl data-type="tags"><dt>部品番号</dt><dd>X1</dd></dl>`+
		`<table data-type="なぞ"><tr><td>1</td></tr></table>`)

	if err := reg.walkObserve(&ObserveContext{}, nodes); err != nil {
		t.Fatalf("walkObserveエラー: %v", err)
	}
	want := []string{"tags", "なぞ"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("マーカー要素の並びが違います: %v（期待 %v）", seen, want)
	}
}

// TestWalkSkipsChrome は `.vocab-chrome` の中へ配らないことを検証します。
// 計算ビューの中身（サーバーが埋めた表示専用HTML）は本文ではないので、
// 索引にも新規化にも入ってはいけません。
func TestWalkSkipsChrome(t *testing.T) {
	reg := newWalkRegistry()
	var seen []string
	reg.observe("*", collectFunc(&seen, true))

	nodes := parseFrag(t, `<section data-type="required-materials">`+
		`<div class="vocab-chrome"><table data-type="materials-table"><tr><td>1</td></tr></table></div>`+
		`</section>`+
		`<dl data-type="tags"><dt>a</dt><dd>b</dd></dl>`)

	if err := reg.walkObserve(&ObserveContext{}, nodes); err != nil {
		t.Fatalf("walkObserveエラー: %v", err)
	}
	for _, s := range seen {
		if s == "materials-table" {
			t.Errorf("クロームの中へ配られました: %v", seen)
		}
	}
	if len(seen) != 2 || seen[0] != "required-materials" || seen[1] != "tags" {
		t.Errorf("本文側の要素が配られていません: %v", seen)
	}
}

// TestWalkRespectsDescendFalse は、descend=false で子孫へ降りないことを検証します
// （入れ子の明細表を親が丸ごと担当する形。二重に読まないため）。
func TestWalkRespectsDescendFalse(t *testing.T) {
	reg := newWalkRegistry()
	var seen []string
	reg.observe("*", collectFunc(&seen, false))

	nodes := parseFrag(t, `<section data-type="client-order">`+
		`<table data-type="client-order-items"><tr><td>1</td></tr></table>`+
		`</section>`)

	if err := reg.walkObserve(&ObserveContext{}, nodes); err != nil {
		t.Fatalf("walkObserveエラー: %v", err)
	}
	if len(seen) != 1 || seen[0] != "client-order" {
		t.Errorf("子孫へ降りてしまいました: %v", seen)
	}
}

// TestWalkStopsOnError は、ハンドラのエラーで歩行が止まり、原因が伝わることを
// 検証します（観察係のエラーは保存失敗。索引の欠けは「黙って壊れる」の芽）。
func TestWalkStopsOnError(t *testing.T) {
	reg := newWalkRegistry()
	count := 0
	reg.observe("*", ObserveHandlerFunc(func(ctx *ObserveContext, el *html.Node) (bool, error) {
		count++
		return true, errors.New("わざと失敗")
	}))

	nodes := parseFrag(t, `<dl data-type="a"><dt>x</dt></dl><dl data-type="b"><dt>y</dt></dl>`)
	err := reg.walkObserve(&ObserveContext{}, nodes)
	if err == nil {
		t.Fatal("エラーが伝わっていません")
	}
	if count != 1 {
		t.Errorf("最初の失敗で止まっていません: %d回呼ばれました", count)
	}
}

// TestMirrorReplaceStopsRecursion は、差し替えた部分木へイベントを配らないこと
// （再回覧なし＝停止する）を検証します。
func TestMirrorReplaceStopsRecursion(t *testing.T) {
	reg := newWalkRegistry()
	calls := 0
	reg.mirror("view", MirrorHandlerFunc(func(ctx *MirrorContext, el *html.Node) (bool, error) {
		calls++
		// 自分と同じ引き金を持つ部分木で差し替える。再回覧するなら無限に増える。
		repl := parseFrag(t, `<div><section data-type="view"><p>中身</p></section></div>`)
		ctx.Replace(el, repl...)
		return false, nil
	}))

	nodes, err := reg.walkMirror(&MirrorContext{}, parseFrag(t, `<section data-type="view"></section>`))
	if err != nil {
		t.Fatalf("walkMirrorエラー: %v", err)
	}
	if calls != 1 {
		t.Errorf("差し替えた部分木へ配られました: %d回", calls)
	}
	var sb strings.Builder
	for _, n := range nodes {
		html.Render(&sb, n)
	}
	if !strings.Contains(sb.String(), "中身") {
		t.Errorf("差し替えが反映されていません: %s", sb.String())
	}
}

// TestWalkAncestors は、ハンドラが祖先を辿れることを検証します
// （ファイル容器の data-src を親から拾う ClosestFileSrc 相当の用途）。
func TestWalkAncestors(t *testing.T) {
	reg := newWalkRegistry()
	var got []string
	reg.observe("client-order", ObserveHandlerFunc(func(ctx *ObserveContext, el *html.Node) (bool, error) {
		for _, a := range ctx.Ancestors() {
			got = append(got, a.Data+"["+Attr(a, "data-type")+"]")
		}
		return true, nil
	}))

	nodes := parseFrag(t, `<section data-type="file" data-src="a.pdf">`+
		`<section data-type="client-order"></section></section>`)
	if err := reg.walkObserve(&ObserveContext{}, nodes); err != nil {
		t.Fatalf("walkObserveエラー: %v", err)
	}
	if len(got) == 0 || got[len(got)-1] != "section[file]" {
		t.Errorf("直近の祖先が辿れません: %v", got)
	}
}

// TestRegisterMirrorRejectsDuplicate は、鏡型の引き金が一意であることを検証します。
// 「順番」に意味を持たせないための構造的な保証で、重複は起動時に落とします。
func TestRegisterMirrorRejectsDuplicate(t *testing.T) {
	reg := newWalkRegistry()
	noop := MirrorHandlerFunc(func(ctx *MirrorContext, el *html.Node) (bool, error) { return false, nil })
	reg.mirror("view", noop)

	defer func() {
		if recover() == nil {
			t.Error("鏡型の重複登録が通ってしまいました")
		}
	}()
	reg.mirror("view", noop)
}
