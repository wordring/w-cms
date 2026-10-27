package toho

// 解析が作る加工製品ページの本文（2026-10-20 に analyze_pdf.go から分けた）——テンプレート「加工製品」を埋める・図面ブロックと最初の改訂明細。

import (
	"strings"
	"time"

	"w-cms/ext/comm"
	"w-cms/internal/cms"
)

// ProductTemplate は加工製品ページを作るテンプレートの題です（2026-09-27）。
//
// ⚠ **図面の解析はこのテンプレートを写して加工製品ページを作ります**——人が手で作るときに
// 選ぶ雛形と同じもの。見出し「図面」の節（図面ブロック）と、キャプション「改訂明細表」の表が
// 要ります（無ければ作りません）。材料・外注加工などの表は写すだけです。
const ProductTemplate = "加工製品"

// drawingHeading は図面ブロック（見出しの節）の見出しです。
const drawingHeading = "図面"

// buildProductPageHTML は加工製品ページの本文を、テンプレート tmpl を埋めて組みます。
//
//	<h1>P103-227-6 台座Assy</h1>
//	<section><h2>図面</h2><dl data-type="tags"> 図面番号・図面名称・…・受信元・対応DXF </dl>
//	  <section data-type="file-view" data-ref="…"></section></section>
//	<table><caption>改訂明細表</caption> … 1版目の行 </table>
//
// **1通のメールの中でPDFとDXFを対応づけた結果**がこのページです。過去のページを
// 図面番号で探して束ねることはしません——番号は別製品で衝突しうるので、
// 同一性を担うのは常にページID（drawing_match.go 冒頭）。
func buildProductPageHTML(tmpl, hostPageID, attachID string, j *orderJudgment, matches []matchedDXF) (string, error) {
	// 題は「図面番号 図面名称」——**図面名称は重複しうる**ので番号を先に置く。
	//
	// **ブロックと同じ正規化を通します。** 通さないと、題が `シュート先Ｔ金具` で
	// ブロックが `シュート先T金具` という食い違いが出ます（実データで出しました）。
	title := strings.TrimSpace(cms.NormalizeNameForIngest(j.DrawingNo) + " " +
		cms.NormalizeNameForIngest(j.DrawingName))
	if title == "" {
		title = "図面（番号不明）"
	}

	d := cms.NewPageDraft(ProductTemplate, tmpl)
	d.SetTitle(title)
	// メールから加工製品ページを作ったので、ページの頭のタグに通信記録（2026-10-27・comm.RecordRefTag）。整理で既にある
	// ページへ図面として運ぶときは、図面のブロックへ付け直す（revision.go の mergeInputs）。
	setHeaderTag(d.DraftBlock, comm.RecordRefTag, hostPageID)
	if err := fillDrawingBlock(d, j, hostPageID, attachID, matches); err != nil {
		return "", err
	}
	if err := fillFirstRevision(d, j.DrawingNo); err != nil {
		return "", err
	}
	return d.HTML(), nil
}

// fillFirstRevision は改訂明細表の1版目を書きます（解析が作る加工製品ページと、メールから作る空の加工製品ページで同じ）。
func fillFirstRevision(d *cms.PageDraft, drawingNo string) error {
	// 改訂明細表（1版目）。
	//
	// **行が社内コードの指し先です**——`ページID-行ID` で押せばその版へ飛びます
	// （2026-09-03 ユーザー:「改訂履歴の項目を作り版にdata-idを割り当てれば良いのでは？」）。
	// 図面ブロックは消せる決まりなので指し先にせず、消す理由の無い小さな行を指します。
	//
	// ⚠ **キャプションだけで名乗ります**（2026-09-27 利用者:「改訂履歴はキャプションだけで良い
	// です」「メニューの方も用語を統一したほうが良いのでは？」）。
	// ⚠ **表と行にブロックIDを振ります**——テンプレートを写すとブロックIDは外れるので、
	// 振らないと改定の行を数える正規表現（`revisionRowRe`）に1版目が数えられず、版がずれます。
	rows, err := fillVocabTable(d.DraftBlock, revisionItemsType, []map[string]string{{
		"revision":    "1",
		"drawing-no":  strings.TrimSpace(drawingNo),
		"received-at": time.Now().In(time.Local).Format("2006-01-02"),
	}})
	if err != nil {
		return err
	}
	if t, ok := d.Table(displayNameOf(revisionItemsType)); ok {
		d.AssignBlockID(t)
	}
	for _, tr := range rows {
		d.AssignBlockID(tr)
	}
	return nil
}

// fillDrawingBlock は図面ブロック（見出し「図面」の節）を埋めます。
//
// **ブロックIDを付けるのが肝**——参照値 `ページID-ブロックID` は押せばこの
// ブロックへ飛ぶので、これが**その改定の社内コード**になります（2026-09-03 ユーザー:
// 「部品の社内コードは加工製品ページのページ番号と改定番号を足したものになるのでは？
// …すると、社内コードでその項目へ飛べることになります」）。
// 図面番号が別製品と衝突しても、この番号は構造上一意です。
//
// 由来（受信元・対応DXF）を**ブロックの中**に置くのは、改定で合流させるときに
// ブロックごと運べば出所も一緒に付いて行くようにするためです。
func fillDrawingBlock(d *cms.PageDraft, j *orderJudgment, hostPageID, attachID string, matches []matchedDXF) error {
	blk, err := d.RequireContainer(drawingHeading)
	if err != nil {
		return err
	}
	d.AssignBlockID(blk.Node())
	// 図面番号も同じ扱いです（2026-09-06）——**人がいちばんコピペする値**なので、
	// 揃わないまま置くと揺れがそこから増えます。畳むのは NFKC までで、
	// `NormalizeCode` は使いません（ハイフンと長音を潰すと読めなくなる）。
	// **顧客名・装置名称・図面名称は早期に正規化します**（2026-09-06 ユーザー）。
	// この3つはそのままページの題になり、題の完全一致が階層の同一性を決めるので、
	// 畳まずに入れると `φ３２０　共通台座` が別の装置ページになります。
	//
	// ── ここは**可変タグ**です（2026-09-18 ユーザー決定）──
	//
	// ユーザー:「この定義リストを『名前：値のタグ』に変更します。なぜなら、この情報こそ
	// 検索したいものだからです。おそらくもっとも頻繁に検索し、ワンノートでは取りこぼしが
	// 多いので、w-cms を作り始めました」。タグにすると `図面番号` は設定で `code` 型なので、
	// 空白・ハイフン・長音・大小を畳んで引けます（`PagesByTagLoose`）。
	//
	// ⚠ **空欄でも欄を出します**——テンプレートの空欄（`<dd><br/></dd>`）がそのまま残ります。
	// 装置名称と客先は解析が読めないことがあり、**書く場所が見えていれば人が埋めます**
	// （実データで7枚のうち3枚に `客先` が無かった）。
	// ⚠ **`品番` は書きません**（2026-09-21 ユーザー:「加工製品ページに品番タグを付けようと
	// 思います」）。**図面からは読みません**——「何が品番か」は取引先ごとの取り決めで、
	// 図面のどこにも書かれていないからです。置き場は H1 の下（テンプレートが持つ）。
	setHeaderTag(blk, DrawingNoTag, cms.NormalizeNameForIngest(j.DrawingNo))
	setHeaderTag(blk, DrawingNameTag, cms.NormalizeNameForIngest(j.DrawingName))
	// 装置名称・客先は置き場所（製造台帳／社名／装置名称／図面名称）に効く項目。
	setHeaderTag(blk, MachineNameTag, cms.NormalizeNameForIngest(j.MachineName))
	setHeaderTag(blk, ClientNameTag, cms.NormalizeNameForIngest(j.Customer))
	ref := hostPageID + "-" + attachID
	setHeaderTag(blk, SourceRefTag, ref)
	// 一致したDXFを参照タグで指す（押すと元の通信記録ページの該当添付へ飛ぶ）。
	// 一致が無ければ何も書かない——**DXFが無いのも普通**（PDFだけの図面）。
	var dxf []string
	for _, m := range matches {
		dxf = append(dxf, hostPageID+"-"+m.AttachID)
	}
	setHeaderTag(blk, "対応DXF", dxf...)
	// **図面をここに開く、と本文に書きます**（2026-09-14）。ユーザー:「HTMLに無いものが
	// 表示されるのは極力避けたい」「表示するという意図を伝える名前が良いと思います」。
	// 中身はコアが描きます（`internal/cms/file_view.go`）——**この拡張はPDFの出し方を
	// 知りません**。テンプレートの図面ブロックに置いたファイル表示の印へ配線します
	// （**人が消せます**——消せば図面は出なくなり、参照タグ〔出所の記録〕は残ります）。
	blk.SetFileView(ref)
	return nil
}
