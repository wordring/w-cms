package toho

// ─────────────────────────────────────────────────────────────────────────
// 整理の「行き先を探す」——図面追加・図面改定の相手を、品番・品名・図面番号・題・ページ番号で探す（2026-10-03）
//
// 利用者:「メールの図面を解析し整理するときに、図面を追加してもどこに入れるか入力する欄が無いので、『⚠ 行き先に同じ
// 加工製品がありません。下の候補を押すか、装置名称・図面名称を合わせてください』と出ます」。
//
// 図面追加・図面改定の行き先は、それまで2つの道しか無かった——①装置名称・図面名称の欄を既にあるページの題に合わせる
// ②機械が出す候補（同じ取引先で図面番号か名前が同じ——`productCandidates`）を押す。**二つ目の図面（溶接図など）は
// 番号も名前も違う**ので候補に出ず、相手の題を知らなければ欄を合わせられなかった。
//
// ⚠ **探すだけで、1枚も作りません**（`findChildByTitle` だけで辿る）。読めないページは出しません。
// ⚠ **決めるのは人**——結果を押すと、候補を押したときと同じく欄（取引先・装置名称・図面名称）に入り、そのページが行き先になる。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"net/http"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// filingSearchLimit は「行き先を探す」が返す数の上限です。
const filingSearchLimit = 20

// productHit は「行き先を探す」の結果1件です。
type productHit struct {
	PageID     string `json:"page_id"`
	Title      string `json:"title"`
	Machine    string `json:"machine"`
	Customer   string `json:"customer"`
	DrawingNos string `json:"drawing_nos"`
	PartNos    string `json:"part_nos"`
	// Kind は "folder" なら装置のページ（2026-10-08——`folders=1` のときだけ出す。整理の「ほかのファイル」の行き先）。空なら加工製品。
	Kind string `json:"kind,omitempty"`
}

// FilingSearchAPIHandler は GET /api/filing-search?q=…&customer=… です。
//
// 同じ取引先（`customer`）のものを先に、ほかの取引先のものを後に返します（取引先を打ち間違えていても見つかるように）。
func FilingSearchAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	q := r.URL.Query()
	hits := searchProducts(user, cms.NormalizeNameForIngest(q.Get("customer")), q.Get("q"), filingSearchLimit, q.Get("folders") == "1")
	json.NewEncoder(w).Encode(map[string]any{"success": true, "results": hits})
}

// searchProducts は取引先の「加工製品」の下から、q を含むページを探します。
//
// 当てるのは、題・装置名称・ほかの名前（品名・図面名称）の**文字**（全角半角を畳んで含むか）と、図面番号・品番の
// **符牒**（区切り・大小も外して含むか）、それにページ番号（`001234`・`/001234` のちょうど一致）。
// withFolders なら、装置のページ（加工製品の箱の直下——題か装置のページ番号で当てる）も返します（2026-10-08）。
func searchProducts(user *auth.User, customer, q string, limit int, withFolders bool) []productHit {
	out := []productHit{}
	q = strings.TrimSpace(q)
	boxID, ok := CustomerBoxPageID()
	if q == "" || !ok {
		return out
	}
	partners, err := cms.ChildPages(database.DB, pageNum(boxID))
	if err != nil {
		return out
	}
	wantText := strings.ToLower(cms.NormalizeText(q))
	// 符牒は区切りまで外して比べる（`NormalizeCode` はハイフンの種類を揃えるだけで消さない——`k120b01` で `K120-B01-7` を探せるように）。
	codeKey := func(s string) string { return strings.NewReplacer("-", "", " ", "").Replace(cms.NormalizeCode(s)) }
	wantCode := codeKey(q)
	wantID, isID := page.NormalizeID(strings.TrimPrefix(q, "/"))
	textHit := func(s string) bool {
		return s != "" && strings.Contains(strings.ToLower(cms.NormalizeText(s)), wantText)
	}
	codeHit := func(joined string) bool {
		if wantCode == "" {
			return false
		}
		for _, no := range strings.Split(joined, "・") {
			if no != "" && strings.Contains(codeKey(no), wantCode) {
				return true
			}
		}
		return false
	}
	wantCustomer := cms.NormalizeText(customer)
	var mine, others []productHit
	for _, p := range partners {
		if !page.CanView(user, p.ID) {
			continue
		}
		productsID, found := findChildByTitle(page.FormatID(p.ID), ProductsBoxTitle)
		if !found {
			continue
		}
		if withFolders {
			folders, _ := cms.ChildPages(database.DB, pageNum(productsID))
			for _, fd := range folders {
				id := page.FormatID(fd.ID)
				if !page.CanView(user, fd.ID) || !((isID && id == wantID) || textHit(fd.Title)) {
					continue
				}
				h := productHit{PageID: id, Title: fd.Title, Customer: p.Title, Kind: "folder"}
				if wantCustomer != "" && cms.NormalizeText(p.Title) == wantCustomer {
					mine = append(mine, h)
				} else {
					others = append(others, h)
				}
			}
		}
		rows, err := productListRows(user, pageNum(productsID))
		if err != nil {
			continue
		}
		for _, r := range rows {
			id := page.FormatID(r.PageID)
			hit := (isID && id == wantID) || textHit(r.Title) || textHit(r.Machine) ||
				codeHit(r.DrawingNo) || codeHit(r.PartNo)
			for _, n := range r.Names {
				if !hit && textHit(n) {
					hit = true
				}
			}
			if !hit {
				continue
			}
			h := productHit{PageID: id, Title: r.Title, Machine: r.Machine, Customer: p.Title,
				DrawingNos: r.DrawingNo, PartNos: r.PartNo}
			if wantCustomer != "" && cms.NormalizeText(p.Title) == wantCustomer {
				mine = append(mine, h)
			} else {
				others = append(others, h)
			}
		}
	}
	out = append(out, mine...)
	out = append(out, others...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// mergeMismatch は、図面追加なのに行き先に同じ図面が在る・図面改定なのに同じ図面が無いときの確認の文を返します（無ければ空）。
//
// 「同じ図面」の見分けは、**確からしいときだけ確認を出す**向きに、2つで強さを変えます:
//
//	図面追加 … 版の印（`rev1`・`_rev0`）を除いた図面番号が**同じ**なら「同じ図面が在る」（`sameDrawingStrict`）。
//	           ⚠ 末尾の英字は見ない——`K120-1W`（溶接図）は `K120-1` とは別の図面のことがある。
//	図面改定 … さらに末尾の英字1〜2字（改訂記号）の違いも同じ図面とみなし、それでも無ければ「同じ図面が無い」
//	           （`sameDrawingLoose`——`K120-1` → `K120-1A` → `K120-1B` と改訂記号で版を進める図面がある）。
//
// 届いた図面の番号が読めていないとき・本文を読めないときは判断しません（空を返す——ほかの関門は残る）。
func mergeMismatch(srcPageID, dstPageID, merge, name string) string {
	srcBody, err := cms.ReadPageBody(srcPageID)
	if err != nil {
		return ""
	}
	dstBody, err := cms.ReadPageBody(dstPageID)
	if err != nil {
		return ""
	}
	srcNo := strings.TrimSpace(drawingNoOf(drawingBlockOf(srcBody)))
	if sameDrawingKey(srcNo) == "" {
		return ""
	}
	dstNos := drawingNosOf(dstBody)
	sameBy := sameDrawingLoose
	if merge == "drawing" {
		sameBy = sameDrawingStrict
	}
	same := ""
	for _, n := range dstNos {
		if sameBy(srcNo, n) {
			same = n
			break
		}
	}
	where := "「" + name + "」（/" + dstPageID + "）"
	switch merge {
	case "drawing":
		if same != "" {
			return "⚠ 図面追加ですが、" + where + "には同じ図面（図面番号「" + same + "」）が既にあります。" +
				"版が変わった図面なら「図面改定」を選んでください。それでも二つ目の図面として足しますか？"
		}
	case "revision":
		if same == "" {
			have := "図面がありません"
			if len(dstNos) > 0 {
				have = "いまの図面は「" + strings.Join(dstNos, "・") + "」です"
			}
			return "⚠ 図面改定ですが、" + where + "に同じ図面（図面番号「" + srcNo + "」の前の版）がありません（" + have + "）。" +
				"別の図面なら「図面追加」を選んでください。それでも改定として差し替えますか？"
		}
	}
	return ""
}

// sameDrawingStrict は2つの図面番号が、版の印（`rev1`・`_rev0`）と区切り・大小を除いて同じかを返します。
func sameDrawingStrict(a, b string) bool {
	strip := func(s string) string { return strings.NewReplacer("-", "", " ", "", "_", "").Replace(sameDrawingKey(s)) }
	ka, kb := strip(a), strip(b)
	return ka != "" && ka == kb
}

// sameDrawingLoose は2つの図面番号が同じ図面（の版違い）かを返します。
//
// 版の印（`rev1`・`_rev0`）を除き、区切りと大小を畳んで同じなら同じ。さらに、**片方がもう片方に英字1〜2字を足しただけ**
// （`K120-1` と `K120-1A`・`M305-822-07` と `M305-822-07B`）も同じとみなします——改訂記号を末尾の英字で付ける図面がある
// （改定の試験の形・2026-09-20 の duplicateReason「改定なら普通は図面番号か改訂記号が変わります」）。⚠ 数字の付け足し
// （`K120-1` と `K120-10`）は別の図面です。⚠ 確認の文を出すかどうかにだけ使う（決めるのは人）。
func sameDrawingLoose(a, b string) bool {
	strip := func(s string) string { return strings.NewReplacer("-", "", " ", "", "_", "").Replace(sameDrawingKey(s)) }
	ka, kb := strip(a), strip(b)
	if ka == "" || kb == "" {
		return false
	}
	if ka == kb {
		return true
	}
	// 末尾の英字1〜2字（数字の後ろにあるもの）を改訂記号として外して比べる（`K120-1A` と `K120-1B` も同じ図面）。
	return trimRevLetters(ka) == trimRevLetters(kb)
}

// trimRevLetters は、数字の後ろに付いた末尾の英字1〜2字を外します（`K1201A` → `K1201`・`K1201` はそのまま）。
func trimRevLetters(k string) string {
	n := 0
	for n < 2 && n < len(k) {
		c := k[len(k)-1-n]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			break
		}
		n++
	}
	if n == 0 || n == len(k) {
		return k
	}
	if c := k[len(k)-1-n]; c < '0' || c > '9' {
		return k // 英字の前が数字でなければ改訂記号とみなさない
	}
	return k[:len(k)-n]
}
