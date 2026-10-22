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
	"io"
	stdhtml "html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
	// only は作り直すページを w-cms のページIDで絞ります（空なら全部・2026-09-30——利用者が直している最中の
	// ほかのページに触らずに、決まったページだけを入れ直すため）。
	only map[string]bool
	db     *sql.DB // w-cms の索引（読むだけ・同じ図面番号のページを探す）
	// seen はこの回に作った図面番号（正規化）→ ワンノートの題。
	seen map[string]string
	// machineNotes はこの回に書いた装置のページ → ワンノートの題（同じ装置のページに2つ書かない）。
	machineNotes map[string]string
	// dests は、いま作っているページの人が決めた表の行き先です（■節 → その節の表ごとの行き先・製造の設定.json の
	// 「表の行き先」をワンノートのセクションと題で引いたもの・tables.go）。無ければ nil（機械の決まりのまま）。
	dests map[string][]string
	// state は製造の記録と報告を置く場所です（既定は root——2026-10-15 に分けた。家でサンプルのデータを作るとき、
	// 職場の製造の記録〔OneDrive で家にも届く〕を上書きしないため）。
	state string

	mutoolMissing bool
	geminiOff     bool
	// byBase は w-cms の加工製品ページの図面番号を改訂記号の前（基）ごとに並べたもの（olderRevisionOf・1回だけ読む）。
	byBase map[string][]productDrawing
	// hint は、いま作っているページの「図面番号の当て」です（製造の設定.json・無ければ空）。
	hint drawingNoHint
	// curKey は、いま作っているワンノートのページの鍵です。machineOwner は装置のページ → 前の回にそこへ書いたワンノートのページの鍵、
	// titles は鍵 → ワンノートの題です（buildMachineNote が、別のワンノートのページの書いた装置のページを上書きしないため・2026-10-22）。
	curKey       string
	machineOwner map[string]string
	titles       map[string]string
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

// ── 改訂記号だけ違う図面（2026-10-22）──────────────────────────────────────
//
// 利用者:「旧版として子ページに入れる（道具を直します）出お願いします」——ワンノートの 02 記録（2024 年の試作・製造の記録）には、
// w-cms に今ある加工製品の**古い版の図面**（今の版が …-02B なのに …-02A の図面など）が多い。図面番号が完全に同じでないと
// duplicate は当たらないので、そのままだと同じ品物のページが2枚できる。そこで、改訂記号の前（基）が同じで改訂の**新しい**
// 加工製品ページがちょうど1枚あれば、そのページの子に**旧版**として作る（w-cms の図面改定と同じ形——ext/toho/revision.go の
// createOldVersionPage: 題「旧版 <図面番号> <図面名称>」・ページ種類「加工製品の旧版」・品番などの題の下のタグは持たない）。
// ワンノートの方が新しい版のときと、候補が2枚以上のときは作らずに報告する（どちらを今の版にするかは人が決める）。

// revSuffixRe は図面番号の末尾の改訂記号です（`…-02A`・`…-1-C`・`… B`・`…_rev0`）。数字のすぐ後ろの英字1字か rev＋数字だけ。
var revSuffixRe = regexp.MustCompile(`^(.*?\d)[\s_-]*(?:REV\s*(\d+)|([A-Z]))$`)

// revisionSplit は図面番号を、改訂記号の前（正規化した基）と改訂記号に分けます。改訂記号が無ければ rev は空。
func revisionSplit(no string) (base, rev string) {
	s := strings.ToUpper(strings.TrimSpace(cms.NormalizeNameForIngest(no)))
	if m := revSuffixRe.FindStringSubmatch(s); m != nil {
		rev = m[2]
		if rev == "" {
			rev = m[3]
		}
		return normNo(m[1]), rev
	}
	return normNo(s), ""
}

// revNewer は改訂記号 a が b より新しいかです（記号なし ＜ A ＜ B …・rev の数字は数で比べる）。
func revNewer(a, b string) bool {
	if a == b {
		return false
	}
	if b == "" {
		return true
	}
	if a == "" {
		return false
	}
	if x, err := strconv.Atoi(a); err == nil {
		if y, err := strconv.Atoi(b); err == nil {
			return x > y
		}
	}
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a > b
}

// productDrawing は w-cms の加工製品ページの図面番号1つです。
type productDrawing struct {
	page, no, rev string
}

// productDrawings は w-cms の加工製品ページ（ページ種類が加工製品）の図面番号を、基ごとに返します（1回だけ読む）。
func (e *env) productDrawings() map[string][]productDrawing {
	if e.byBase != nil || e.db == nil {
		return e.byBase
	}
	e.byBase = map[string][]productDrawing{}
	rows, err := e.db.Query(`SELECT t.page_id, t.value FROM page_tags t WHERE t.name = '図面番号' AND t.page_id IN
		(SELECT k.page_id FROM page_tags k WHERE k.name = 'ページ種類' AND k.value = '加工製品')`)
	if err != nil {
		return e.byBase
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var v string
		if rows.Scan(&id, &v) != nil || strings.TrimSpace(v) == "" {
			continue
		}
		base, rev := revisionSplit(v)
		e.byBase[base] = append(e.byBase[base], productDrawing{page: fmt.Sprintf("%06d", id), no: strings.TrimSpace(v), rev: rev})
	}
	return e.byBase
}

// olderRevisionOf は、drawingNo が w-cms の加工製品ページの古い版の図面かを見ます。
// 親にする今の版のページ（ちょうど1枚）を返すか、作らない理由（ワンノートの方が新しい・候補が2枚以上）を返します。
// どちらも空なら、改訂記号だけ違う図面はありません（いつもどおり作る）。
func (e *env) olderRevisionOf(drawingNo string) (parent, parentNo, refuse string) {
	if strings.TrimSpace(drawingNo) == "" {
		return "", "", ""
	}
	base, rev := revisionSplit(drawingNo)
	pages := map[string]string{} // 今の版のページ → その図面番号
	var newerHere []string
	for _, d := range e.productDrawings()[base] {
		if normNo(d.no) == normNo(drawingNo) {
			continue // 同じ図面番号は duplicate が見る
		}
		if revNewer(d.rev, rev) {
			pages[d.page] = d.no
		} else {
			newerHere = append(newerHere, "/"+d.page+"（"+d.no+"）")
		}
	}
	if len(newerHere) > 0 {
		return "", "", "改訂記号だけ違う図面の w-cms のページ " + strings.Join(newerHere, "・") + " より、ワンノートの方が新しい版です"
	}
	if len(pages) > 1 {
		var l []string
		for p, n := range pages {
			l = append(l, "/"+p+"（"+n+"）")
		}
		sort.Strings(l)
		return "", "", "改訂記号だけ違う図面の新しい版のページが2枚以上あります（" + strings.Join(l, "・") + "）"
	}
	for p, n := range pages {
		return p, n, ""
	}
	return "", "", ""
}

// pageOfDrawing は、図面番号がちょうど no の加工製品ページ（ちょうど1枚）です——人が「今の版の図面番号」を書いたとき、
// その子に旧版として作る。無い・2枚以上なら作らない理由を返します。
func (e *env) pageOfDrawing(no string) (parent, parentNo, refuse string) {
	base, _ := revisionSplit(no)
	pages := map[string]string{}
	for _, d := range e.productDrawings()[base] {
		if normNo(d.no) == normNo(no) {
			pages[d.page] = d.no
		}
	}
	if len(pages) != 1 {
		return "", "", "「今の版の図面番号」" + no + " の加工製品ページが " + strconv.Itoa(len(pages)) + " 枚です（ちょうど1枚のときだけ、その子に旧版として作ります）"
	}
	for p, n := range pages {
		return p, n, ""
	}
	return "", "", ""
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

// ── この回に作ったページの中の改訂違い（2026-10-22）──────────────────────────────
//
// 利用者:「旧版に揃えます。道具を治しても良いですし、Claudeが書き換えても良いです。私は何もしたくないです。」——ワンノートの中だけに、
// 同じ部品の古い版と新しい版がある（02 記録の別々の仕事の記録など）。olderRevisionOf は w-cms に既にあるページとしか比べないので、
// どちらも新しいページになる。そこで、この回に作ったページを全部作り終えてから、改訂記号の前が同じ組を探し、いちばん新しい版の
// ページを残して、ほかをその子の旧版に書き換える（題「旧版 <図面番号> <名前>」・ページ種類「加工製品の旧版」・品番・品名・
// 弊社品番のタグを外す——ext/toho/revision.go の createOldVersionPage と同じ形）。どれが新しいか決まらない組は人に聞く。

// runPage はこの回に作った加工製品ページ1枚です。
type runPage struct {
	key, title string // ワンノートのページの鍵と題
	pr         *pageRecord
	note       *pageNote
}

// foldRunRevisions は、この回に作ったページの改訂違いを旧版にそろえます（下見では、そうすると報告するだけ）。
// 製造の記録を書き換えたら true。
func foldRunRevisions(c *client, built []runPage, dry bool) bool {
	groups := map[string][]runPage{}
	var bases []string
	for _, b := range built {
		base, _ := revisionSplit(b.pr.DrawingNo)
		if _, ok := groups[base]; !ok {
			bases = append(bases, base)
		}
		groups[base] = append(groups[base], b)
	}
	changed := false
	for _, base := range bases {
		g := groups[base]
		if len(g) < 2 {
			continue
		}
		newest := g[0]
		for _, b := range g[1:] {
			if _, r := revisionSplit(b.pr.DrawingNo); revNewer(r, revOf(newest.pr.DrawingNo)) {
				newest = b
			}
		}
		for _, b := range g {
			if b.key == newest.key {
				continue
			}
			if !revNewer(revOf(newest.pr.DrawingNo), revOf(b.pr.DrawingNo)) {
				b.note.ask("改訂記号の前が同じ図面番号のページがこの回に2枚以上あり、どれが新しい版か決まりません（" + b.pr.DrawingNo + "・" +
					newest.pr.DrawingNo + "）——人が決める")
				continue
			}
			if dry {
				b.note.info("本番では、この回に作る「" + newest.title + "」（" + newest.pr.DrawingNo + "）の旧版として、その子ページにします")
				continue
			}
			if err := c.makeOldVersion(b.pr.WCMS, newest.pr.WCMS, b.pr.DrawingNo); err != nil {
				b.note.warn("⚠ 旧版にできませんでした: " + err.Error())
				continue
			}
			b.pr.Kind = kindOldVersion
			changed = true
			b.note.info("この回に作った /" + newest.pr.WCMS + "（" + newest.pr.DrawingNo + "）の旧版として、その子ページにしました")
		}
	}
	return changed
}

// revOf は図面番号の改訂記号です。
func revOf(no string) string {
	_, rev := revisionSplit(no)
	return rev
}

// h1TextRe は本文の題です。
var h1TextRe = regexp.MustCompile(`<h1\b[^>]*>([\s\S]*?)</h1>`)

// makeOldVersion は、作ったばかりの加工製品ページ id を、parent の子の旧版に書き換えます（ロックの中で読んで書く）。
func (c *client) makeOldVersion(id, parent, no string) error {
	token, err := c.lock(id)
	if err != nil {
		return err
	}
	defer c.unlock(id, token)
	body, ok := c.readBody(id)
	if !ok {
		return fmt.Errorf("/%s の本文を読めません", id)
	}
	name := ""
	if m := h1TextRe.FindStringSubmatch(body); m != nil {
		name = strings.TrimSpace(stdhtml.UnescapeString(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(m[1], "")))
	}
	name = strings.TrimSpace(strings.TrimPrefix(name, no)) // 題が「図面番号 図面名称」の取引先
	d := cms.EditBody(body)
	d.SetTitle(strings.TrimSpace("旧版 " + no + " " + name))
	d.ReplaceTag("ページ種類", "加工製品の旧版")
	for _, tag := range []string{"品番", "品名", "弊社品番"} {
		d.ReplaceTag(tag)
	}
	if err := c.save(id, token, d.HTML()); err != nil {
		return err
	}
	res, err := c.do("POST", "/api/set-parent?id="+id+"&parent="+parent, nil, "", token)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("親を /%s にできません（%d %s）", parent, res.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// titleOf はワンノートのページの鍵を「セクション / 題」にします（報告のため・無ければ鍵のまま）。
func (e *env) titleOf(key string) string {
	if t, ok := e.titles[key]; ok {
		return t
	}
	return key
}
