package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// 受注フォルダを開いたとき、空いている `弊社品番` を埋める（2026-10-01・backlog.go の linkBacklogRows）。
//
// 利用者:「受注ページを作った時に弊社品番が無くリンクされなかった品物は、受注フォルダを開いたタイミングで、品番を
// 検索しページ番号を取得します。検索出来れば弊社品番がありますし、なければ依然として背景薄赤です」。

// seedOrderUnder は受注フォルダ rootInt の下に受注ページを置きます（本文のファイルも書く——結ぶときは本文を読むため）。
func seedOrderUnder(t *testing.T, idInt, rootInt int, rows ...[2]string) {
	t.Helper()
	addPage(t, idInt, rootInt, "受注", "alice", "302", true)
	seedBody(t, page.FormatID(idInt), orderBody(rows...))
}

// TestBacklogLinksLaterProductWhenOpened は、**受注が先・加工製品ページが後**でも、受注フォルダを開けば
// 結ばれることを固定します。
//
//   - 空いている行だけ埋める（人が入れた値は触らない）・候補の無い品番は空のまま（薄赤のまま）
//   - 埋めた行は、その場の表でリンクになる（書いてから読み直す）
//   - 2回目は何も書かない
func TestBacklogLinksLaterProductWhenOpened(t *testing.T) {
	setupExtTest(t, "000600", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	addPage(t, 601, -1, "受注", "alice", "302", true)
	// 受注が先——このときはまだ加工製品ページが無い。
	seedOrderUnder(t, 602, 601, [2]string{"", "K120-01-211"}, [2]string{"", "K120-99-999"})
	seedOrderUnder(t, 603, 601, [2]string{"000777", "K120-01-211"}) // 人が入れた値
	// あとから加工製品ページができる（整理を通らない取り込みなど）。
	seedProductPage(t, 604, "K120-01-211 留めブラケット", "K120-01-211")

	out := backlogViewHTML(adminUser(), 601)
	if !strings.Contains(out, `<a href="/000604">000604</a>`) {
		t.Errorf("開いた表で弊社品番がリンクになっていません:\n%s", out)
	}
	if n := strings.Count(out, `backlog-no-item`); n != 1 {
		t.Errorf("薄赤のセルが %d 個です（候補の無い K120-99-999 の1個を期待）:\n%s", n, out)
	}
	body, err := cms.ReadPageBody("000602")
	if err != nil || !strings.Contains(body, "000604") {
		t.Errorf("受注ページに書き戻されていません（%v）:\n%s", err, body)
	}
	other, _ := cms.ReadPageBody("000603")
	if !strings.Contains(other, "000777") || strings.Contains(other, "000604") {
		t.Errorf("⚠ 人が入れた弊社品番を書き換えています:\n%s", other)
	}

	groups, _ := backlogScan(adminUser(), 601)
	if n := linkBacklogRows(adminUser(), groups); n != 0 {
		t.Errorf("2回目で %d 行書き換えています", n)
	}
}

// TestBacklogLinkSkipsEditingAndReadOnly は、⚠ **書けない人は書かず、誰かが編集中の受注ページは飛ばす**ことを
// 固定します（どちらも薄赤のまま——次に書ける人が開いたとき・編集が終わってから開いたときに結ぶ）。
func TestBacklogLinkSkipsEditingAndReadOnly(t *testing.T) {
	setupExtTest(t, "000610", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	addPage(t, 611, -1, "受注", "alice", "302", true)
	seedOrderUnder(t, 612, 611, [2]string{"", "K120-01-211"})
	seedProductPage(t, 613, "K120-01-211 留めブラケット", "K120-01-211")

	// 読めるが書けない人（other＝読むだけ）が開いても書かない。
	reader := &auth.User{Username: "bob"}
	out := backlogViewHTML(reader, 611)
	if !strings.Contains(out, `backlog-no-item`) {
		t.Errorf("書けない人が開いたのに薄赤が消えています:\n%s", out)
	}
	if body, _ := cms.ReadPageBody("000612"); strings.Contains(body, "000613") {
		t.Errorf("⚠ 書けない人が開いただけで受注ページを書き換えています:\n%s", body)
	}

	// 誰かが編集中なら、書ける人が開いても飛ばす。
	if r := editlock.Locks.TryAcquire(612, "alice", ""); !r.Acquired {
		t.Fatal("ロックを取れません")
	}
	t.Cleanup(func() { editlock.Locks.ForceRelease(612) })
	backlogViewHTML(adminUser(), 611)
	if body, _ := cms.ReadPageBody("000612"); strings.Contains(body, "000613") {
		t.Errorf("⚠ 編集中の受注ページを書き換えています:\n%s", body)
	}

	// 編集が終われば、次に開いたときに結ぶ。
	editlock.Locks.ForceRelease(612)
	backlogViewHTML(adminUser(), 611)
	if body, _ := cms.ReadPageBody("000612"); !strings.Contains(body, "000613") {
		t.Errorf("編集が終わったあとに開いても結ばれていません:\n%s", body)
	}
}
