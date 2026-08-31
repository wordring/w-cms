package cms

import (
	"database/sql"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"w-cms/internal/cms/page"
)

// 回覧化の**同値性テスト**（[docs/【考察】パーサとプラグイン.md] §8）。
//
// 語彙移行（migrate_vocab）で使った流儀をそのまま踏襲します——「同じ本文から
// 同じ索引行が得られる」ことを、旧走査と新観察係の両方を走らせて突き合わせる。
// 移植そのものはテストが緑になるだけでは足りず、**行が一致すること**まで見ないと、
// 「動いてはいるが中身が違う」形の退行を見逃します。

// syncVocabAll は**移植前の走査**（旧 `vocabIndexPlugin.Sync`）です。
// 比較対象としてテスト側に置いています——本番のコードに「使われない古い実装」を
// 残さないためで、ここが本文なのはこのファイルが同値性テストだからです。
func syncVocabAll(tx *sql.Tx, pageID int, root *html.Node) error {
	if _, err := tx.Exec(`DELETE FROM vocab_index WHERE page_id = ?`, pageID); err != nil {
		return err
	}
	blockNo := map[string]int{}
	var firstErr error
	WalkElements(root, func(n *html.Node) {
		if firstErr != nil || (n.Data != "table" && n.Data != "dl") {
			return
		}
		dataType := Attr(n, "data-type")
		if dataType == "" {
			return // 素の table / dl は文書中の普通の表・定義リスト（オプトイン規約）
		}
		no := blockNo[dataType]
		blockNo[dataType]++
		def, _ := VocabDefByType(dataType)

		var err error
		if n.Data == "table" {
			err = syncVocabTable(tx, pageID, dataType, no, Attr(n, "data-id"), def, n)
		} else {
			err = syncVocabDL(tx, pageID, dataType, no, Attr(n, "data-id"), def, n)
		}
		if err != nil {
			firstErr = err
		}
	})
	return firstErr
}

// indexRows は vocab_index の全行を比較しやすい文字列にして返します。
func indexRows(t *testing.T, db *sql.DB, pageID int) []string {
	t.Helper()
	rows, err := db.Query(`
		SELECT data_type, block_no, COALESCE(block_id,''), row_no, field,
		       COALESCE(value,''), COALESCE(norm_value,'')
		FROM vocab_index WHERE page_id = ?
		ORDER BY data_type, block_no, row_no, field, value`, pageID)
	if err != nil {
		t.Fatalf("vocab_index の読み出しに失敗: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var dt, blockID, field, value, norm string
		var blockNo, rowNo int
		if err := rows.Scan(&dt, &blockNo, &blockID, &rowNo, &field, &value, &norm); err != nil {
			t.Fatalf("Scanエラー: %v", err)
		}
		out = append(out, strings.Join([]string{
			dt, strconv.Itoa(blockNo), blockID, strconv.Itoa(rowNo), field, value, norm,
		}, "|"))
	}
	return out
}

// equivBodies は同値性の比較に使う本文です。②汎用索引の消費形態がいちばん広いので、
// 表・定義リスト・入れ子・未定義の形式・クロームを1通り並べます。
var equivBodies = []struct {
	name string
	html string
}{
	{"タグの定義リスト", `<dl data-type="tags"><dt>部品番号</dt><dd>X1</dd><dt>担当</dt><dd>山田</dd></dl>`},
	{"部材表", `<table data-type="part-materials"><tbody>` +
		`<tr><th>部材名</th><th>単価</th><th>仕入先</th><th>数量</th></tr>` +
		`<tr><td>SS400</td><td>¥8,000</td><td>鋼材商会</td><td>2</td></tr>` +
		`<tr><td>SPCC</td><td>500</td><td>東邦</td><td></td></tr>` +
		`</tbody></table>`},
	{"入れ子の受発注", `<section data-type="file" data-src="po.pdf">` +
		`<section data-type="client-order"><dl><dt>発注書番号</dt><dd>PO-1</dd>` +
		`<dt>発注元</dt><dd>得意先A</dd><dt>発注日</dt><dd>2026-08-26</dd></dl>` +
		`<table data-type="client-order-items"><tbody>` +
		`<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td>X1</td><td>部品X1</td><td>1000</td><td>3</td><td>未着手</td></tr>` +
		`</tbody></table></section></section>`},
	{"未定義の形式も索引する", `<table data-type="なぞの表"><tbody>` +
		`<tr><th>あ</th><th>い</th></tr><tr><td>1</td><td>2</td></tr></tbody></table>`},
	{"同じ形式が複数ある（block_no）", `<dl data-type="tags"><dt>a</dt><dd>1</dd></dl>` +
		`<p>あいだ</p><dl data-type="tags"><dt>a</dt><dd>2</dd></dl>`},
	{"素の表と定義リストは索引しない", `<table><tr><td>素</td></tr></table><dl><dt>素</dt><dd>x</dd></dl>`},
	{"クロームの中は索引しない", `<section data-type="required-materials">` +
		`<div class="vocab-chrome"><table data-type="part-materials"><tbody>` +
		`<tr><th>部材名</th></tr><tr><td>出てはいけない</td></tr></tbody></table></div></section>`},
}

// TestVocabIndexObserverMatchesLegacyWalk は、②汎用索引の観察係が旧走査と
// **同じ行**を書くことを検証します。
//
// 意図的に違うのは2点で、**どちらも修正であって退行ではない**ので、
// その本文だけ期待を分けて確かめます。
//
//  1. クロームの扱い——旧走査は `.vocab-chrome` の中も索引していました
//     （各自が除外を覚える方式だったので、②は覚えていなかった）。新方式では
//     配送係が歩かないので索引されません。
//  2. 業務文書ブロックのヘッダ——旧走査は data-type のある table / dl しか見ず、
//     ヘッダの素の <dl> を落としていました。硬いドメイン表があったころは各
//     プラグインが自分で読んでいたので気づきませんでしたが、テーブルを廃して
//     索引へ一本化する（D-1）と発注元・発注日がどこにも残りません。
//     新方式は section の側から拾います（TestSectionHeaderIsIndexed）。
func TestVocabIndexObserverMatchesLegacyWalk(t *testing.T) {
	db := setupSaveTest(t)

	for _, c := range equivBodies {
		t.Run(c.name, func(t *testing.T) {
			root, err := html.Parse(strings.NewReader(c.html))
			if err != nil {
				t.Fatalf("パースエラー: %v", err)
			}

			// 旧走査（ページ 900）
			tx, err := db.Begin()
			if err != nil {
				t.Fatalf("Beginエラー: %v", err)
			}
			if err := syncVocabAll(tx, 900, root); err != nil {
				tx.Rollback()
				t.Fatalf("旧走査エラー: %v", err)
			}
			tx.Commit()

			// 新観察係（ページ 901）
			tx, err = db.Begin()
			if err != nil {
				t.Fatalf("Beginエラー: %v", err)
			}
			ctx := &ObserveContext{Tx: tx, PageID: 901}
			vip := vocabIndexPlugin{}
			if err := vip.OnPageStart(ctx); err != nil {
				tx.Rollback()
				t.Fatalf("OnPageStartエラー: %v", err)
			}
			reg := newWalkRegistry()
			reg.observe(TriggerAll, ObserveHandlerFunc(vip.OnElement))
			if err := reg.walkObserve(ctx, []*html.Node{root}); err != nil {
				tx.Rollback()
				t.Fatalf("回覧エラー: %v", err)
			}
			tx.Commit()

			legacy := indexRows(t, db, 900)
			modern := indexRows(t, db, 901)

			if strings.Contains(c.name, "クローム") {
				if len(modern) != 0 {
					t.Errorf("クロームの中が索引されています: %v", modern)
				}
				if len(legacy) == 0 {
					t.Error("前提が崩れています: 旧走査はクロームの中も索引していたはず")
				}
				return
			}
			if strings.Contains(c.name, "受発注") {
				// 新方式はヘッダ（素の dl）も拾うので、旧走査の行は新方式の
				// 部分集合になる。**旧に無い行が増えている**ことを確かめる。
				for _, row := range legacy {
					if !slices.Contains(modern, row) {
						t.Errorf("旧走査の行が新方式で消えました: %s", row)
					}
				}
				if len(modern) <= len(legacy) {
					t.Errorf("ヘッダが索引されていません:\n旧:\n%s\n新:\n%s",
						strings.Join(legacy, "\n"), strings.Join(modern, "\n"))
				}
				return
			}
			if strings.Join(legacy, "\n") != strings.Join(modern, "\n") {
				t.Errorf("索引行が一致しません:\n旧:\n%s\n新:\n%s",
					strings.Join(legacy, "\n"), strings.Join(modern, "\n"))
			}
		})
	}
}

// TestObserversSeeNestedItemsTable は、受注の観察係が明細表を丸ごと担当しても
// ②汎用索引が同じ明細表を索引できることを検証します。
//
// 配送係は「**全員**が降りないと言ったときだけ降りない」（OR）という規約で動きます。
// ここを AND にすると、受注の観察係が descend=false を返した瞬間に②の索引から
// 明細が丸ごと消えます——**動いているのに中身が欠ける**形の退行なので、固定します。
func TestObserversSeeNestedItemsTable(t *testing.T) {
	db := setupSaveTest(t)
	body := `<section data-type="client-order"><dl><dt>発注書番号</dt><dd>PO-9</dd></dl>` +
		`<table data-type="client-order-items"><tbody>` +
		`<tr><th>品番</th><th>数量</th></tr><tr><td>X9</td><td>4</td></tr>` +
		`</tbody></table></section>`

	if err := page.WriteSidecar("000910", page.PageMeta{Owner: "tester", Mode: page.DefaultMode}); err != nil {
		t.Fatalf("サイドカー作成エラー: %v", err)
	}
	if err := SyncIndex("000910", body); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM vocab_index WHERE page_id = ? AND data_type = ?`,
		910, "client-order-items").Scan(&n); err != nil {
		t.Fatalf("クエリエラー: %v", err)
	}
	if n == 0 {
		t.Error("明細表が②汎用索引に載っていません（受注の観察係が担当したせいで消えた）")
	}

	// 受注ヘッダ（素の dl）も索引へ入っていること。
	var orders int
	db.QueryRow(`SELECT COUNT(DISTINCT block_no) FROM vocab_index WHERE page_id = ? AND data_type = ?`, 910, "client-order").Scan(&orders)
	if orders != 1 {
		t.Errorf("受注ヘッダが入っていません: %d", orders)
	}
}
