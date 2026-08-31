package cms

// ─────────────────────────────────────────────────────────────────────────
// 保存時の告知（エコーバックの流儀）
//
// レジストリは安全性の門ではないので、未知の形式も改名された見出しも**保存は
// 通します**。その代わり、保存応答で「システムはこう読めなかった」を返すのが
// この検査群です——拒否ではなく告知。担い手は2つ:
//
//   - UnknownVocabTypes    … レジストリに無い data-type（綴り違い・未定義の形式）
//   - UnresolvedVocabFields … 見出しの改名で③計算が読めなくなった列と、
//                             形式の外にある鍵（RequiresTag）の欠け
//
// 検査の語彙（レジストリ・推論辞書）は vocab.go、走査の道具（eachDLPair・
// tableRows）は vocab_index.go と共有します。
// ─────────────────────────────────────────────────────────────────────────

import (
	"sort"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

// UnknownVocabTypes は本文中の table/dl が持つ data-type のうち、レジストリに
// 未定義のものを重複なくソートして返します。
//
// 未知の data-type は**通します**（決定ログ: 値は不活性で害がなく、索引に載るだけ。
// 落とす方式はレジストリを安全性の門に変質させる）。この関数は保存時の**告知**
// （拒否ではなくエコーバックの流儀）のために使います。
func UnknownVocabTypes(htmlStr string) []string {
	nodes, err := htmldoc.ParseFragment(htmlStr)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, root := range nodes {
		WalkElements(root, func(n *html.Node) {
			// マーカーの担い手は table / dl / section の3つ（サニタイザの許可と揃える）。
			// section を落としていたため、業務文書ブロックの綴り違いが完全に無症状だった
			// ——③計算からは受注が消えるのに、画面も告知も何も変わらない。
			if n.Data != "table" && n.Data != "dl" && n.Data != "section" {
				return
			}
			dt := Attr(n, "data-type")
			if dt == "" || seen[dt] {
				return
			}
			if _, ok := VocabDefByType(dt); !ok {
				seen[dt] = true
			}
		})
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// UnresolvedVocabFields は「見出しの改名によって③計算プラグインが読めなくなった列」を
// 告知用に返します。返す形は "表示名: ラベル"（例: "顧客の発注書: 発注元"）です。
//
// 鍵は **見出しの表示文字**で解決されます（columnFor は Field でも Label でも引ける）。
// そのため見出しを「単価」→「単価（税抜）」のように改名すると宣言列に当たらなくなり、
// 型付きテーブルへの同期が**黙って**止まります。それを保存時に気づけるようにするのが
// この関数の役割です——拒否ではなく告知（エコーバックの流儀。UnknownVocabTypes と同じ）。
//
// 報告するのは **機械キー（Field）を持つ列だけ**です。Field を持たない列
// （tags の自由語・inspection-record の記録列）は表示文字がそのまま鍵なので、
// 改名しても「別の鍵になる」だけで壊れません。
//
// 誤検知を避けるため、**改名の徴候があるときだけ**報告します——すなわち
// 「宣言列のうち解決されなかったものがある」**かつ**「どの宣言列にも当たらない見出しがある」。
// 列を消しただけ（見出しごと削除）や、独自の列を足しただけでは報告しません。
func UnresolvedVocabFields(htmlStr string) []string {
	nodes, err := htmldoc.ParseFragment(htmlStr)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	// 形式の外にある鍵（RequiresTag）を使う形式が本文にあるか。あるなら、その鍵が
	// 実際に引けることまで確かめる。
	needsTag := map[string]VocabDef{}
	for _, root := range nodes {
		WalkElements(root, func(n *html.Node) {
			// 形式の解決は索引と同じ vocabTypeOf——data-type が正、無ければ機能見出し
			// （D-2）。ここが索引側とずれると「索引には載るのに告知されない」ズレになる。
			def, ok := VocabDefByType(vocabTypeOf(n))
			if !ok {
				return
			}
			if n.Data == "section" {
				// section は素の中身から鍵を集める（syncVocabSection と同じ切り分け）。
				// 素の dl はヘッダ＝形式自身の列、素の table は明細＝ Items 宣言の列。
				itemsDef := def
				if def.Items != "" {
					if idef, ok := VocabDefByType(def.Items); ok {
						itemsDef = idef
					}
				}
				eachPlainVocabChild(n, func(c *html.Node) {
					switch c.Data {
					case "dl":
						for _, label := range unresolvedKeys(dlHeadingKeys(c), def) {
							seen[def.DisplayName+": "+label] = true
						}
					case "table":
						for _, label := range unresolvedKeys(tableHeadingKeys(c), itemsDef) {
							seen[itemsDef.DisplayName+": "+label] = true
						}
					}
				})
			} else if n.Data == def.Element {
				for _, label := range unresolvedKeys(vocabHeadingKeys(n, def), def) {
					seen[def.DisplayName+": "+label] = true
				}
			} else {
				return // 要素と形式が合わない（table に dl の形式名など）は対象外
			}
			if def.RequiresTag != "" {
				needsTag[def.Type] = def
			}
		})
	}
	// tags は自由語なので、鍵を列として宣言すると全ページで誤検知になる
	// （担当者しか書いていないページでも「部品番号が無い」と鳴ってしまう）。
	// 報告するのは、宣言列と同じく**改名の徴候があるとき**だけ——すなわち
	// 「その形式が本文にあり」「タグを実際に使っており」「それでも鍵が引けない」場合。
	// タグをまだ1つも書いていない書きかけのページでは黙る（列を消しただけなら
	// 報告しない、というこの関数の流儀に合わせる）。
	if len(needsTag) > 0 && hasTagsList(nodes) {
		for _, def := range needsTag {
			found := false
			for _, root := range nodes {
				if TagValue(root, def.RequiresTag) != "" {
					found = true
					break
				}
			}
			if !found {
				seen[def.DisplayName+": "+def.RequiresTag] = true
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// hasTagsList は本文にページのタグ（セクション外の素の dl。属性でも可）が
// 1つでもあるかを返します。
// RequiresTag の告知を「タグを使っているのに鍵が引けない」場合に限るための徴候判定。
func hasTagsList(nodes []*html.Node) bool {
	found := false
	for _, root := range nodes {
		WalkElements(root, func(n *html.Node) {
			if n.Data == "dl" && vocabTypeOf(n) == "tags" {
				found = true
			}
		})
		if found {
			return true
		}
	}
	return false
}

// unresolvedKeys は鍵の列を宣言（def）と突き合わせ、解決できなかった宣言列のラベルを返します。
func unresolvedKeys(keys []string, def VocabDef) []string {
	// 機械キーを持つ列が無い形式（自由語・記録用）は対象外。
	typed := false
	for _, c := range def.Columns {
		if c.Field != "" {
			typed = true
			break
		}
	}
	if !typed {
		return nil
	}

	resolved := map[string]bool{}
	stray := false
	for _, key := range keys {
		if col, ok := def.columnFor(key); ok {
			resolved[col.Field] = true
		} else if key != "" {
			stray = true // どの宣言列にも当たらない見出し＝改名の徴候
		}
	}
	if !stray {
		return nil // 列の削除・独自列の追加だけなら報告しない
	}
	var out []string
	for _, c := range def.Columns {
		if c.Field != "" && !resolved[c.Field] {
			out = append(out, c.Label)
		}
	}
	return out
}

// tableHeadingKeys は表の見出し行（最初の tr）の表示文字を返します。
func tableHeadingKeys(table *html.Node) []string {
	rows := tableRows(table)
	if len(rows) == 0 {
		return nil
	}
	var out []string
	for _, cell := range rowCells(rows[0]) {
		out = append(out, strings.TrimSpace(nodeText(cell)))
	}
	return out
}

// vocabHeadingKeys は形式インスタンスが携帯するスキーマ（表の見出し行・dl の dt）から
// 鍵を取り出します。鍵の決め方は読み取り経路（VocabTableRows / VocabDLFields）と同じ。
// section は呼び出し側（UnresolvedVocabFields）が素の中身ごとに分けて扱うため、ここへは来ません。
func vocabHeadingKeys(n *html.Node, def VocabDef) []string {
	switch def.Element {
	case "table":
		return tableHeadingKeys(n)
	case "dl":
		return dlHeadingKeys(n)
	}
	return nil
}

// dlHeadingKeys は dl の項目の鍵（直前の dt の表示文字）を返します。
func dlHeadingKeys(dl *html.Node) []string {
	if dl == nil {
		return nil
	}
	var out []string
	eachDLPair(dl, true, func(key string, dd *html.Node) bool {
		out = append(out, key)
		return true
	})
	return out
}
