package toho

// ─────────────────────────────────────────────────────────────────────────
// 整理の「装置のページへ」——図面を装置名称のページ（加工製品のフォルダ）に足す（2026-10-08）
//
// 利用者:「メールの整理について、図面をフォルダページに追加したいのですが、出来ません」（メール /001163 の組立図）。
// 整理の行き先は加工製品ページ（`取引先／社名／加工製品／装置名称／図面名称`）だけで、装置名称のページそのものは選べなかった
// ——組立図のような**装置全体の図面**は、加工製品にせず装置のページに置きたい。
//
//   - 行き先は欄の取引先・装置名称で辿る**既にある**装置のページ（作らない——無ければ断る。新しい装置なら「新規」で）。
//     図面名称の欄は見ない。
//   - 運ぶのは図面追加と同じ（`mergeAsDrawing`——解析で作ったページの図面のまとまりを装置のページの図面の後ろへ・
//     解析で作ったページはごみ箱へ）。加工製品ページは作らない。区分も書かない（品物ではないので）。
//   - 装置のページに**同じファイルの表示が既にある**とき（手で貼ったもの）は確かめる（足すと2つになる）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"regexp"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
)

// findMachineFolder は `取引先／社名／加工製品／装置名称` の装置のページを、作らずに探します。
func findMachineFolder(customer, machine string) (string, bool) {
	boxID, ok := CustomerBoxPageID()
	if !ok {
		return "", false
	}
	customerID, ok := findChildByTitle(boxID, customer)
	if !ok {
		return "", false
	}
	productsID, ok := findChildByTitle(customerID, ProductsBoxTitle)
	if !ok {
		return "", false
	}
	return findChildByTitle(productsID, machine)
}

// fileViewRefRe は本文の中のファイル表示が指すもの（`data-ref`）です。
var fileViewRefRe = regexp.MustCompile(`data-ref="([^"]+)"`)

// fileToMachineFolder は整理の「装置のページへ」の1行です。
func fileToMachineFolder(user *auth.User, pageID string, row filingRequest) filingResult {
	customer := cms.NormalizeNameForIngest(row.Customer)
	machine := cms.NormalizeNameForIngest(row.MachineName)
	if customer == "" || machine == "" {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "顧客名・装置名称のどちらかが空なので、そのままにしました"}
	}
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !canWritePage(user, idInt) {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "このページを動かす権限がありません"}
	}
	if holder, open := editlock.Locks.EditorOpen(idInt); open {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "このページは編集中です（" + holder + "）。閉じてからもう一度お試しください"}
	}
	where := customer + "／" + ProductsBoxTitle + "／" + machine
	folderID, ok := findMachineFolder(customer, machine)
	if !ok {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "装置のページ「" + where + "」がありません——装置名称を既にある装置に合わせてください（新しい装置なら「新規」で）"}
	}
	folderInt, _ := strconv.Atoi(folderID)
	if !canWritePage(user, folderInt) {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "装置のページ「" + where + "」へ書き込む権限がありません"}
	}
	if holder, open := editlock.Locks.EditorOpen(folderInt); open {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "装置のページ「" + where + "」は編集中です（" + holder + "）。閉じてからもう一度お試しください"}
	}
	// 同じファイルの表示が装置のページに既にあれば確かめる（手で貼ったものなど——足すと2つになる）。
	if !row.ConfirmRevision {
		srcBody, err := cms.ReadPageBody(pageID)
		if err != nil {
			return filingResult{PageID: pageID, Outcome: "skipped", Message: "このページを読めません: " + err.Error()}
		}
		folderBody, err := cms.ReadPageBody(folderID)
		if err != nil {
			return filingResult{PageID: pageID, Outcome: "skipped", Message: "装置のページを読めません: " + err.Error()}
		}
		for _, m := range fileViewRefRe.FindAllStringSubmatch(drawingBlockOf(srcBody), -1) {
			if strings.Contains(folderBody, `data-ref="`+m[1]+`"`) {
				return filingResult{PageID: pageID, Outcome: "needs_confirm", TargetID: folderID,
					Message: "装置のページ「" + where + "」には、このファイルの表示が既にあります。足すと2つになります" +
						"（前からある表示はそのまま残ります——要らなければあとで消してください）"}
			}
		}
	}
	// 取引先と装置名称を図面のまとまりにも書き戻す（図面名称は運ぶ図面のまま——図面追加と同じ）。
	if err := syncDrawingFields(user, pageID, customer, machine, ""); err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "図面ブロックの値を直せません: " + err.Error()}
	}
	if err := mergeAsDrawing(user, pageID, folderID); err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "装置のページへ足せません: " + err.Error()}
	}
	auth.Audit(user.Username, "file-drawing.folder", pageID+" -> "+folderID)
	return filingResult{PageID: pageID, Outcome: "added", TargetID: folderID,
		Message: where + " のページへ図面を足しました（加工製品ページは作っていません）"}
}
