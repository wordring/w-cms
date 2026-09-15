package cms

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// ── ファイル表示（section[data-type="file-view" data-ref]）─────────────────
//
// 配線は**属性1つ**です（2026-09-15 に中の参照タグから移した）。ここで固定するのは
// 「何を見てファイルを決めるか」と、**決められなかったときに黙らないこと**。

// fileViewFixture は参照先のページと添付を1つ作ります。
func fileViewFixture(t *testing.T, hostID, attachName string) {
	t.Helper()
	setupUploadTest(t, hostID, page.PageMeta{Owner: "alice", Mode: "777"})
	dir := filepath.Join(page.GetPageDir(hostID), "files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("添付ディレクトリを作れません: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, attachName), []byte("%PDF-1.4 ダミー"), 0o644); err != nil {
		t.Fatalf("添付を作れません: %v", err)
	}
}

// renderFileViewBody は閲覧者つきで鏡を走らせます。
func renderFileViewBody(t *testing.T, viewer *auth.User, pageIDInt int, body string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/000001", nil)
	if viewer != nil {
		req = auth.WithUser(req, viewer)
	}
	return RenderComputedViews(req, pageIDInt, body)
}

// TestFileViewOpensAttributeRef は、**属性が指すファイルを開く**ことを固定します。
//
// もとは中の `<dl>` の「最初の参照」を読んでいました。やめたのは、同じ値が
// 「届いた記録」と「表示先の指定」の2つの意味で並び、しかもタグ名に意味が
// 無かったからです（`受信元` でも `あ` でも動いた）。
func TestFileViewOpensAttributeRef(t *testing.T) {
	fileViewFixture(t, "000001", "c3p7.pdf")

	body := `<h1>部品</h1><section data-type="file-view" data-ref="000001-c3p7"></section>`
	out := renderFileViewBody(t, &auth.User{Username: "alice", IsAdmin: true}, 1, body)

	for _, want := range []string{
		`data-type="file-view"`, `data-ref="000001-c3p7"`, // マーカーは保存内容のまま
		`class="vocab-chrome"`, `contenteditable="false"`,
		`type="application/pdf"`, `src="/000001/c3p7.pdf`, // 機械が組んだURL
	} {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q がありません:\n%s", want, out)
		}
	}
}

// TestFileViewIgnoresInnerTags は、**中に書いた参照タグでは開かない**ことを固定します。
//
// 逆向きの固定です——「最初の `dl` を読む」当てずっぽうへ戻ると、また同じ値が
// 2つの意味で並びます。属性が無ければ開かない、が新しい約束です。
func TestFileViewIgnoresInnerTags(t *testing.T) {
	fileViewFixture(t, "000001", "c3p7.pdf")

	body := `<section data-type="file-view">` +
		`<dl data-type="tags"><dt>受信元</dt><dd>000001-c3p7</dd></dl></section>`
	out := renderFileViewBody(t, &auth.User{Username: "alice", IsAdmin: true}, 1, body)

	if strings.Contains(out, "application/pdf") {
		t.Errorf("中のタグでファイルが開いています（配線は属性だけのはず）:\n%s", out)
	}
	// **黙りません**——何をすればよいかを画面に出します。
	if !strings.Contains(out, "参照がありません") {
		t.Errorf("参照が無いことを知らせていません:\n%s", out)
	}
}

// TestFileViewOpensAnyKind は、**PDF以外も開ける**ことを固定します
// （2026-09-15 ユーザー:「PDFに限らず表示できるほうが良い」）。
func TestFileViewOpensAnyKind(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"c3p7.pdf", `type="application/pdf"`},
		{"c3p8.png", `<img class="file-view-body"`},
		{"c3p9.mp4", `<video class="file-view-body"`},
		{"c3pa.mp3", `<audio class="file-view-body"`},
		// **テキストは「描ける」に入れません**（2026-09-15 のコードレビュー #1）。
		// 配信が octet-stream＋attachment なので `<embed>` は描けず、開く口を出すのが正。
		{"c3pb.csv", `<a href="/000001/c3pb.csv">`},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			id := strings.TrimSuffix(tc.file, filepath.Ext(tc.file))
			fileViewFixture(t, "000001", tc.file)
			body := `<section data-type="file-view" data-ref="000001-` + id + `"></section>`
			out := renderFileViewBody(t, &auth.User{Username: "alice", IsAdmin: true}, 1, body)
			if !strings.Contains(out, tc.want) {
				t.Errorf("%s が開けていません（%q が無い）:\n%s", tc.file, tc.want, out)
			}
		})
	}
}

// TestFileViewUnrenderableOffersLink は、**ブラウザで描けない形式でも口を出す**ことを
// 固定します。
//
// 前は「見つかりません」と言っていました——**嘘です**。ファイルは在るのですから、
// 開く口を出すのが正しい答えです（2026-09-15）。
func TestFileViewUnrenderableOffersLink(t *testing.T) {
	fileViewFixture(t, "000001", "c3p7.dxf")

	body := `<section data-type="file-view" data-ref="000001-c3p7"></section>`
	out := renderFileViewBody(t, &auth.User{Username: "alice", IsAdmin: true}, 1, body)

	if strings.Contains(out, "見つかりません") {
		t.Errorf("在るファイルを「見つかりません」と言っています:\n%s", out)
	}
	if !strings.Contains(out, `href="/000001/c3p7.dxf"`) {
		t.Errorf("開く口がありません:\n%s", out)
	}
}

// TestFileViewHidesUnreadable は、**読めないページの添付は出さない**ことを固定します。
// URLを機械が組むこととは別に、認可そのものをここで通します。
func TestFileViewHidesUnreadable(t *testing.T) {
	setupUploadTest(t, "000001", page.PageMeta{Owner: "alice", Mode: "700"})
	dir := filepath.Join(page.GetPageDir("000001"), "files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("添付ディレクトリを作れません: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c3p7.pdf"), []byte("x"), 0o644); err != nil {
		t.Fatalf("添付を作れません: %v", err)
	}

	body := `<section data-type="file-view" data-ref="000001-c3p7"></section>`
	out := renderFileViewBody(t, &auth.User{Username: "bob"}, 1, body)

	if strings.Contains(out, "c3p7.pdf") {
		t.Errorf("読めない相手にファイルが出ています:\n%s", out)
	}
}

// TestFileViewRejectsPageOnlyRef は、**ページ全体への参照では開かない**ことを固定します
// （`010272` はファイルを1つに決めません）。
func TestFileViewRejectsPageOnlyRef(t *testing.T) {
	fileViewFixture(t, "000001", "c3p7.pdf")

	body := `<section data-type="file-view" data-ref="000001"></section>`
	out := renderFileViewBody(t, &auth.User{Username: "alice", IsAdmin: true}, 1, body)

	if strings.Contains(out, "application/pdf") {
		t.Errorf("ページ参照でファイルが開いています:\n%s", out)
	}
	if !strings.Contains(out, "ファイルの参照ではありません") {
		t.Errorf("理由を知らせていません:\n%s", out)
	}
}
