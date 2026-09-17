package cms

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// ZIP展開のテスト。固定するのは:
//   - 中のファイルが**フォルダを含むパス**で返る（フォルダ名が装置名称を含む実データのため）
//   - 中の ZIP・許可外の拡張子・申告より大きい中身は**理由つきで**飛ばす（黙って落とさない）
//   - 合計バイトの上限で打ち切る（ZIP爆弾の蓋）
//   - 名前の復号: Shift_JIS（フラグ無し）・UTF-8（フラグ有り）・**UTF-8 なのにフラグ無し**

func zipBytes(t *testing.T, build func(*zip.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	build(zw)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExpandZipKeepsFoldersAndSkipsWithReason(t *testing.T) {
	z := zipBytes(t, func(zw *zip.Writer) {
		w, _ := zw.Create("Q055-図面/R310-002_本体.pdf")
		w.Write([]byte("%PDF-1.4 a"))
		w, _ = zw.Create("Q055-図面/部品.dxf")
		w.Write([]byte("0\nSECTION\n"))
		w, _ = zw.Create("Q055-図面/inner.zip")
		w.Write([]byte("PK"))
		w, _ = zw.Create("readme.html")
		w.Write([]byte("<b>x</b>"))
		zw.Create("Q055-図面/empty-dir/") // フォルダ行は無視
	})
	allow := func(name string) bool { return AttachmentExtAccepted(name[strings.LastIndex(name, "."):]) }
	members, skipped, err := ExpandZip(z, allow)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range members {
		names = append(names, m.Name)
	}
	if got := strings.Join(names, "|"); got != "Q055-図面/R310-002_本体.pdf|Q055-図面/部品.dxf" {
		t.Errorf("取り出した中身が違います: %q", got)
	}
	if string(members[0].Content) != "%PDF-1.4 a" {
		t.Errorf("中身が違います: %q", members[0].Content)
	}
	reasons := map[string]string{}
	for _, s := range skipped {
		reasons[s.Name] = s.Reason
	}
	if reasons["Q055-図面/inner.zip"] != "中のZIPは展開しません" {
		t.Errorf("中のZIPを飛ばしていません: %v", skipped)
	}
	if reasons["readme.html"] != "受け付けない形式" {
		t.Errorf("許可外の拡張子を飛ばしていません: %v", skipped)
	}
	if len(skipped) != 2 {
		t.Errorf("飛ばした件数が違います: %v", skipped)
	}
}

func TestExpandZipSkipsOversized(t *testing.T) {
	limit := MaxUploadBytes()
	z := zipBytes(t, func(zw *zip.Writer) {
		w, _ := zw.Create("big.dxf")
		w.Write(bytes.Repeat([]byte("0"), int(limit)+1))
		w, _ = zw.Create("ok.dxf")
		w.Write([]byte("0\nEOF\n"))
	})
	members, skipped, err := ExpandZip(z, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].Name != "ok.dxf" {
		t.Errorf("大きすぎる中身を飛ばしていません: %v", members)
	}
	if len(skipped) != 1 || skipped[0].Reason != "大きすぎます" {
		t.Errorf("理由が違います: %v", skipped)
	}
}

func TestExpandZipRejectsBroken(t *testing.T) {
	if _, _, err := ExpandZip([]byte("not a zip"), nil); err == nil {
		t.Error("壊れたZIPでエラーになりません")
	}
}

func TestDecodeZipNameHandlesAllThreeCases(t *testing.T) {
	sjis, _, err := transform.String(japanese.ShiftJIS.NewEncoder(), "図面/支持金具.pdf")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name     string
		nonUTF8  bool
		want     string
		whatever string
	}{
		{sjis, true, "図面/支持金具.pdf", "Windows の右クリック圧縮（Shift_JIS・フラグ無し）"},
		{"図面/支持金具.pdf", false, "図面/支持金具.pdf", "Mac/Unix（UTF-8・フラグ有り）"},
		{"図面/支持金具.pdf", true, "図面/支持金具.pdf", "古いツール（UTF-8 なのにフラグ無し）"},
		{"plain.pdf", true, "plain.pdf", "ASCII だけ"},
	} {
		if got := DecodeZipName(c.name, c.nonUTF8); got != c.want {
			t.Errorf("%s: %q, want %q", c.whatever, got, c.want)
		}
	}
}
