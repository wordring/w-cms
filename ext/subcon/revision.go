package subcon

// ─────────────────────────────────────────────────────────────────────────
// 改定図面の合流——同じ部品のページが既にあったとき（filing.go から分離）
//
// ユーザー:「同じ部品のページが既にあれば、その図面は改定図面」（2026-09-03）。
// 顧客名／装置名称の下では図面名称が一意なので、**ページが在ること自体が改定の合図**です。
//
// **旧版は最新版の子ページになります**（2026-09-06 ユーザー:「旧版を最も新しい版の
// 子にしてはどうでしょう。ワンノートではページが子を持てなかったので、出来ません
// でしたが、CMSでは可能では？」）。もとは同じページに積み上げて古いものに赤枠を
// 付ける形でしたが、**それはワンノートの制約を写しただけ**でした。
//
// ⚠ **ここはHTML文字列を正規表現で切り貼りします。** 本文をパースして組み直すのでは
// なく、ブロックをそのまま運びます——`data-id`（社内コードの指し先）を保つためです。
// パースし直すと属性の順や空白が変わり、**同じブロックなのに差分が出ます**。
//
// ⚠ **偽の改定を作らないための検査が要ります**（`duplicateReason`・`checkRevision`）。
// 同じ図面を2度整理したときに「版が2つある」ことにしてはいけません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// mergeAsRevision は改定図面を既存の加工製品ページへ合流させます。
//
// **旧版は最新版の子ページになります**（2026-09-06 ユーザー:「図面が改定された場合、
// 旧版を最も新しい版の子にしてはどうでしょう。ワンノートではページが子を持てなかった
// ので、出来ませんでしたが、CMSでは可能では？」）。もとは同じページに図面ブロックを
// 積み上げ、古いものに赤枠を付けて見分ける形でした——**ワンノートの制約を写した形**で、
// ページが子を持てるいまは写す理由がありません。
//
// 変わったこと:
//
//   - 加工製品ページに載るのは**最新の図面1つだけ**。積み上がらないので赤枠も要らない。
//   - 旧版は `旧版 <図面番号> <図面名称>` という子ページへ、**ブロックごと**移る
//     （由来の `受信元` はブロックの中にあるので、出所も一緒に付いて行く）。
//   - 改訂履歴は最新版に載ったまま。その版の行の図面番号が**旧版ページへのリンク**
//     になります（本文の `a[href]` は制限が無いので、参照タグの文法は要りません）。
//
// 仮のページを消すのは**物理削除ではなくゴミ箱への移動**です。
//
// **順序に意味があります**——旧版ページを先に作り、次に加工製品ページを書き換え、
// 最後に仮のページを片付けます。途中で失敗しても、図面がどこにも無い状態は生まれません。
func mergeAsRevision(user *auth.User, srcPageID, dstPageID string) error {
	srcBody, err := cms.ReadPageBody(srcPageID)
	if err != nil {
		return err
	}
	block := cms.FirstBlockHTML(srcBody)
	if strings.TrimSpace(block) == "" {
		return errors.New("移す図面ブロックが見つかりません")
	}
	dstInt, err := strconv.Atoi(dstPageID)
	if err != nil || !canWritePage(user, dstInt) {
		return errors.New("合流先へ書き込む権限がありません")
	}
	dstBody, err := cms.ReadPageBody(dstPageID)
	if err != nil {
		return err
	}

	// 1. いま載っている図面ブロックを外し、旧版ページへ移す。
	oldBlocks, rest := extractDrawingSections(dstBody)
	oldPageID, oldNo := "", ""
	if len(oldBlocks) > 0 {
		oldNo = drawingNoOf(oldBlocks[0])
		oldPageID, err = createOldVersionPage(user, dstPageID, oldNo, oldBlocks)
		if err != nil {
			return err
		}
		dstBody = rest
	}

	// 2. 新しい図面ブロックを見出しの直後へ。
	//
	// **ブロックIDが衝突しないようにする**——`ページID-ブロックID` はその改定の
	// 社内コードなので、1つのページの中で重複したら指し先が定まりません
	// （4桁 base36 なので確率は低いが、低いことと起きないことは違う）。
	block = reassignBlockIDIfTaken(block, dstBody)
	newNo := drawingNoOf(block)
	if err := cms.RewriteBody(dstPageID, user.Username, func(string) string {
		body := cms.InsertAfterH1(dstBody, block)
		// **改訂履歴に1行足す**——社内コードの指し先はこの行です（vocab.go の
		// drawing-revisions）。図面ブロックは人が消せる決まりなので、消せるものを
		// 指し先にすると紙に出たコードが宙ぶらりんになります。
		body = InsertRevisionRow(body, newNo)
		if oldPageID != "" {
			body = linkRevisionRow(body, oldNo, oldPageID)
		}
		return body
	}); err != nil {
		return err
	}

	// 3. 合流し終えてから仮のページを片付ける。
	if _, err := cms.DeletePageToTrash(srcPageID); err != nil {
		return err
	}
	return nil
}

// createOldVersionPage は旧版の子ページを作り、そのIDを返します。
//
// 題は `旧版 <図面番号> <図面名称>`（2026-09-06 ユーザー決定）。同じ題の兄弟が
// 既に居れば受領日を添えます——**図面番号が変わらない改定**が実際にあるためです。
func createOldVersionPage(user *auth.User, dstPageID, oldNo string, blocks []string) (string, error) {
	name := pageTitleOf(dstPageID)
	title := strings.TrimSpace("旧版 " + strings.TrimSpace(oldNo) + " " + strings.TrimSpace(name))
	if _, taken := findChildByTitle(dstPageID, title); taken {
		title += "（" + time.Now().In(time.Local).Format("2006-01-02") + "）"
	}
	body := "<h1>" + htmlEscape(title) + "</h1>" + strings.Join(blocks, "")
	return cms.CreateChildPage(dstPageID, user.Username, body)
}

// extractDrawingSections は本文から図面ブロックを取り出し、残りの本文と一緒に返します。
//
// **`section` が入れ子にならない前提**です——業務ブロックは機能見出し形で平らに並ぶ
// （入れ子にする書き方が無い）。見出しの文字で見分けるのは、機能見出し形そのもの
// ——機械キーを本文へ書く属性はありません。
// ⚠ **入れ子を数えて切ります**（2026-09-20）。図面ブロックは中にファイル表示の節を
// 含むので、「最初に出会う `</section>` まで」で切ると**余分な `</section>` が1つ
// 残ります**。それまではその余りが、運ぶ側（`FirstBlockHTML`）の**閉じ足りない
// ブロック**と噛み合って釣り合っていました——**2つの誤りが打ち消し合っていた**わけで、
// 片方だけ直すと崩れます。いまはどちらも `cms.SectionBlockAt` を通ります。
//
// ⚠ **入れ子の節は自分では拾いません。** `IndexSectionTag` で見つけた位置から
// ブロック丸ごと飛ばすので、図面ブロックの中のファイル表示が別のブロックとして
// 数えられることはありません。
func extractDrawingSections(body string) (blocks []string, rest string) {
	var out strings.Builder
	i := 0
	for {
		open := cms.IndexSectionTag(body, i)
		if open < 0 {
			break
		}
		sec, end, ok := cms.SectionBlockAt(body, open)
		if !ok {
			break
		}
		if strings.Contains(sec, "<h2>図面</h2>") {
			blocks = append(blocks, sec)
			out.WriteString(body[i:open]) // ブロックは落とし、間の本文は残す
		} else {
			out.WriteString(body[i:end])
		}
		i = end
	}
	out.WriteString(body[i:])
	return blocks, out.String()
}

// linkRevisionRow は改訂履歴の中で図面番号が no の行を、旧版ページへのリンクにします。
//
// **見つからなければ何もしません**（手で消した履歴・古い形のページ）。旧版へは
// 子ページの一覧からも行けるので、ここが効かなくても行き止まりにはなりません。
func linkRevisionRow(body, no, oldPageID string) string {
	no = strings.TrimSpace(no)
	if no == "" || oldPageID == "" {
		return body
	}
	at := strings.Index(body, `<table data-type="drawing-revision-items">`)
	if at < 0 {
		return body
	}
	cell := "<td>" + htmlEscape(no) + "</td>"
	rel := strings.Index(body[at:], cell)
	if rel < 0 {
		return body
	}
	i := at + rel
	linked := `<td><a href="/` + htmlEscape(oldPageID) + `">` + htmlEscape(no) + `</a></td>`
	return body[:i] + linked + body[i+len(cell):]
}

// blockIDRe は section の先頭に付いたブロックIDを拾います。
var blockIDRe = regexp.MustCompile(`^<section data-id="([0-9a-z]+)"`)

// reassignBlockIDIfTaken は、運ぶブロックのIDが合流先で既に使われていたら振り直します。
// 使われていなければ**そのまま**——既にどこかで社内コードとして書き留められて
// いるかもしれないので、必要のない振り直しはしません。
func reassignBlockIDIfTaken(block, dstBody string) string {
	m := blockIDRe.FindStringSubmatch(block)
	if m == nil {
		return block
	}
	if !strings.Contains(dstBody, `data-id="`+m[1]+`"`) {
		return block
	}
	return strings.Replace(block,
		`data-id="`+m[1]+`"`, `data-id="`+cms.NewBlockID(dstBody)+`"`, 1)
}

// drawingNoRe は図面ブロックから図面番号を拾います（改訂履歴の行に載せる）。
var drawingNoRe = regexp.MustCompile(`<dt>図面番号</dt><dd>([^<]*)</dd>`)

// drawingNoOf は図面ブロックの図面番号を返します（無ければ空）。
func drawingNoOf(block string) string {
	if m := drawingNoRe.FindStringSubmatch(block); m != nil {
		return m[1]
	}
	return ""
}

// ── 偽の改定を作らないための検査（2026-09-03）──────────────────────────
//
// ユーザー:「同じ図面名称を2回整理すると改定になるのはちょっとマズいと思います」。
// 同じPDFを解析し直して整理に流すと、**中身は同じなのに版が増えます**。
// 履歴が嘘になり、社内コードが増え、赤枠の古い図面が意味も無く積み上がります。
//
// 判定は2段に分けます:
//
//	確実な重複  = 由来（受信元）が既存の図面ブロックと同じ。**同じ添付から作った**
//	              ものなので、改定ではありえない。黙って止める
//	疑わしい    = 図面番号が既存の版と同じ。改定なら普通は番号か改訂記号が変わる。
//	              **機械には決められない**ので人に尋ねる（確認して再実行）

// sourceRefRe は図面ブロックの由来（受信元）を拾います。
//
// **書き手（analyze_pdf.go）と同じ定数から組みます**（2026-09-16）——正規表現に
// 名前を焼き込むと、書き手を直しても**合流の重複検知だけが静かに素通り**します。
// `regexp.QuoteMeta` を通すのは、タグ名に将来メタ文字が入っても壊れないため。
var sourceRefRe = regexp.MustCompile(
	`<dt>` + regexp.QuoteMeta(SourceRefTag) + `</dt><dd>([^<]*)</dd>`)

// revNumberRe は改訂履歴の行から図面番号を拾います。
var revNumberRe = regexp.MustCompile(`<tr data-id="[0-9a-z]+"><td>[0-9]+</td><td>([^<]*)</td>`)

// duplicateReason は合流させてよいかを調べ、止める理由を返します
// （空なら合流してよい）。needsConfirm は「人が確認すれば通してよい」の印です。
func duplicateReason(block, dstBody string, confirmed bool) (reason string, needsConfirm bool) {
	// 確実な重複——同じ添付から作られている。確認しても通しません。
	if sameSourceAttachment(block, dstBody) {
		return "同じ添付から作られた図面が既にあります（重複なので合流しません）", false
	}
	if confirmed {
		return "", false
	}
	// 疑わしい——図面番号が既存の版と同じ。改定なら普通は番号が変わります。
	no := strings.TrimSpace(drawingNoOf(block))
	if no == "" {
		return "", false
	}
	for _, m := range revNumberRe.FindAllStringSubmatch(dstBody, -1) {
		if strings.TrimSpace(m[1]) == no {
			return "図面番号「" + no + "」の版が既にあります。" +
				"改定なら普通は図面番号か改訂記号が変わります。" +
				"本当に改定として合流させるなら「改定として合流」にチェックして実行してください", true
		}
	}
	return "", false
}

// checkRevision は合流させてよいかを、運ぶブロックと合流先の本文から調べます。
func checkRevision(srcPageID, dstPageID string, confirmed bool) (reason string, needsConfirm bool, err error) {
	srcBody, err := cms.ReadPageBody(srcPageID)
	if err != nil {
		return "", false, err
	}
	dstBody, err := cms.ReadPageBody(dstPageID)
	if err != nil {
		return "", false, err
	}
	r, c := duplicateReason(cms.FirstBlockHTML(srcBody), dstBody, confirmed)
	return r, c, nil
}

// ── 二つ目の図面として追加 ───────────────────────────────────────────────
//
// ユーザー:「図面が複数あるのは、部品図と溶接図などがあるからで、**品物としては
// 一つです**。図面さえ置ければ、それでいいのですが、整理の時に既存のページへ
// 二つ目の図面として追加できる仕組みが必要かと」（2026-09-20）。
//
// ⚠ **それまでは黙って「改定」にしていました。** 行き先に同じ題のページがあるとき、
// 図面番号が違えば改定として合流していました——ところが部品図と溶接図も「図面番号が
// 違う」ので、**片方が旧版として子ページへ押し込まれます**。エラーも確認も出ません。
//
// **機械には区別できません**（改定も別図面も「同じ品物・違う図面番号」）。
// なので**人が選びます**——このプロジェクトで繰り返している形です。

// mergeAsDrawing は同じ品物の**別の図面**を、既にある加工製品ページへ並べます。
//
// 改定との違いは3つです:
//
//   - **旧版ページを作りません**（どちらも現行なので、古い新しいがない）
//   - **改訂履歴に行を足しません**（⚠ 下の注記）
//   - **図面名称を書き戻しません**（呼ぶ側の責任。溶接図自身の名前を潰さないため）
//
// ⚠ **改訂履歴に足さない理由**: 履歴の表は `版 / 図面番号 / 受領日` の1本で、版番号が
// 通し番号です。別の図面をそこへ混ぜると、**版が2枚の図面をまたいで進みます**
// （部品図の版2のつもりが溶接図だった、が起きる）。1ページに図面が2枚あるときの
// 履歴の持ち方は**未決**なので、間違った行を足すより足さないほうを選びました。
// 由来は運ぶブロックの中の `受信元` が持っているので、出所は失われません。
func mergeAsDrawing(user *auth.User, srcPageID, dstPageID string) error {
	srcBody, err := cms.ReadPageBody(srcPageID)
	if err != nil {
		return err
	}
	block := cms.FirstBlockHTML(srcBody)
	if strings.TrimSpace(block) == "" {
		return errors.New("移す図面ブロックが見つかりません")
	}
	dstInt, err := strconv.Atoi(dstPageID)
	if err != nil || !canWritePage(user, dstInt) {
		return errors.New("合流先へ書き込む権限がありません")
	}
	dstBody, err := cms.ReadPageBody(dstPageID)
	if err != nil {
		return err
	}
	// **ブロックIDが衝突しないようにする**——`ページID-ブロックID` は社内コードなので、
	// 1つのページの中で重複したら指し先が定まりません（改定の合流と同じ作法）。
	block = reassignBlockIDIfTaken(block, dstBody)

	if err := cms.RewriteBody(dstPageID, user.Username, func(string) string {
		return insertAfterDrawings(dstBody, block)
	}); err != nil {
		return err
	}
	// 並べ終えてから仮のページを片付ける（順序の意味は mergeAsRevision と同じ）。
	_, err = cms.DeletePageToTrash(srcPageID)
	return err
}

// insertAfterDrawings は、既にある**最後の図面ブロックの直後**へ足します。
//
// 後ろに置くのは、**先に届いた図面が上に残る**ほうが読む人の当てが外れないためです
// （改定は新しいものが上ですが、こちらは新旧ではなく部品図と溶接図の並びなので、
// 届いた順のほうが素直）。図面ブロックが1つも無ければ見出しの直後へ。
//
// ⚠ **入れ子を数えて探します**（`cms.SectionBlockAt`）。図面ブロックは中にファイル表示の
// 節を含むので、最初の `</section>` で切ると**改訂履歴の手前ではなく図面の中へ**
// 入ってしまいます（2026-09-20 に直した打ち消し合いと同じ罠）。
func insertAfterDrawings(body, block string) string {
	at := -1
	i := 0
	for {
		open := cms.IndexSectionTag(body, i)
		if open < 0 {
			break
		}
		sec, end, ok := cms.SectionBlockAt(body, open)
		if !ok {
			break
		}
		if strings.Contains(sec, "<h2>図面</h2>") {
			at = end
		}
		i = end
	}
	if at < 0 {
		return cms.InsertAfterH1(body, block)
	}
	return body[:at] + block + body[at:]
}

// sameSourceAttachment は、運ぶブロックと**同じ添付から作られた図面**が行き先に
// 既にあるかを返します（`受信元` の値が一致するか）。
//
// ⚠ **これは「確実な重複」です**——改定でも二つ目の図面でもなく、同じものを2度
// 整理しただけ。**どちらを選んでも通しません**。
func sameSourceAttachment(block, dstBody string) bool {
	m := sourceRefRe.FindStringSubmatch(block)
	if m == nil || strings.TrimSpace(m[1]) == "" {
		return false
	}
	return strings.Contains(dstBody, "<dd>"+m[1]+"</dd>")
}

// duplicateSource はページ同士で同じことを調べます（整理の分岐から使う口）。
func duplicateSource(srcPageID, dstPageID string) (bool, error) {
	srcBody, err := cms.ReadPageBody(srcPageID)
	if err != nil {
		return false, err
	}
	dstBody, err := cms.ReadPageBody(dstPageID)
	if err != nil {
		return false, err
	}
	return sameSourceAttachment(cms.FirstBlockHTML(srcBody), dstBody), nil
}
