package cms

import "testing"

// **DBに入るのは、登録された語彙だけ**（2026-09-20 ユーザー決定:「事前に登録されている
// 語彙だけＤＢに入れましょう。すべての表をＤＢに入れる必要はないと思います」）。
//
// ⚠ **それまでは、形式が解決しない表も「形式名が空」のまま索引に入っていました**——
// `VocabDefByType` が「未定義でもゼロ値の def で続行」していたためで、普通の文章の中の
// 表まで載っていました。意図した設計ではなく、気づかれていなかった振る舞いです。
//
// これが効くと、**受注ページに顧客の発注書の写しをそのまま置けます**——登録しなければ
// 索引に載らないので、弊社の明細と二重計上になりません
// （[docs/【考察】発注書から受注明細へ.md] §2.5）。

// TestUnregisteredTableIsNotIndexed は、**登録されていない形式の表が索引に入らない**
// ことを固定します。
func TestUnregisteredTableIsNotIndexed(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>受注</h1>` +
		// 顧客の発注書の写し——**見せるだけ。登録しないので索引に載らない**。
		`<table data-type="client-order-source"><tbody>` +
		`<tr><th>図面No.</th><th>品名</th><th>員数</th></tr>` +
		`<tr><td>K120-1</td><td>受けブラケット</td><td>100</td></tr>` +
		`</tbody></table>` +
		// 弊社の明細——**登録済みなので載る**。
		`<table data-type="client-order-items"><tbody>` +
		`<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td>A1</td><td>受けブラケット</td><td>390</td><td>100</td><td>未着手</td></tr>` +
		`</tbody></table>`
	if err := SyncIndex("000071", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rows := queryVocabRows(t, 71)
	for _, r := range rows {
		if r.dataType != "client-order-items" {
			t.Errorf("登録されていない形式が索引に入っています: %+v", r)
		}
	}
	// **登録済みのほうは、ちゃんと入っている**（塞ぎすぎていないことの確認）。
	if len(rows) == 0 {
		t.Error("登録済みの明細まで索引から消えています")
	}
}

// TestUnregisteredHeadingSectionIsNotIndexed は、**登録されていない見出しの節の中の
// 素の表も索引に入らない**ことを固定します。
//
// ⚠ `OnElement` と `syncVocabSection` で**線引きが割れやすい**ところです。揃えないと
// 未登録の節の中の素の表が、その節の名前のまま索引に残ります。
//
// ⚠ **当て先は `data-type` を持つ節です**（2026-09-20 に変異試験で気づきました）。
// `<section><h2>作業メモ</h2>` のような**見出しだけの未登録の節は、配送係がそもそも
// 届けません**——引き金（`vocabTypeOf`）が空の要素では誰も呼ばれない決まりなので、
// `syncVocabSection` に到達しません。最初そちらで書いたら、**守りを外しても通る
// 試験**になっていました（測っていたのは配送係で、守りではなかった）。
//
// ⚠ **見出しは `検査記録` を使います。** ワンノートの受け皿そのもの（`■材料` など）は
// `ext/subcon` の語彙なので、**コアの試験では登録されていません**——`材料` で書いたら
// 当然のように索引されず、危うく「受け皿が壊れた」と誤報するところでした。
// 確かめたい仕組み（見出しで形式が決まる素の表）は同じです。
func TestUnregisteredHeadingSectionIsNotIndexed(t *testing.T) {
	setupSaveTest(t)

	body := `<h1>加工製品</h1>` +
		`<section data-type="なぞの節"><h2>作業メモ</h2>` + // レジストリに無い形式
		`<table><tbody><tr><th>日</th><th>やったこと</th></tr>` +
		`<tr><td>2026-09-20</td><td>段取り</td></tr></tbody></table>` +
		`</section>` +
		`<section><h2>検査記録</h2>` + // 登録済みの見出し（コアの語彙）
		`<table><tbody><tr><th>品番</th><th>判定</th></tr>` +
		`<tr><td>A1</td><td>合格</td></tr></tbody></table>` +
		`</section>`
	if err := SyncIndex("000072", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	rows := queryVocabRows(t, 72)
	for _, r := range rows {
		if r.dataType != "inspection-record" {
			t.Errorf("登録されていない見出しの表が索引に入っています: %+v", r)
		}
	}
	// ⚠ **ワンノート移行の受け皿を潰していないこと**——見出しで形式が決まる素の表は、
	// これまでどおり載ります（一度うっかり消して気づいた経緯あり・2026-09-18）。
	if len(rows) == 0 {
		t.Error("登録済みの見出しの下の素の表まで索引から消えています（移行の受け皿）")
	}
}
