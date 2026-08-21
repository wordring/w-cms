package cms

import (
	"os"
	"path/filepath"
	"testing"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 索引の全再構築は data/master を1ページずつ読み直す長い処理で、途中で止まると
// （強制終了・停電）**次の起動は何事もなく成功し、取り込めなかったページは開いても
// 「ありません」のまま**になっていました。起動時のフックが見ていたのは pages が
// 空かどうかだけで、途中まで入ったDBは「再構築済み」と見分けが付かないためです。
//
// 決定（2026-08-21）: ①始めた印を残す ②起動時に印が残っていたら必ずやり直す
// ③終わったら件数と所要時間を記録に残す。方式そのもの（ファイルから作り直す）は維持。

// seedMasterPages は data/master へページを n 枚置きます（DBには入れない）。
func seedMasterPages(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		dir := page.GetPageDir(id)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("MkdirAllエラー: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".html"),
			[]byte("<h1>ページ"+id+"</h1>"), 0644); err != nil {
			t.Fatalf("本文の作成エラー: %v", err)
		}
		if err := page.WriteSidecar(id, page.PageMeta{Owner: "alice", Mode: "330"}); err != nil {
			t.Fatalf("WriteSidecarエラー: %v", err)
		}
	}
}

// countPages は pages の行数を返します。
func countPages(t *testing.T) int {
	t.Helper()
	var n int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&n); err != nil {
		t.Fatalf("pages を数えられません: %v", err)
	}
	return n
}

// TestRebuildIfNeededResumesInterrupted は、途中で止まった再構築を次の起動でやり直すことを
// 固定します。「pages が空でない」だけでは済ませない、というのがこの試験の眼目です。
func TestRebuildIfNeededResumesInterrupted(t *testing.T) {
	setupUploadTest(t, "000001", page.PageMeta{Owner: "alice", Mode: "330"})
	seedMasterPages(t, "000001", "000002", "000003")

	// 1ページだけ取り込んだところで落ちた状態を作る
	if err := markRebuildStarted(); err != nil {
		t.Fatalf("markRebuildStartedエラー: %v", err)
	}
	if err := SyncIndex("000001", "<h1>ページ000001</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	if countPages(t) != 1 {
		t.Fatalf("前提が崩れています: pages=%d (期待 1)", countPages(t))
	}

	// 起動時のフック。pages は空ではないが、印が残っているのでやり直すこと。
	if err := RebuildIfNeeded(); err != nil {
		t.Fatalf("RebuildIfNeededエラー: %v", err)
	}
	if n := countPages(t); n != 3 {
		t.Errorf("中断した再構築がやり直されていません: pages=%d (期待 3)", n)
	}

	// やり直しが終われば印は消え、2回目は走らない（毎起動で再構築しない）。
	if unfinished, _ := rebuildUnfinished(); unfinished {
		t.Error("再構築が終わったのに印が残っています")
	}
	before := countPages(t)
	if err := RebuildIfNeeded(); err != nil {
		t.Fatalf("RebuildIfNeeded(2回目)エラー: %v", err)
	}
	if countPages(t) != before {
		t.Error("2回目の起動でも再構築が走りました")
	}
}

// TestRebuildRecordsResult は、終わったときに件数と所要時間が残ることを固定します。
// 「取り込めなかったページがある」ことに後から気づける手掛かりになります。
func TestRebuildRecordsResult(t *testing.T) {
	setupUploadTest(t, "000001", page.PageMeta{Owner: "alice", Mode: "330"})
	seedMasterPages(t, "000001", "000002")

	if err := RebuildDatabase(); err != nil {
		t.Fatalf("RebuildDatabaseエラー: %v", err)
	}

	pages, ms, ok := lastRebuildResult()
	if !ok {
		t.Fatal("再構築の記録が残っていません")
	}
	if pages != 2 {
		t.Errorf("記録された件数が違います: %d (期待 2)", pages)
	}
	if ms < 0 {
		t.Errorf("記録された所要時間が負です: %d", ms)
	}
}

// TestRebuildIfNeededStillHandlesEmptyDB は、従来の「DBが空＋ファイルあり」でも
// 再構築が走ることを固定します（バックアップからファイルだけ戻した場合の復旧フック）。
func TestRebuildIfNeededStillHandlesEmptyDB(t *testing.T) {
	setupUploadTest(t, "000001", page.PageMeta{Owner: "alice", Mode: "330"})
	seedMasterPages(t, "000001", "000002")

	if _, err := database.DB.Exec(`DELETE FROM pages`); err != nil {
		t.Fatalf("pages の削除エラー: %v", err)
	}
	if err := RebuildIfNeeded(); err != nil {
		t.Fatalf("RebuildIfNeededエラー: %v", err)
	}
	if n := countPages(t); n != 2 {
		t.Errorf("空のDBから再構築されていません: pages=%d (期待 2)", n)
	}
}
