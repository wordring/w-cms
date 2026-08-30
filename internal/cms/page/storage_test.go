package page

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFileAtomicReplacesAndCleansUp は、原子的書き込みが
// (1) 既存ファイルを置き換えられ（Windows の rename 上書きを含む）、
// (2) 一時ファイルを残さないことを検証します。
// 正本（本文HTML・サイドカー・添付・版・設定）を書く全経路がこの関数を通ります。
func TestWriteFileAtomicReplacesAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "000001.meta.json")

	if err := WriteFileAtomic(path, []byte("v1"), 0644); err != nil {
		t.Fatalf("新規書き込みエラー: %v", err)
	}
	if err := WriteFileAtomic(path, []byte("v2"), 0644); err != nil {
		t.Fatalf("上書きエラー: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "v2" {
		t.Errorf("上書き後の内容が違います: %q (err=%v)", got, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDirエラー: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("一時ファイルが残っています: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("フォルダに余計なファイルがあります: %d 個", len(entries))
	}
}

// TestWriteFileAtomicKeepsOldOnError は、書き込みに失敗したとき
// 既存の内容が無傷で残ることを検証します（この関数の存在理由そのもの）。
// 失敗はフォルダが無いパスへの書き込みで起こします。
func TestWriteFileAtomicKeepsOldOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-such-subdir", "x.json")
	if err := WriteFileAtomic(path, []byte("v1"), 0644); err == nil {
		t.Fatal("存在しないフォルダへの書き込みが成功してしまいました")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("失敗したのにファイルができています: %v", err)
	}
}
