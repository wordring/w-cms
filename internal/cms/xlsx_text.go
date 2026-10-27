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
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	xlsxPartLimit  = 20 << 20 // 1つの XML の展開の上限
	xlsxTotalLimit = 60 << 20 // 全部の XML の展開の合計の上限（2026-10-27 の見直し）
	xlsxTextLimit  = 200000   // 出力の文字数の上限
	xlsxSheetLimit = 50       // 読むシートの数の上限（同じファイルを指すシートは1つに数える）
	xlsxCellLimit  = 500000   // 読むセルの数の上限（空のセルも数える）
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
	// ⚠ 2026-10-27 の見直し: 同じシートのファイルを指す並びを数千入れると、20 MB の XML を何千回も展開して、21 KB のファイルで
	// 何分も止まった。展開の合計にも上限を置き、同じファイルは1回だけ読む。
	var total int64
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
		if total += int64(len(data)); total > xlsxTotalLimit {
			return nil, errors.New("表計算のファイルの中身が大きすぎます")
		}
		return data, nil
	}

	shared, err := xlsxSharedStrings(read)
	if err != nil {
		return "", err
	}
	// シートは、ブックの並び（workbook.xml）と、その r:id が指すファイル（workbook.xml.rels）で引く（2026-10-26。それまでは
	// sheetN.xml の番号の順と名前の並びを順に当てていて、番号の飛んだファイル・並べ替えたブックでは名前を取り違えた）。
	// 引けなければ、いままでどおり番号の順に読む。
	sheets := xlsxSheetsByRels(read, files)
	if len(sheets) == 0 {
		names := xlsxSheetNames(read)
		var paths []string
		for name := range files {
			if xlsxSheetRe.MatchString(name) {
				paths = append(paths, name)
			}
		}
		sort.Slice(paths, func(i, j int) bool { return xlsxSheetNo(paths[i]) < xlsxSheetNo(paths[j]) })
		for i, p := range paths {
			name := p
			if i < len(names) {
				name = names[i]
			}
			sheets = append(sheets, xlsxSheet{Name: name, Path: p})
		}
	}
	if len(sheets) == 0 {
		return "", errors.New("シートがありません")
	}

	var out strings.Builder
	seen := map[string]bool{}
	cells := 0
	for _, sh := range sheets {
		if seen[sh.Path] {
			continue
		}
		seen[sh.Path] = true
		if len(seen) > xlsxSheetLimit {
			out.WriteString("…（シートが多いので途中まで）\n")
			break
		}
		data, err := read(sh.Path)
		if err != nil {
			return "", err
		}
		name := sh.Name
		out.WriteString("=== シート " + name + "\n")
		if err := xlsxWriteRows(&out, data, shared, &cells); err != nil {
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

// xlsxSheet はシート1つの名前と、中身のファイルの場所です。
type xlsxSheet struct{ Name, Path string }

// xlsxSheetsByRels は、ブックの並びのシートを、名前と中身のファイル（workbook.xml.rels で r:id を引いた先）で返します。
// どれか1つでも引けなければ空（呼ぶ側が番号の順に読む）。
func xlsxSheetsByRels(read func(string) ([]byte, error), files map[string]*zip.File) []xlsxSheet {
	wb, err := read("xl/workbook.xml")
	if err != nil || wb == nil {
		return nil
	}
	var doc struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			RID  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if xml.Unmarshal(wb, &doc) != nil || len(doc.Sheets) == 0 {
		return nil
	}
	rels, err := read("xl/_rels/workbook.xml.rels")
	if err != nil || rels == nil {
		return nil
	}
	var rd struct {
		Rels []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if xml.Unmarshal(rels, &rd) != nil {
		return nil
	}
	target := map[string]string{}
	for _, r := range rd.Rels {
		t := r.Target
		if strings.HasPrefix(t, "/") {
			t = strings.TrimPrefix(t, "/") // 根からの書き方（/xl/worksheets/…）
		} else {
			t = path.Clean("xl/" + t) // xl/ からの書き方（worksheets/…）
		}
		target[r.ID] = t
	}
	var out []xlsxSheet
	for _, s := range doc.Sheets {
		p, ok := target[s.RID]
		if !ok || !xlsxSheetRe.MatchString(p) || files[p] == nil {
			return nil
		}
		out = append(out, xlsxSheet{Name: s.Name, Path: p})
	}
	return out
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

// xlsxWriteRows はシートの行を書きます。cells は、ここまでに読んだセルの数です（全部のシートで数える）。
//
// ⚠ 2026-10-27 の見直し: xml.Unmarshal で1枚を丸ごと構造に読むと、20 MB の <c/> の並びで数百 MB を取り、出力の上限も行ごとに
// しか見ていなかったので、長い文字を指すセルを1行に数千並べるだけで、1行を組むのに何 GB も取った。いまは1つずつ読んで、
// セルを書くたびに出力の長さを見る。セルの数（xlsxCellLimit）でも止める。
func xlsxWriteRows(out *strings.Builder, data []byte, shared []string, cells *int) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var (
		inRow, inCell, inV, inIS, inT bool
		cellR, cellT                  string
		val                           strings.Builder
		rowStarted                    bool
	)
	flushCell := func() {
		v := val.String()
		if cellT == "s" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 && n < len(shared) {
				v = shared[n]
			}
		}
		v = strings.TrimSpace(strings.ReplaceAll(v, "\n", " "))
		if v == "" {
			return
		}
		if rowStarted {
			out.WriteString(" | ")
		}
		rowStarted = true
		out.WriteString(strings.TrimRight(cellR, "0123456789") + "=")
		if room := xlsxTextLimit + 1 - out.Len(); len(v) > room && room >= 0 {
			v = v[:room]
		}
		out.WriteString(v)
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("シートを読めません: " + err.Error())
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				inRow, rowStarted = true, false
			case "c":
				if !inRow {
					continue
				}
				if *cells++; *cells > xlsxCellLimit {
					out.WriteString("\n…（セルが多いので途中まで）")
					return nil
				}
				inCell, cellR, cellT = true, "", ""
				val.Reset()
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "r":
						cellR = a.Value
					case "t":
						cellT = a.Value
					}
				}
			case "v":
				inV = inCell && cellT != "inlineStr"
			case "is":
				inIS = inCell
			case "t":
				inT = inIS
			}
		case xml.CharData:
			if (inV || inT) && val.Len() < xlsxTextLimit {
				val.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "t":
				inT = false
			case "is":
				inIS = false
			case "c":
				if inCell {
					flushCell()
				}
				inCell = false
			case "row":
				if rowStarted {
					out.WriteString("\n")
				}
				inRow = false
			}
			if out.Len() > xlsxTextLimit {
				return nil
			}
		}
	}
	return nil
}
