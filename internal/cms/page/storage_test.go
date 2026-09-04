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

// TestGeneratedAttachmentIDIsBlockShaped は、添付の識別子が**ブロックIDと同じ形**
// （4桁の base36）で、かつ**同じページの中で衝突しない**ことを固定します。
//
// 参照 `ページID-ID` が飛ぶ先は常に本文のブロックなので、添付だけ別の採番規則に
// する理由がありません（2026-09-04 ユーザー:「ファイル名とブロックのidを一緒にしては？」）。
// 揃えた結果、**一意でなければならない範囲が2つ**になりました——`files/` の中と、
// 本文の data-id です。
func TestGeneratedAttachmentIDIsBlockShaped(t *testing.T) {
	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })

	const pageID = "000123"
	if err := os.MkdirAll(AttachmentDir(pageID), 0o755); err != nil {
		t.Fatalf("MkdirAllエラー: %v", err)
	}

	id := GeneratedAttachmentID(pageID, ".pdf")
	if len(id) != 4 {
		t.Errorf("識別子がブロックIDと同じ4桁ではありません: %q", id)
	}
	for _, r := range id {
		if !('0' <= r && r <= '9') && !('a' <= r && r <= 'z') {
			t.Errorf("base36 小文字ではない文字が入っています: %q", id)
		}
	}

	// **拡張子が違っても同じ識別子は使えない**——同居すると本文に同じ data-id が
	// 2つ生まれる（`a7k2.pdf` と `a7k2.dxf`）。
	if err := os.WriteFile(filepath.Join(AttachmentDir(pageID), id+".dxf"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFileエラー: %v", err)
	}
	if got := GeneratedAttachmentID(pageID, ".pdf"); got == id {
		t.Errorf("拡張子違いの既存ファイルと衝突しました: %q", got)
	}

	// **本文の data-id とも衝突しない**（ブロックの採番と同じ名前空間に入ったため）。
	os.Remove(filepath.Join(AttachmentDir(pageID), id+".dxf"))
	body := `<p data-id="` + id + `">既存のブロック</p>`
	if err := os.WriteFile(filepath.Join(GetPageDir(pageID), pageID+".html"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFileエラー: %v", err)
	}
	if got := GeneratedAttachmentID(pageID, ".pdf"); got == id {
		t.Errorf("本文のブロックIDと衝突しました: %q", got)
	}
}
