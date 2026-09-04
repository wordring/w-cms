package cms

// ─────────────────────────────────────────────────────────────────────────
// 参照追従——集計のスコープを参照タグで決める（2026-09-04）
//
// 決定 D-4 で参照専用テーブル `page_refs` を捨て、ページ間の線は本文の
// 「名前：値」タグ（値は `ページID-ブロックID`）になりました。その**本体コスト**が
// ここです（[docs/【考察】通信記録処理.md] §2.5「集計の参照追従化」）。
//
// **何が変わるか**——③計算はこれまで `WHERE page_id = ?` で自ページの明細しか
// 読めませんでした。取り込みで生まれた受注ページは通信記録ページの子で、
// `受信元` タグで親を指しています。**通信記録ページの上で集計しても、明細は
// 別のページに在るので何も出ない**——これが「生成された受注ページが③計算に
// 乗らない」の中身でした。
//
// **どう解くか**——集計の対象を「このページ」から「**このページと、参照で
// 直接つながっているページ**」へ広げます。向きは両方:
//
//	案件ハブ ──原発注書:000002-12──▶ 発注書ページ   （外向き。ハブが集計単位になる）
//	通信記録 ◀──受信元:010154-mt3b── 受注ページ     （内向き。記録の上で明細が見える）
//
// **深さは1だけ**です。辿り続けると、どこまでが1つの案件なのかが人に見えなく
// なります（画面には参照が1本ずつしか出ていないのに、集計だけが遠くのページを
// 巻き込む）。多段が要るなら、その中間に案件ハブを置くのが設計の筋です。
//
// **読めないページは混ぜません**（`page.CanView`）。品番は本文へ自由に書けるので、
// 絞りを外すと読めないページの中身が集計ごしに漏れます（設計総点検）。
// 欠けたことは知らせません（C案の決定——[docs/作業引き継ぎ.md]）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"fmt"
	"sort"
	"strconv"
)

// RelatedPages は集計のスコープを返します——**自ページ＋参照で直接つながる
// ページ**（外向き・内向きの両方・深さ1）。返り値は昇順で、重複はありません。
//
// 参照の文法は描画側（ref_render.go）と同じものを使います。ここを別に持つと、
// **画面ではリンクになっているのに集計には乗らない**（あるいはその逆）という、
// いちばん説明しづらい食い違いが生まれます。
func RelatedPages(db ReadOnlyDB, pageID int) ([]int, error) {
	found := map[int]bool{pageID: true}

	// ── 外向き: このページのタグが指しているページ ──
	rows, err := db.Query(`
		SELECT field, value FROM vocab_index
		WHERE page_id = ? AND data_type = 'tags'
	`, pageID)
	if err != nil {
		return nil, err
	}
	// **先に読み切ってから解釈します**。行を読みながら別のクエリを投げると
	// カーソルが接続を握ったままになり、`:memory:` DBでは別の空DBに当たります
	// （2026-09-03 に本番コードで踏んだ罠）。
	type tag struct{ field, value string }
	var tags []tag
	for rows.Next() {
		var t tag
		if err := rows.Scan(&t.field, &t.value); err != nil {
			rows.Close()
			return nil, err
		}
		tags = append(tags, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, t := range tags {
		if id, _, ok := parseRefValue(t.value); ok {
			addPageID(found, id)
			continue
		}
		// ページ全体を指すと宣言されたタグ（返信元 等）は6桁だけの形も採る。
		if id, ok := parsePageRef(t.field, t.value); ok {
			addPageID(found, id)
		}
	}

	// ── 内向き: このページを指しているタグを持つページ ──
	//
	// 値の**前方一致**で候補を集め、文法の判定は Go 側で行います。SQL だけで
	// 絞れないのは、区切りのハイフンに種類があるためです（`-` `‐` `−` `－` `ー` `ｰ`)。
	// 6桁で前方一致を取ってから `parseRefValue` に通せば、描画側とまったく同じ
	// 規則で判定できます（`0101540` のような7桁は文法で落ちます）。
	self, ok := page6(pageID)
	if !ok {
		return sortedKeys(found), nil
	}
	back, err := db.Query(`
		SELECT page_id, field, value FROM vocab_index
		WHERE data_type = 'tags' AND value LIKE ?
	`, self+"%")
	if err != nil {
		return nil, err
	}
	defer back.Close()
	for back.Next() {
		var id int
		var field, value string
		if err := back.Scan(&id, &field, &value); err != nil {
			return nil, err
		}
		target, _, ok := parseRefValue(value)
		if !ok {
			target, ok = parsePageRef(field, value)
		}
		if ok && target == self {
			found[id] = true
		}
	}
	return sortedKeys(found), back.Err()
}

// addPageID はゼロ埋め6桁のページIDを集合へ足します。
func addPageID(set map[int]bool, id string) {
	if n, err := strconv.Atoi(id); err == nil {
		set[n] = true
	}
}

// page6 はページ番号をゼロ埋め6桁にします（範囲外は ok=false）。
func page6(id int) (string, bool) {
	if id < 0 || id > 999999 {
		return "", false
	}
	return fmt.Sprintf("%06d", id), true
}

func sortedKeys(set map[int]bool) []int {
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
