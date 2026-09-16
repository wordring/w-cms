package cms

// データの初期化のテスト（2026-09-16）。
//
// 固定するのは**取り消せること**です——「初期化」は取り返しのつかない操作に
// 見えますが、控えが1手で戻せる形で残っていれば、間違えて押しても助かります。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// TestResetDataKeepsRestorableBackup は、**控えが1手で戻せる形**であることを
// 固定します。
//
// 最初の実装は `data/master` を控えの名前へ改名していたので、中身が
// `<控え>/00/…` と `<控え>/trash` の並びになりました。**戻すにはシャードを
// 1つずつ動かすことになり**、「取り消せる」と言えない形でした。
// いまは `<控え>/master` を `data/master` へ動かすだけで戻せます。
func TestResetDataKeepsRestorableBackup(t *testing.T) {
	newTestFileDB(t) // Chdir も済む（一時ディレクトリ）

	// ページを2枚とゴミ箱を1件、作ったことにする。
	for _, id := range []string{"000000", "000123"} {
		dir := page.GetPageDir(id)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".html"),
			[]byte("<h1>"+id+"</h1>"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join("data", "trash", "000999"), 0755); err != nil {
		t.Fatal(err)
	}

	sum, err := ResetData("alice")
	if err != nil {
		t.Fatalf("初期化できません: %v", err)
	}
	if sum.Pages != 2 {
		t.Errorf("控えへ移したページ数が違います: %d（2 を期待）", sum.Pages)
	}
	if !strings.Contains(sum.BackupDir, "_reset-") {
		t.Errorf("控えの場所が返りません: %q", sum.BackupDir)
	}

	// ① **1手で戻せる形**——控えの中で元の名前のまま。
	if _, err := os.Stat(filepath.Join(sum.BackupDir, "master", "00", "000123")); err != nil {
		t.Errorf("控えの中が <控え>/master/… になっていません: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sum.BackupDir, "trash", "000999")); err != nil {
		t.Errorf("ゴミ箱が控えに入っていません: %v", err)
	}

	// ② **消えている**（正本の側は空）。
	if n := countPageDirs(page.MasterDir); n != 0 {
		t.Errorf("ページが残っています: %d枚", n)
	}
	// ③ **置き場は在る**——無いままだと、次にページを作るとき転びます。
	if fi, err := os.Stat(page.MasterDir); err != nil || !fi.IsDir() {
		t.Errorf("data/master が作り直されていません: %v", err)
	}
	// ④ **残すものは残っている**（この試験では auth.db などは作っていないので、
	//    消しにいっていないことだけを見ます）。
	if _, err := os.Stat(sum.BackupDir); err != nil {
		t.Errorf("控えそのものが消えています: %v", err)
	}
	if !sum.Rebuilt {
		t.Error("派生索引を作り直していません（消したページが一覧に出続けます）")
	}
	if !strings.Contains(sum.KeptNotice, "トークン") {
		t.Errorf("残したものの案内が空です: %q", sum.KeptNotice)
	}
}

// TestResetDataOnEmptyDataDir は、**まだ何も無い環境でも転ばない**ことを固定します
// （初回起動の直後に押す場合）。
func TestResetDataOnEmptyDataDir(t *testing.T) {
	newTestFileDB(t)
	if err := os.RemoveAll(page.MasterDir); err != nil {
		t.Fatal(err)
	}
	sum, err := ResetData("alice")
	if err != nil {
		t.Fatalf("空の環境で転びました: %v", err)
	}
	if sum.Pages != 0 {
		t.Errorf("ページ数が0ではありません: %d", sum.Pages)
	}
	if fi, err := os.Stat(page.MasterDir); err != nil || !fi.IsDir() {
		t.Errorf("data/master を作り直していません: %v", err)
	}
}
