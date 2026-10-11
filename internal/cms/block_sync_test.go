package cms

import (
	"strings"
	"testing"
)

// ブロック単位の保護の試験（2026-10-12・【考察】編集モードの再考 §6「試験の約束」）。
// 利用者:「込み入ってるのでテストは十分にお願いします」。
//
// ここは**純粋な部品**——セルごとの合わせ方（mergeBlock3）と、画面の変更をいまの本文へ当てる計画（planSync）。
// 口（/api/sync）を通した試験・同時に書く試験は sync_handler_test.go。

// ── mergeBlock3（表は違うセルなら黙って合わせる——問い4「エ」）──────────────────

const tbl = `<table data-id="t1"><caption>部品表</caption><tbody>` +
	`<tr><th>品名</th><th>数量</th></tr>` +
	`<tr><td>A</td><td>1</td></tr>` +
	`<tr><td>B</td><td>2</td></tr>` +
	`</tbody></table>`

func TestMergeBlock3TableDifferentCells(t *testing.T) {
	mine := strings.Replace(tbl, "<td>1</td>", "<td>10</td>", 1)   // 自分は A の数量
	theirs := strings.Replace(tbl, "<td>B</td>", "<td>B2</td>", 1) // 相手は B の品名
	got, ok := mergeBlock3(tbl, mine, theirs)
	if !ok {
		t.Fatalf("違うセルなのに合わせられなかった")
	}
	if !strings.Contains(got, "<td>10</td>") || !strings.Contains(got, "<td>B2</td>") {
		t.Fatalf("両方の変更が入っていない: %s", got)
	}
	if strings.Contains(got, "<td>1</td>") || strings.Contains(got, "<td>B</td>") {
		t.Fatalf("古い値が残っている: %s", got)
	}
}

func TestMergeBlock3TableSameCellDifferent(t *testing.T) {
	mine := strings.Replace(tbl, "<td>1</td>", "<td>10</td>", 1)
	theirs := strings.Replace(tbl, "<td>1</td>", "<td>20</td>", 1)
	if got, ok := mergeBlock3(tbl, mine, theirs); ok {
		t.Fatalf("同じセルを違う値にしたのに黙って合わせた: %s", got)
	}
}

func TestMergeBlock3TableSameCellSameValue(t *testing.T) {
	mine := strings.Replace(tbl, "<td>1</td>", "<td>10</td>", 1)
	got, ok := mergeBlock3(tbl, mine, mine)
	if !ok || !strings.Contains(got, "<td>10</td>") {
		t.Fatalf("同じ値に変えたのに合わせられなかった: ok=%v %s", ok, got)
	}
}

func TestMergeBlock3TableRowAdded(t *testing.T) {
	mine := strings.Replace(tbl, "</tbody>", "<tr><td>C</td><td>3</td></tr></tbody>", 1) // 自分は行を足した
	theirs := strings.Replace(tbl, "<td>B</td>", "<td>B2</td>", 1)
	if got, ok := mergeBlock3(tbl, mine, theirs); ok {
		t.Fatalf("行を足したのに黙って合わせた（並べるはず）: %s", got)
	}
	// 相手だけが行を足し、自分は何も変えていない（まれ——自分の書き出しが元と同じ）なら相手のまま。
	theirsRow := strings.Replace(tbl, "</tbody>", "<tr><td>C</td><td>3</td></tr></tbody>", 1)
	got, ok := mergeBlock3(tbl, tbl, theirsRow)
	if !ok || !strings.Contains(got, "<td>C</td>") {
		t.Fatalf("自分が変えていないのに相手の行が入らない: ok=%v %s", ok, got)
	}
}

func TestMergeBlock3SkeletonAndCell(t *testing.T) {
	mine := strings.Replace(tbl, "部品表", "部品表（改）", 1)             // 自分はキャプション
	theirs := strings.Replace(tbl, "<td>2</td>", "<td>5</td>", 1) // 相手はセル
	got, ok := mergeBlock3(tbl, mine, theirs)
	if !ok || !strings.Contains(got, "部品表（改）") || !strings.Contains(got, "<td>5</td>") {
		t.Fatalf("キャプションとセルが合わない: ok=%v %s", ok, got)
	}
	// 両方がキャプションを違うように変えたら合わせない。
	theirs2 := strings.Replace(tbl, "部品表", "部材表", 1)
	if _, ok := mergeBlock3(tbl, mine, theirs2); ok {
		t.Fatalf("両方がキャプションを変えたのに合わせた")
	}
}

func TestMergeBlock3Paragraph(t *testing.T) {
	base := `<p data-id="p1">はじめ</p>`
	if _, ok := mergeBlock3(base, `<p data-id="p1">自分</p>`, `<p data-id="p1">相手</p>`); ok {
		t.Fatalf("段落を両方が変えたのに合わせた")
	}
	got, ok := mergeBlock3(base, base, `<p data-id="p1">相手</p>`)
	if !ok || !strings.Contains(got, "相手") {
		t.Fatalf("自分が変えていない段落で相手の版にならない: %v %s", ok, got)
	}
}

// 画面の書き出しは字下げ・属性の順がサーバーと違う——同じ中身なら同じに扱う（でないと、触っていない
// セルまで「相手が変えた」に見えて、いつもぶつかる）。
func TestMergeBlock3FormattingDifferences(t *testing.T) {
	base := "<table data-id=\"t1\">\n    <tbody>\n        <tr><td>A</td><td>1</td></tr>\n        <tr><td>B</td><td>2</td></tr>\n    </tbody>\n</table>"
	mine := strings.Replace(base, "<td>1</td>", "<td>10</td>", 1)
	theirs := `<table data-id="t1"><tbody><tr><td>A</td><td>1</td></tr><tr><td>B2</td><td>2</td></tr></tbody></table>`
	got, ok := mergeBlock3(base, mine, theirs)
	if !ok || !strings.Contains(got, "<td>10</td>") || !strings.Contains(got, "<td>B2</td>") {
		t.Fatalf("字下げの違いでぶつかった: ok=%v %s", ok, got)
	}
}

func TestCanonBlockAttrOrder(t *testing.T) {
	a := canonBlock(`<table data-id="t1" data-type="x"><tbody><tr><td>1</td></tr></tbody></table>`)
	b := canonBlock(`<table data-type="x" data-id="t1"><tbody><tr><td>1</td></tr></tbody></table>`)
	if a != b {
		t.Fatalf("属性の順で違う形になった:\n%s\n%s", a, b)
	}
}

// ── splitStoredBlocks・ensureStoredBlockIDs ─────────────────────────────

func TestEnsureStoredBlockIDs(t *testing.T) {
	blocks, err := splitStoredBlocks(`<h1>題</h1><p data-id="aaaa">1</p><p data-id="aaaa">2</p><p>3</p>`)
	if err != nil {
		t.Fatal(err)
	}
	if n := ensureStoredBlockIDs(blocks); n != 3 {
		t.Fatalf("振った数=%d（h1・重複の2つ目・p の3つのはず）", n)
	}
	seen := map[string]bool{}
	for _, b := range blocks {
		if b.ID == "" || seen[b.ID] {
			t.Fatalf("ID が空か重なっている: %+v", blocks)
		}
		seen[b.ID] = true
		if !strings.Contains(b.HTML, `data-id="`+b.ID+`"`) {
			t.Fatalf("書き出しに ID が入っていない: %s", b.HTML)
		}
		if b.Hash != blockHash(b.HTML) {
			t.Fatalf("指紋が書き出しと合わない")
		}
	}
	if blocks[1].ID != "aaaa" {
		t.Fatalf("1つ目の aaaa を振り直した（参照が切れる）: %s", blocks[1].ID)
	}
}

// 本文へ戻して読み直しても指紋が変わらない（書き直しのたびに「相手が変えた」にならない）。
func TestStoredBlocksRoundTrip(t *testing.T) {
	blocks, _ := splitStoredBlocks(`<h1 data-id="h">題</h1>` + "\n\n" + tbl + `<p data-id="p">x &amp; y</p>`)
	again, _ := splitStoredBlocks(joinStoredBlocks(blocks))
	if len(again) != len(blocks) {
		t.Fatalf("数が変わった")
	}
	for i := range blocks {
		if blocks[i].Hash != again[i].Hash {
			t.Fatalf("読み直すと指紋が変わる: %s / %s", blocks[i].HTML, again[i].HTML)
		}
	}
}

// ── planSync（画面の変更をいまの本文へ当てる）─────────────────────────────

// pb は試験用のブロックを作ります。
func pb(id, text string) storedBlock {
	h := `<p data-id="` + id + `">` + text + `</p>`
	return storedBlock{ID: id, HTML: h, Hash: blockHash(h)}
}

// syncCase は「元の本文」「いまの本文（相手の変更のあと）」「画面の並びと変更」から計画を立てます。
type syncCase struct {
	base    []storedBlock // 画面が書き始めたとき
	current []storedBlock // いまの正本
	order   []string      // 画面のいまの並び
	changes map[string]string
}

func (c syncCase) plan() syncPlan {
	req := SyncRequest{Order: c.order}
	baseHTML := map[string]string{}
	for _, b := range c.base {
		req.Base = append(req.Base, SyncBase{ID: b.ID, H: b.Hash})
		baseHTML[b.ID] = b.HTML
	}
	mine := map[string]storedBlock{}
	baseH := map[string]string{}
	for _, b := range c.base {
		baseH[b.ID] = b.Hash
	}
	for _, id := range c.order {
		text, ok := c.changes[id]
		if !ok {
			continue
		}
		var m storedBlock
		if strings.HasPrefix(text, "<") {
			m, _ = storedFormOf(text, id)
		} else {
			m = pb(id, text)
		}
		mine[id] = m
		req.Changes = append(req.Changes, SyncChange{ID: id, BaseH: baseH[id], BaseHTML: baseHTML[id], HTML: m.HTML})
	}
	return planSync(c.current, req, mine, map[string]bool{})
}

func finalText(p syncPlan) string {
	var parts []string
	for _, b := range p.final {
		s := b.HTML
		s = s[strings.Index(s, ">")+1:]
		if i := strings.LastIndex(s, "<"); i >= 0 {
			s = s[:i]
		}
		parts = append(parts, b.ID+"="+s)
	}
	return strings.Join(parts, " ")
}

func conflictKinds(p syncPlan) string {
	var parts []string
	for _, c := range p.conflicts {
		parts = append(parts, c.ID+":"+c.Kind)
	}
	return strings.Join(parts, " ")
}

func TestPlanSync(t *testing.T) {
	a, b, c := pb("a", "A"), pb("b", "B"), pb("c", "C")
	base := []storedBlock{a, b, c}
	tests := []struct {
		name      string
		sc        syncCase
		final     string
		conflicts string
		kept      string
		dirty     bool
	}{
		{
			name:  "何も変えていない——書かない",
			sc:    syncCase{base: base, current: base, order: []string{"a", "b", "c"}},
			final: "a=A b=B c=C",
		},
		{
			name:  "自分だけが変えた",
			sc:    syncCase{base: base, current: base, order: []string{"a", "b", "c"}, changes: map[string]string{"b": "B自分"}},
			final: "a=A b=B自分 c=C", dirty: true,
		},
		{
			name:  "違うブロックを自分と相手が変えた——両方残る",
			sc:    syncCase{base: base, current: []storedBlock{pb("a", "A相手"), b, c}, order: []string{"a", "b", "c"}, changes: map[string]string{"b": "B自分"}},
			final: "a=A相手 b=B自分 c=C", dirty: true,
		},
		{
			name:      "同じブロックを違うように変えた——ぶつかる（本文は相手の版のまま）",
			sc:        syncCase{base: base, current: []storedBlock{a, pb("b", "B相手"), c}, order: []string{"a", "b", "c"}, changes: map[string]string{"b": "B自分"}},
			final:     "a=A b=B相手 c=C",
			conflicts: "b:changed",
		},
		{
			name:  "同じブロックを同じに変えた——ぶつからない",
			sc:    syncCase{base: base, current: []storedBlock{a, pb("b", "B同じ"), c}, order: []string{"a", "b", "c"}, changes: map[string]string{"b": "B同じ"}},
			final: "a=A b=B同じ c=C",
		},
		{
			name:      "相手が消したブロックを変えた——ぶつかる（deleted）",
			sc:        syncCase{base: base, current: []storedBlock{a, c}, order: []string{"a", "b", "c"}, changes: map[string]string{"b": "B自分"}},
			final:     "a=A c=C",
			conflicts: "b:deleted",
		},
		{
			name:  "相手が変えていないブロックを消した——消える",
			sc:    syncCase{base: base, current: base, order: []string{"a", "c"}},
			final: "a=A c=C", dirty: true,
		},
		{
			name:  "相手が変えたブロックを消した——残す（kept）",
			sc:    syncCase{base: base, current: []storedBlock{a, pb("b", "B相手"), c}, order: []string{"a", "c"}},
			final: "a=A b=B相手 c=C", kept: "b",
		},
		{
			name:  "相手も消していた——そのまま",
			sc:    syncCase{base: base, current: []storedBlock{a, c}, order: []string{"a", "c"}},
			final: "a=A c=C",
		},
		{
			name:  "足した——画面で前にあるブロックの後ろへ",
			sc:    syncCase{base: base, current: base, order: []string{"a", "n", "b", "c"}, changes: map[string]string{"n": "新"}},
			final: "a=A n=新 b=B c=C", dirty: true,
		},
		{
			name:  "同じ位置に2人が足した——先に保存した相手が上",
			sc:    syncCase{base: base, current: []storedBlock{a, pb("t", "相手の新"), b, c}, order: []string{"a", "n", "b", "c"}, changes: map[string]string{"n": "自分の新"}},
			final: "a=A t=相手の新 n=自分の新 b=B c=C", dirty: true,
		},
		{
			name:  "相手が足した——相手の位置に残る（自分は別を変えた）",
			sc:    syncCase{base: base, current: []storedBlock{a, b, pb("t", "相手の新"), c}, order: []string{"a", "b", "c"}, changes: map[string]string{"a": "A自分"}},
			final: "a=A自分 b=B t=相手の新 c=C", dirty: true,
		},
		{
			name:  "自分が並べ替えた（c を先頭へ）——自分の並び",
			sc:    syncCase{base: base, current: base, order: []string{"c", "a", "b"}},
			final: "c=C a=A b=B", dirty: true,
		},
		{
			name:  "相手が並べ替え、自分は中身だけ変えた——相手の並び＋自分の中身",
			sc:    syncCase{base: base, current: []storedBlock{c, a, b}, order: []string{"a", "b", "c"}, changes: map[string]string{"a": "A自分"}},
			final: "c=C a=A自分 b=B", dirty: true,
		},
		{
			name:  "2人とも並べ替えた——後から並べた自分の並び",
			sc:    syncCase{base: base, current: []storedBlock{c, a, b}, order: []string{"b", "a", "c"}},
			final: "b=B a=A c=C", dirty: true,
		},
		{
			name:  "自分が並べ替え、相手が足した——相手の新しいブロックは相手の並びで前にあるものの後ろへ",
			sc:    syncCase{base: base, current: []storedBlock{a, pb("t", "相手の新"), b, c}, order: []string{"c", "a", "b"}},
			final: "c=C a=A t=相手の新 b=B", dirty: true,
		},
		{
			name:  "相手が動かしたブロックを自分が変えた——ぶつからない（中身は自分・位置は相手）",
			sc:    syncCase{base: base, current: []storedBlock{b, a, c}, order: []string{"a", "b", "c"}, changes: map[string]string{"a": "A自分"}},
			final: "b=B a=A自分 c=C", dirty: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.sc.plan()
			if got := finalText(p); got != tt.final {
				t.Errorf("仕上がり\n got: %s\nwant: %s", got, tt.final)
			}
			if got := conflictKinds(p); got != tt.conflicts {
				t.Errorf("ぶつかり got=%q want=%q", got, tt.conflicts)
			}
			if got := strings.Join(p.kept, " "); got != tt.kept {
				t.Errorf("残した got=%q want=%q", got, tt.kept)
			}
			if p.dirty != tt.dirty {
				t.Errorf("書く got=%v want=%v", p.dirty, tt.dirty)
			}
		})
	}
}

// 表の違うセルを2人が直した——計画の段で黙って合わさる（why=merged）。
func TestPlanSyncTableMerge(t *testing.T) {
	tb, _ := storedFormOf(tbl, "t1")
	theirs, _ := storedFormOf(strings.Replace(tbl, "<td>B</td>", "<td>B2</td>", 1), "t1")
	sc := syncCase{
		base: []storedBlock{tb}, current: []storedBlock{theirs}, order: []string{"t1"},
		changes: map[string]string{"t1": strings.Replace(tbl, "<td>1</td>", "<td>10</td>", 1)},
	}
	p := sc.plan()
	if len(p.conflicts) != 0 {
		t.Fatalf("違うセルなのにぶつかった: %v", p.conflicts)
	}
	if p.why["t1"] != "merged" || !p.dirty {
		t.Fatalf("合わせた印が無い: why=%q dirty=%v", p.why["t1"], p.dirty)
	}
	if h := p.final[0].HTML; !strings.Contains(h, "<td>10</td>") || !strings.Contains(h, "<td>B2</td>") {
		t.Fatalf("両方が入っていない: %s", h)
	}
	// 同じセルなら並べる。
	sc.changes = map[string]string{"t1": strings.Replace(tbl, "<td>B</td>", "<td>B3</td>", 1)}
	if p := sc.plan(); conflictKinds(p) != "t1:changed" {
		t.Fatalf("同じセルを違うように直したのにぶつからない: %v", p.conflicts)
	}
}

// 画面の元の書き出しが正本と字面だけ違う（画面の書き出しは字下げする）——指紋が違っても中身が同じなら当てる。
func TestPlanSyncBaseFormattingOnly(t *testing.T) {
	stored, _ := storedFormOf(tbl, "t1")
	clientBase := strings.Replace(tbl, "<tbody>", "\n    <tbody>\n        ", 1) // 画面の字下げ
	req := SyncRequest{
		Base:  []SyncBase{{ID: "t1", H: "画面が持っていた古い指紋"}},
		Order: []string{"t1"},
	}
	mine, _ := storedFormOf(strings.Replace(tbl, "<td>1</td>", "<td>10</td>", 1), "t1")
	req.Changes = []SyncChange{{ID: "t1", BaseH: "画面が持っていた古い指紋", BaseHTML: clientBase, HTML: mine.HTML}}
	p := planSync([]storedBlock{stored}, req, map[string]storedBlock{"t1": mine}, map[string]bool{})
	if len(p.conflicts) != 0 || !strings.Contains(p.final[0].HTML, "<td>10</td>") {
		t.Fatalf("字面だけの違いでぶつかった: %v %s", p.conflicts, p.final[0].HTML)
	}
}
