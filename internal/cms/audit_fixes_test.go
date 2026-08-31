package cms

// 2026-08-05 監査で「脆弱性ではないが直す価値がある」とされた3点のうち、
// テストで固定できる2点（JSONボディの上限・ページIDの正規化）を検証します。
// 残る1点（/assets/ の Cache-Control）は main.go のルーティング層のため
// ここでは扱いません（cmd/w-cms/main.go のコメント参照）。

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// TestNormalizeID は page_id 文字列の正規化規則を固定します。
func TestNormalizeID(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"12", "000012", true},
		{"0012", "000012", true},   // ゼロ詰めの揺れを吸収する
		{"+12", "000012", true},    // Atoi が通す表記も正規形へ
		{"000012", "000012", true}, // 正規形はそのまま
		{"1234567", "1234567", true}, // 6桁を超えるIDは桁を保つ
		{"-1", "", false},
		{"12a", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := page.NormalizeID(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeID(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestSaveNormalizesPageID は、揺れた表記（"0012"）で保存しても正本が
// 正規のディレクトリ（data/master/00/000012/）へ書かれることを検証します。
// 修正前は data/master/00/0012/ という別ディレクトリができていた。
func TestSaveNormalizesPageID(t *testing.T) {
	setupSaveTest(t)
	if err := page.WriteSidecar("000012", page.PageMeta{Owner: "tester", Mode: "330"}); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}
	if err := SyncIndex("000012", "<h1>正規化テスト</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	got := postSave(t, "0012", "<h1>正規化テスト</h1><p>更新</p>")
	if got["page_id"] != "000012" {
		t.Errorf("応答の page_id が正規化されていません: %v", got["page_id"])
	}

	normPath := filepath.Join("data", "master", "00", "000012", "000012.html")
	if _, err := os.Stat(normPath); err != nil {
		t.Errorf("正規のパスに正本がありません: %v", err)
	}
	if _, err := os.Stat(filepath.Join("data", "master", "00", "0012")); !os.IsNotExist(err) {
		t.Errorf("揺れた表記のディレクトリ data/master/00/0012 が作られています")
	}
}

// TestSaveRejectsHugeJSONBody は、上限（8MiB）を超えるJSONボディが 413 で
// 拒否されることを検証します。上限が無いと認証済み利用者がメモリを圧迫できる。
func TestSaveRejectsHugeJSONBody(t *testing.T) {
	setupSaveTest(t)

	huge := strings.Repeat("あ", (8<<20)/3+1<<20) // UTF-8で確実に 8MiB 超
	payload, _ := json.Marshal(map[string]string{"page_id": "000012", "html": huge})
	req := httptest.NewRequest("POST", "/api/save", strings.NewReader(string(payload)))
	req = auth.WithUser(req, &auth.User{Username: "tester", IsAdmin: true})

	rr := httptest.NewRecorder()
	SaveAPIHandler(rr, req)
	if rr.Code != 413 {
		t.Errorf("巨大ボディが拒否されていません: status=%d", rr.Code)
	}
}

// TestUploadNormalizesPageID は、添付アップロードでも page_id が正規化され、
// 正規のディレクトリへ保存されることを検証します。
func TestUploadNormalizesPageID(t *testing.T) {
	setupUploadTest(t, "000012", page.PageMeta{Owner: "tester", Mode: "330"})
	u := &auth.User{Username: "tester"}

	rr := postUpload(t, "0012", "spec.pdf", []byte("%PDF-1.7 test"), u)
	if rr.Code != 200 {
		t.Fatalf("アップロードが失敗しました: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join("data", "master", "00", "000012", "files", "spec.pdf")); err != nil {
		t.Errorf("正規のディレクトリに添付がありません: %v", err)
	}
	if _, err := os.Stat(filepath.Join("data", "master", "00", "0012")); !os.IsNotExist(err) {
		t.Errorf("揺れた表記のディレクトリが作られています")
	}
}
