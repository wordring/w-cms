package toho

// ─────────────────────────────────────────────────────────────────────────
// 記録の置き場——`記録／年／月／依頼・製造記録`（2026-10-16）
//
// 利用者:「今までは記録を受注発注見積などのフォルダに分けていましたが、記録フォルダの下の年月フォルダに
// 受注発注見積フォルダを作って記録するほうが現実的に思えてきました」「年月の箱は業務側において欲しいです」
// 「年月の下には、依頼と製造ではどうでしょうか？」「では、トップ直下を製造台帳、年月下を製造記録としては
// どうでしょう？」「その文書を作成した日のフォルダで良いと思います。（つまり、一つのフォルダにたくさん入ると
// 見づらいから分けるだけです）」（【考察】全体の設計 §4.1・§4.2）。
//
//   - **依頼**: 客先から来るもの——受注ページ・見積書。フォルダに分けずに入れます（種類は `ページ種類` のタグ）。
//   - **製造記録**: 依頼から始まる弊社の動き——部材見積依頼書・部材発注書（見積計算書・製造の記録もここへ）。
//   - **月は、その書類を作った日**です。月に意味は持たせません（見づらくならないように分けるだけ）。
//     同じ仕事の依頼と部材発注が、別の月に入ってかまいません。結ぶのは明細の行です。
//
// 年と月のフォルダの題の形はコア（`cms.DateFolderTitles`）。フォルダの本文に `ページ種類` を入れるのは拡張です
// （コアはこの言葉を知らない）。⚠ **フォルダの種類には頼りすぎないこと**（利用者）——書類を集めるのは、書類自身の
// `ページ種類` です（受注残表・加工製品の一覧）。
//
// トップ直下の「受注」「部材発注」「見積」「部材見積依頼」は、**作業面**として残ります（受注残表・必要部材表・
// 発注部材表・見積依頼必要部材表 などのビューと表の置き場）。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"time"

	"w-cms/internal/cms"
)

const (
	// RecordBoxTitle は書類を年月で入れる置き場の題です（トップ直下）。
	RecordBoxTitle = "記録"
	// RecordRequestFolder は月の下の、客先から来るもの（受注ページ・見積書）のフォルダの題です。
	RecordRequestFolder = "依頼"
	// RecordWorkFolder は月の下の、弊社の動き（部材見積依頼書・部材発注書）のフォルダの題です。
	RecordWorkFolder = "製造記録"
)

func init() {
	// **置き場もテンプレートからだけ作ります**（テンプレート駆動・`cms.EnsureTopLevelBox` は同じ題のテンプレートを写す）。
	cms.RegisterRequiredPage(cms.RequiredPage{
		Title:     RecordBoxTitle,
		Extension: "toho",
		Why: "書類の置き場です（記録／年／月／依頼・製造記録。月は書類を作った日）。依頼は客先から来るもの（案件ページ・見積書）、" +
			"製造記録は弊社の動き（部材見積依頼書・部材発注書）です。",
	})
}

// recordFolder は `記録／年／月／<sub>` を必要なだけ作り、いちばん下のフォルダのIDを返します（sub は依頼か製造記録）。
//
// 新しく作るフォルダには `ページ種類`（年・月・依頼・製造記録）を入れます。既にあるフォルダには触りません。
func recordFolder(owner, sub string, t time.Time) (string, error) {
	boxID, err := cms.EnsureTopLevelBox(RecordBoxTitle, owner)
	if err != nil {
		return "", err
	}
	y, m := cms.DateFolderTitles(t)
	yearID, err := cms.EnsureFolder(boxID, owner, y, kindFolderBody(y, "年"))
	if err != nil {
		return "", err
	}
	monthID, err := cms.EnsureFolder(yearID, owner, m, kindFolderBody(m, "月"))
	if err != nil {
		return "", err
	}
	return cms.EnsureFolder(monthID, owner, sub, kindFolderBody(sub, sub))
}

// recordPath は人に見せる行き先（`記録／2026年／10月／依頼`）です。
func recordPath(sub string, t time.Time) string {
	y, m := cms.DateFolderTitles(t)
	return RecordBoxTitle + "／" + y + "／" + m + "／" + sub
}

// kindFolderBody は `ページ種類` を持つフォルダの本文です。
func kindFolderBody(title, kind string) string {
	return "<h1>" + stdhtml.EscapeString(title) + `</h1><dl data-type="tags"><dt>` + PageKindTag + `</dt><dd>` +
		stdhtml.EscapeString(kind) + `</dd></dl>`
}
