package main

// 図面の下ごしらえ（2026-09-28）——印刷イメージをベクターの PDF にし、Gemini で表題欄を読み、
// 添付の PDF と同じ図面の印刷イメージは移さない。
//
// 利用者:「図面のPNGについてですが、これもGEMINIで解析して、添付されているPDFもGEMINIで解析して、
// 図番や品名などが一致したら、PDFの方を採用して、PNGは移植しないでください」「PNGの図面はワンノートでは
// XPS形式で記録されています。これをベクターのままPDFに変換できませんか？」。
//
//   - 印刷イメージの元は XPS の束（吸い出しが XPS\ に置く）。mutool で束の1ページだけを PDF にし、
//     PDF化\ に置いて使い回す（⚠ mutool は AGPL なので同梱しない——移植の作業でだけ使う）。
//   - Gemini の答えは Gemini\ にファイルの中身の要約（SHA-256）で控え、**同じファイルで二度呼ばない**
//     （製造は何度でもやり直すので）。
//   - 図面番号・図面名称・装置名称は図面の表題欄から入れる（利用者:「すべて、Geminiで図番、装置名称、
//     品名などを確認して入れます」）。ワンノートと違えば報告に出す。

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"golang.org/x/net/html"

	"w-cms/internal/cms"
)

// drawingRead は表題欄から読んだ1つの図面です。
type drawingRead struct {
	No       string `json:"drawing_no"`
	Name     string `json:"drawing_name"`
	Machine  string `json:"machine_name"`
	Customer string `json:"customer"`
}

type judgment struct {
	IsDrawing bool          `json:"is_drawing"`
	Drawings  []drawingRead `json:"drawings"`
	File      string        `json:"ファイル"` // 控えを人が読むための元の名前
}

// drawingPrompt は表題欄を読む頼み文です（移植の道具の持ち物——解析の頼み文より狭く、図面だけを読む）。
//
// ⚠ JSON の鍵と drawingRead・judgment のタグは一対一（片方だけ直すと、その項目は黙って空になる）。
const drawingPrompt = `この画像またはPDFが、部品や製品の図面（表題欄に図面番号・図面名称があるもの）かを判定し、図面なら表題欄を読んでください。
次の形式のJSONオブジェクトのみを出力してください（マークダウンのコードブロック修飾は付けない）:
{
  "is_drawing": true または false,
  "drawings": [{"drawing_no": "図面番号", "drawing_name": "図面名称", "machine_name": "装置名称", "customer": "客先"}]
}
⚠ 1つのファイルに複数の図面が入っていることがあります（ページごとに別の図面、あるいは1ページに部品図と組立図）。
その場合は drawings に図面の数だけ要素を入れてください。図面でなければ空配列にします。
drawings の各項目は次のとおりです:
  - drawing_no   : 表題欄の図面番号。**書かれている文字列をそのまま**返してください（ハイフンや記号を補ったり省いたりしない）。記載が無ければ空文字
  - drawing_name : 表題欄の図面名称（記載が無ければ空文字）
  - machine_name : 表題欄の装置名称（その部品が使われる装置・機械の名前。記載が無ければ空文字）
  - customer     : 図面を作った会社（発注元）の名前（記載が無ければ空文字）`

// env は製造の道具の持ち物です（図面の下ごしらえと、同じ図面の見張り）。
type env struct {
	root   string
	mutool string
	gemini bool
	fresh  bool // 前に作った「移行中」のページをごみ箱へ移して作り直す
	db     *sql.DB // w-cms の索引（読むだけ・同じ図面番号のページを探す）
	// seen はこの回に作った図面番号（正規化）→ ワンノートの題。
	seen map[string]string
	// machineNotes はこの回に書いた装置のページ → ワンノートの題（同じ装置のページに2つ書かない）。
	machineNotes map[string]string
	// tableDest は人が決めた表の行き先（製造の設定.json の「表の行き先」・tables.go）。
	tableDest map[string]map[string][]string

	mutoolMissing bool
	geminiOff     bool
}

var errNoMutool = errors.New("mutool がありません")

// vectorPDF は XPS の束の1ページを PDF にします（PDF化\ に置いて使い回す）。
func (e *env) vectorPDF(xps string, page int) (string, error) {
	if e.mutool == "" {
		return "", errNoMutool
	}
	if _, err := os.Stat(e.mutool); err != nil {
		e.mutoolMissing = true
		return "", errNoMutool
	}
	src := filepath.Join(e.root, "XPS", xps)
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("XPS の束 %s がありません（吸い出し直してください）", xps)
	}
	dir := filepath.Join(e.root, "PDF化")
	out := filepath.Join(dir, fmt.Sprintf("%s_p%d.pdf", strings.TrimSuffix(xps, ".xps"), page))
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		return out, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// convert で1ページだけ PDF にし、clean で小さくする（-gggg: 使っていない物を落とす・-z: 圧縮）。
	tmp := strings.TrimSuffix(out, ".pdf") + ".tmp.pdf"
	defer os.Remove(tmp)
	if b, err := exec.Command(e.mutool, "convert", "-o", tmp, src, fmt.Sprint(page)).CombinedOutput(); err != nil {
		return "", fmt.Errorf("mutool convert: %v %s", err, strings.TrimSpace(string(b)))
	}
	if b, err := exec.Command(e.mutool, "clean", "-gggg", "-z", tmp, out).CombinedOutput(); err != nil {
		os.Remove(out)
		return "", fmt.Errorf("mutool clean: %v %s", err, strings.TrimSpace(string(b)))
	}
	return out, nil
}

// read はファイルの表題欄を読みます（控えがあれば Gemini を呼ばない）。読めなければ ok=false。
func (e *env) read(path, shown string, note *pageNote) ([]drawingRead, bool) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil, false
	}
	sum := sha256.Sum256(b)
	cache := filepath.Join(e.root, "Gemini", hex.EncodeToString(sum[:16])+".json")
	var j judgment
	if readJSON(cache, &j) == nil {
		return j.Drawings, true
	}
	if !e.gemini {
		return nil, false
	}
	mime := "application/pdf"
	if !strings.EqualFold(filepath.Ext(path), ".pdf") {
		ext, ok := imageExt(path)
		if !ok {
			return nil, false
		}
		mime = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}[ext]
	}
	resp, err := cms.GeminiGenerate(drawingPrompt, genai.Blob{MIMEType: mime, Data: b})
	if errors.Is(err, cms.ErrNoGeminiKey) {
		e.geminiOff = true
		e.gemini = false
		return nil, false
	}
	if err != nil {
		note.warn("⚠ Gemini で " + shown + " を読めません: " + err.Error())
		return nil, false
	}
	if err := json.Unmarshal([]byte(cms.StripJSONFence(resp)), &j); err != nil {
		head := []rune(strings.TrimSpace(resp))
		if len(head) > 200 {
			head = append(head[:200], '…')
		}
		note.warn("⚠ Gemini の答えを読めません（" + shown + "）: " + string(head))
		return nil, false
	}
	if !j.IsDrawing {
		j.Drawings = nil
	}
	j.File = shown
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err == nil {
		writeJSON(cache, j) // 控え（失敗しても次に呼び直すだけ）
	}
	return j.Drawings, true
}

// normNo・normName は図面の突き合わせのための畳み方です。
func normNo(s string) string   { return cms.NormalizeCode(s) }
func normName(s string) string { return strings.Join(strings.Fields(cms.NormalizeNameForIngest(s)), "") }

func sameDrawing(a, b drawingRead) bool {
	if normNo(a.No) != normNo(b.No) || normName(a.Name) != normName(b.Name) {
		return false
	}
	return normNo(a.No) != "" || normName(a.Name) != ""
}

// prepare は1ページの図面の候補（印刷イメージ・添付の PDF）を下ごしらえします——印刷イメージは XPS から
// ベクターの PDF に、どちらも表題欄を読み、添付の PDF と同じ図面の印刷イメージには Skip を付けます。
func (e *env) prepare(dir string, secs []section, note *pageNote) {
	var attached []*item
	var prints []*item
	for si := range secs {
		for ii := range secs[si].Items {
			it := &secs[si].Items[ii]
			switch {
			case it.Kind == "image" && it.Print:
				// 先に PNG で図面かを読み、**移す図面だけ** XPS からベクターの PDF にする（下の終わり）——印刷
				// イメージには、スキャンした手書きの見積メモなども混ざり、それは PDF にしても画像のまま（得が無く、
				// 束によっては変換に数分・一時ファイルが 60MB）。PNG が取れていなければ PDF にしてから読む。
				path := ""
				if it.File != "" {
					path = filepath.Join(e.root, dir, "files", it.File)
				} else {
					e.toVector(it, note)
					path = it.Vector
				}
				if path == "" {
					continue
				}
				it.Reads, it.Judged = e.read(path, "印刷イメージ", note)
				prints = append(prints, it)
			case it.Kind == "file" && it.File != "" && strings.EqualFold(filepath.Ext(it.Name), ".pdf"):
				it.Reads, it.Judged = e.read(filepath.Join(e.root, dir, "files", it.File), it.Name, note)
				attached = append(attached, it)
			}
		}
	}
	for _, p := range prints {
		if !p.Judged || len(p.Reads) == 0 {
			continue
		}
		var same *item
		for _, a := range attached {
			all := true
			for _, r := range p.Reads {
				hit := false
				for _, ar := range a.Reads {
					if sameDrawing(r, ar) {
						hit = true
					}
				}
				all = all && hit
			}
			if all {
				same = a
				break
			}
		}
		if same != nil {
			p.Skip = "添付の「" + same.Name + "」と同じ図面"
			note.info("印刷イメージ（" + describe(p.Reads) + "）は添付の「" + same.Name + "」と同じ図面なので移していません")
			continue
		}
		for _, a := range attached {
			for _, r := range p.Reads {
				for _, ar := range a.Reads {
					if normNo(r.No) != "" && normNo(r.No) == normNo(ar.No) && normName(r.Name) != normName(ar.Name) {
						note.ask("印刷イメージと添付の PDF で、図面番号が同じなのに図面名称が違います——両方入れました")
					}
				}
			}
		}
	}
	// 移す印刷イメージのうち、図面は（読めなかったものも図面かもしれないので）ベクターの PDF に。
	for _, p := range prints {
		if p.Skip == "" && (!p.Judged || len(p.Reads) > 0) {
			e.toVector(p, note)
		}
	}
}

// toVector は印刷イメージを XPS の束からベクターの PDF にします（取れなければ PNG のまま）。
func (e *env) toVector(it *item, note *pageNote) {
	if it.XPS == "" || it.XPSPage <= 0 || it.Vector != "" {
		return
	}
	p, err := e.vectorPDF(it.XPS, it.XPSPage)
	switch {
	case err == nil:
		it.Vector = p
	case errors.Is(err, errNoMutool):
	default:
		note.warn("⚠ 印刷イメージを PDF にできません（PNG のまま入れます）: " + err.Error())
	}
}

func describe(rs []drawingRead) string {
	var parts []string
	for _, r := range rs {
		parts = append(parts, strings.TrimSpace(r.No+" "+r.Name))
	}
	return strings.Join(parts, "・")
}

// mainDrawing はページの図面を決めます——■図面 の候補から、ワンノートの図面番号と同じもの → 題と同じ名前の
// もの → 添付の PDF → 印刷イメージ の順。
func mainDrawing(secs []section, oneNo, oneName string) (drawingRead, bool) {
	var reads []drawingRead
	var fromPDF, fromPrint []drawingRead
	for _, s := range secs {
		if s.Name != "図面" {
			continue
		}
		for _, it := range s.Items {
			if !it.Judged || it.Skip != "" {
				continue
			}
			reads = append(reads, it.Reads...)
			if it.Kind == "file" {
				fromPDF = append(fromPDF, it.Reads...)
			} else {
				fromPrint = append(fromPrint, it.Reads...)
			}
		}
	}
	if oneNo != "" {
		for _, r := range reads {
			if normNo(r.No) == normNo(oneNo) {
				return r, true
			}
		}
	}
	for _, r := range reads {
		if normName(r.Name) == normName(oneName) {
			return r, true
		}
	}
	if len(fromPDF) > 0 {
		return fromPDF[0], true
	}
	if len(fromPrint) > 0 {
		return fromPrint[0], true
	}
	return drawingRead{}, false
}

// duplicate は同じ図面番号のページが既にあるかです（この回に作ったもの・w-cms の索引）。own は自分が前に
// 作ったページ（除く）。
func (e *env) duplicate(drawingNo, own string) string {
	key := normNo(drawingNo)
	if key == "" {
		return ""
	}
	if t, ok := e.seen[key]; ok {
		return "この回に先に作ったワンノートのページ「" + t + "」"
	}
	if e.db == nil {
		return ""
	}
	ids, err := cms.PagesByTagLoose(e.db, "図面番号", drawingNo)
	if err != nil {
		return ""
	}
	for _, id := range ids {
		s := fmt.Sprintf("%06d", id)
		if s != own {
			return "w-cms の /" + s
		}
	}
	return ""
}

// readsInclude はファイルから読んだ図面の中に、主な図面があるかです。
func readsInclude(reads []drawingRead, main drawingRead) bool {
	for _, r := range reads {
		if sameDrawing(r, main) {
			return true
		}
	}
	return false
}

// ownReads は1つのファイルから読んだ図面のうち、**このページのもの**だけを返します（2026-09-30）。others はそれ以外。
//
// 利用者:「一つのPDFに複数の図面が入っている場合もあり得ます」——一式（本体・側板の左右・カバーの左右）を1つの PDF に
// まとめた添付を、それまでは読んだ図面番号を全部このページの図面ブロックに並べていた。すると**別の加工製品の図面番号を
// このページが名乗り**、受注の行が違う品物のページに結ばれる（実データで、左右のカバーの右が本体のページに結ばれていた）。
//
// このページのもの: 主な図面と同じ図面番号／名前が主な図面の名前を含む（「○○(溶接指示図)」）。
// ⚠ ファイルの図面が1枚だけなら、それはこのページに添えられた図面なので全部このページのもの（部品図と溶接図を別の
// ファイルで添えることがある）。⚠ 主な図面が分からない・どれも当てはまらないときも全部返す（決められないので減らさない）。
func ownReads(reads []drawingRead, main drawingRead, hasMain bool) (own, others []drawingRead) {
	if len(reads) <= 1 || !hasMain {
		return reads, nil
	}
	mainNo, mainName := normNo(main.No), normName(main.Name)
	for _, r := range reads {
		sameNo := mainNo != "" && normNo(r.No) == mainNo
		sameName := mainName != "" && strings.Contains(normName(r.Name), mainName)
		if sameNo || sameName {
			own = append(own, r)
		} else {
			others = append(others, r)
		}
	}
	if len(own) == 0 {
		return reads, nil
	}
	return own, others
}

// fileViewHTML はファイル表示の印です（添付の参照1つ）。
func fileViewHTML(ref string) string {
	return `<section data-type="file-view" data-ref="` + stdhtml.EscapeString(ref) + `"></section>`
}

// extraDrawingBlock は2枚目からの図面のブロックを、**テンプレートの図面ブロックを写して**作ります（2026-09-29）。
//
// 図面1枚ごとにブロックを1つ——タグ（図面番号・図面名称）はそのファイルから読んだ値（1つのファイルに図面が
// 何枚もあれば、その数だけ並べる）、装置名称と客先はページと同じ。読めなかったファイルはタグを空欄のまま
// （人が書く）。返すノードはどの木にも付いていない（呼び手が差し込む）。
func extraDrawingBlock(tmplTitle, tmpl string, reads []drawingRead, machine, partner, ref string) (*html.Node, error) {
	x := cms.NewPageDraft(tmplTitle, tmpl)
	b, err := x.RequireContainer("図面")
	if err != nil {
		return nil, err
	}
	var nos, names []string
	seenNo, seenName := map[string]bool{}, map[string]bool{}
	for _, r := range reads {
		if no := cms.NormalizeNameForIngest(r.No); no != "" && !seenNo[normNo(no)] {
			seenNo[normNo(no)] = true
			nos = append(nos, no)
		}
		if nm := cms.NormalizeNameForIngest(r.Name); nm != "" && !seenName[normName(nm)] {
			seenName[normName(nm)] = true
			names = append(names, nm)
		}
	}
	b.SetTag("図面番号", nos...)
	b.SetTag("図面名称", names...)
	b.SetTag("装置名称", machine)
	b.SetTag("客先", cms.NormalizeNameForIngest(partner))
	if !b.SetFileView(ref) {
		appendHTML(b.Node(), fileViewHTML(ref))
	}
	n := b.Node()
	if n.Parent != nil {
		n.Parent.RemoveChild(n)
	}
	return n, nil
}
