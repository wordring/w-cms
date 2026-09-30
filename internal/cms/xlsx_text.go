package cms

// ─────────────────────────────────────────────────────────────────────────
// 表計算（.xlsx）の中身を文字にする（2026-09-30）
//
// 最初の使い手は東邦の拡張の解析（利用者:「Excelの注文リストは法的に無効ですが、顧客との信頼関係で取り扱う場合が
// あります。ハッキリと受注したとわかる場合は、受注ページにして良いと思います」）——Gemini は .xlsx をそのまま受けない
// ので、シートを文字にして渡す。**どの業務でも「表計算の添付を読む」は起きる**のでコアに置く（開発方針 §0）。
//
// 外部の部品を使わず標準ライブラリだけで読む（開発方針 §1）——.xlsx は ZIP の中の XML:
//
//	xl/workbook.xml          シートの名前と並び
//	xl/sharedStrings.xml     文字のセルの中身（セルは番号で指す）
//	xl/worksheets/sheetN.xml 行とセル（`<c r="B3" t="s"><v>12</v></c>`・`t="inlineStr"` は `<is><t>`）
//
// ⚠ 数式は計算しない（保存されている値 `<v>` を読む）。日付は数（シリアル値）のまま。書式・結合セルは見ない。
// ⚠ **大きすぎるものは途中で止める**——1つの部品の展開は `xlsxPartLimit` まで、出力は `xlsxTextLimit` 文字まで
// （ZIP の圧縮爆弾・巨大な表で止まらないため）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	xlsxPartLimit = 20 << 20 // 1つの XML の展開の上限
	xlsxTextLimit = 200000   // 出力の文字数の上限
)

// XLSXText は .xlsx の全シートを「=== シート 名前」と「A=値 | B=値」の行で返します（空のセル・空の行は書かない）。
func XLSXText(b []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return "", errors.New("表計算のファイルとして読めません: " + err.Error())
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	read := func(name string) ([]byte, error) {
		f, ok := files[name]
		if !ok {
			return nil, nil
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, xlsxPartLimit+1))
		if err != nil {
			return nil, err
		}
		if len(data) > xlsxPartLimit {
			return nil, errors.New(name + " が大きすぎます")
		}
		return data, nil
	}

	shared, err := xlsxSharedStrings(read)
	if err != nil {
		return "", err
	}
	names := xlsxSheetNames(read)
	var sheets []string
	for name := range files {
		if xlsxSheetRe.MatchString(name) {
			sheets = append(sheets, name)
		}
	}
	if len(sheets) == 0 {
		return "", errors.New("シートがありません")
	}
	sort.Slice(sheets, func(i, j int) bool { return xlsxSheetNo(sheets[i]) < xlsxSheetNo(sheets[j]) })

	var out strings.Builder
	for i, s := range sheets {
		data, err := read(s)
		if err != nil {
			return "", err
		}
		name := s
		if i < len(names) {
			name = names[i]
		}
		out.WriteString("=== シート " + name + "\n")
		if err := xlsxWriteRows(&out, data, shared); err != nil {
			return "", err
		}
		if out.Len() > xlsxTextLimit {
			return xlsxTruncate(out.String()), nil
		}
	}
	return out.String(), nil
}

var xlsxSheetRe = regexp.MustCompile(`^xl/worksheets/sheet(\d+)\.xml$`)

func xlsxSheetNo(name string) int {
	n, _ := strconv.Atoi(xlsxSheetRe.FindStringSubmatch(name)[1])
	return n
}

func xlsxTruncate(s string) string {
	r := []rune(s)
	if len(r) > xlsxTextLimit {
		r = r[:xlsxTextLimit]
	}
	return string(r) + "\n…（長いので途中まで）"
}

// xlsxSharedStrings は文字のセルの中身を番号順に返します（ふりがな `<rPh>` は読まない）。
func xlsxSharedStrings(read func(string) ([]byte, error)) ([]string, error) {
	data, err := read("xl/sharedStrings.xml")
	if err != nil || data == nil {
		return nil, err
	}
	var doc struct {
		SI []struct {
			T string `xml:"t"`
			R []struct {
				T string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, errors.New("文字の表を読めません: " + err.Error())
	}
	out := make([]string, len(doc.SI))
	for i, si := range doc.SI {
		s := si.T
		for _, r := range si.R {
			s += r.T
		}
		out[i] = s
	}
	return out, nil
}

// xlsxSheetNames はシートの名前を並び順に返します（読めなければ空）。
func xlsxSheetNames(read func(string) ([]byte, error)) []string {
	data, err := read("xl/workbook.xml")
	if err != nil || data == nil {
		return nil
	}
	var doc struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
		} `xml:"sheets>sheet"`
	}
	if xml.Unmarshal(data, &doc) != nil {
		return nil
	}
	out := make([]string, len(doc.Sheets))
	for i, s := range doc.Sheets {
		out[i] = s.Name
	}
	return out
}

// xlsxWriteRows はシートの行を書きます。
func xlsxWriteRows(out *strings.Builder, data []byte, shared []string) error {
	var doc struct {
		Rows []struct {
			C []struct {
				R  string `xml:"r,attr"`
				T  string `xml:"t,attr"`
				V  string `xml:"v"`
				IS struct {
					T string `xml:"t"`
					R []struct {
						T string `xml:"t"`
					} `xml:"r"`
				} `xml:"is"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return errors.New("シートを読めません: " + err.Error())
	}
	for _, row := range doc.Rows {
		var cells []string
		for _, c := range row.C {
			v := c.V
			switch c.T {
			case "s":
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 && n < len(shared) {
					v = shared[n]
				}
			case "inlineStr":
				v = c.IS.T
				for _, r := range c.IS.R {
					v += r.T
				}
			}
			v = strings.TrimSpace(strings.ReplaceAll(v, "\n", " "))
			if v == "" {
				continue
			}
			col := strings.TrimRight(c.R, "0123456789")
			cells = append(cells, col+"="+v)
		}
		if len(cells) > 0 {
			out.WriteString(strings.Join(cells, " | ") + "\n")
		}
		if out.Len() > xlsxTextLimit {
			return nil
		}
	}
	return nil
}
