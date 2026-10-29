package toho

import (
	"w-cms/internal/cms"
)

// Routes は部材手配計算APIのエンドポイントを提供します（RouteProvider実装）。
func (materialsPlugin) Routes() []cms.Route {
	return []cms.Route{
		{Pattern: "/api/required-materials", Handler: RequiredMaterialsAPIHandler},
		// PDF解析（東邦の業務）。main.go への直書きをやめてここへ寄せた
		// ——ルートも拡張と一緒に外れる（`-tags minimal` で消える）。
		{Pattern: "/api/analyze-attachment", Handler: AnalyzeAttachmentAPIHandler},
		// 見積計算表の弊社利益の率を書く（2026-10-01・estimate.go）。
		{Pattern: "/api/estimate/record", Handler: EstimateRecordAPIHandler},
		{Pattern: "/api/estimate/duplicate", Handler: EstimateDuplicateAPIHandler},
		// 見積回答の単価を見積計算表へ写す（2026-10-05・estimate_put.go）。
		{Pattern: "/api/estimate/rows", Handler: EstimateRowsAPIHandler},
		{Pattern: "/api/estimate/put-cost", Handler: EstimatePutCostAPIHandler},
		// 顧客へ出す見積書（2026-10-01・estimate_doc.go・estimate_pdf.go）。
		{Pattern: "/api/estimate/add", Handler: AddToEstimateAPIHandler},
		{Pattern: "/api/estimate-pdf", Handler: EstimatePDFAPIHandler},
		// 加工製品ページの整理（提案を出す口と、実行する口）。**提案は何も作りません**
		// ——顧客名・装置名称のページが生まれるのは実行のときだけ（filing.go）。
		{Pattern: "/api/filing-proposal", Handler: FilingProposalAPIHandler},
		// 整理の欄を打ち替えるたびに「その行き先にページがあるか」を聞く口（2026-09-20）。
		{Pattern: "/api/filing-target", Handler: FilingTargetAPIHandler},
		// 図面追加・図面改定の行き先を、品番・品名・図面番号・題・ページ番号で探す口（2026-10-03・filing_search.go）。
		{Pattern: "/api/filing-search", Handler: FilingSearchAPIHandler},
		// 整理の「ほかのファイル」（2026-10-08・filing_files.go）——添付を表示しているページ・添付を足す。
		{Pattern: "/api/filing-files-shown", Handler: FilingFilesShownAPIHandler},
		{Pattern: "/api/filing-attach", Handler: FilingAttachAPIHandler},
		// 部材見積依頼（2026-10-03・段1——rfq.go・rfq_api.go）。
		{Pattern: "/api/rfq/collect", Handler: RFQCollectAPIHandler},
		{Pattern: "/api/rfq/folder-products", Handler: RFQFolderProductsAPIHandler},
		// 部材見積依頼書ページを作る（2026-10-03・段2——rfq_doc.go）。
		{Pattern: "/api/rfq/new", Handler: RFQNewDocAPIHandler},
		// 過去の見積もりを部材見積依頼書ページへ移す（2026-10-04・管理者だけ——rfq_doc.go）。
		{Pattern: "/api/rfq/import", Handler: RFQImportAPIHandler},
		{Pattern: "/api/rfq/fill-names", Handler: RFQFillNamesAPIHandler},
		{Pattern: "/api/rfq/set-cells", Handler: RFQSetCellsAPIHandler},
		// 過去の販売価格を見積書ページへ移す（2026-10-04・管理者だけ——estimate_import.go）。
		{Pattern: "/api/estimate/import", Handler: EstimateImportAPIHandler},
		// 見積計算書を作る（2026-10-16・estimate_calc_doc.go——加工製品ページの「🧮 見積計算書」の「➕ 新しい見積計算書を作る」）。
		{Pattern: "/api/estimate-calc/new", Handler: EstimateCalcNewAPIHandler},
		// 納品書を作る（2026-10-17・delivery.go——2026-10-22 からは製造ページの納品作業からだけ。案件残表の「🚚 選んだ行で納品書を作る」は外した）。
		{Pattern: "/api/delivery/new", Handler: DeliveryNewAPIHandler},
		{Pattern: "/api/delivery-pdf", Handler: DeliveryPDFAPIHandler}, // 納品書の紙（2026-10-17・delivery_pdf.go）
		{Pattern: "/api/mfg/estimate", Handler: MfgEstimateAPIHandler}, // 製造の見積書（2026-10-19・quote_done.go）
		{Pattern: "/api/mfg/close", Handler: MfgCloseAPIHandler}, // 製造を閉じる（記録へ・2026-10-19・mfg_close.go）
		{Pattern: "/api/mfg/parts", Handler: MfgPartsAPIHandler},                  // 選んだ製造明細の行の部材を必要部材表へ（2026-10-21 夕・mfg_work.go）
		{Pattern: "/api/mfg/needs", Handler: MfgNeedsAPIHandler},                  // 必要部材表の選んだ行を発注・見積もり・不要・外す（2026-10-21 夕・mfg_work.go）
		{Pattern: "/api/mfg/delivery-draft", Handler: MfgDeliveryDraftAPIHandler}, // 選んだ製造明細の行で納品表を作る（2026-10-21 夕・mfg_work.go）
		{Pattern: "/api/mfg/skip", Handler: MfgSkipAPIHandler},              // 発注部材表の行を手配不要表へ（2026-10-21・mfg_skip.go）
		{Pattern: "/api/our-order/unsend", Handler: OrderUnsendAPIHandler}, // 送ったことを取り消す（2026-10-21・order_unsend.go）
		{Pattern: "/api/our-order/bind-candidates", Handler: OrderBindCandidatesAPIHandler}, // 行を結ぶ相手の候補（2026-10-23・order_bind.go）
		{Pattern: "/api/our-order/bind", Handler: OrderBindAPIHandler},                       // 行を加工製品の部材の行に結ぶ（同）
		{Pattern: "/api/mfg/skip/remove", Handler: MfgSkipRemoveAPIHandler}, // 手配不要表の行を外す（2026-10-21・mfg_skip.go）
		{Pattern: "/api/mfg/made", Handler: MfgMadeAPIHandler},         // 製造完了（2026-10-19・mfg_view.go）
		{Pattern: "/api/mfg/new", Handler: MfgNewAPIHandler},           // 製造ページ（2026-10-19・mfg.go）
		{Pattern: "/api/mfg/wait", Handler: MfgWaitAPIHandler},         // 製造待ちへ入れる（2026-10-21・mfg_wait.go）
		{Pattern: "/api/mfg/unwait", Handler: MfgUnwaitAPIHandler},     // 製造待ちから案件へ戻す（同）
		{Pattern: "/api/mfg/start", Handler: MfgStartAPIHandler},       // 製造待ちから開始する（同）
		{Pattern: "/api/mfg/back", Handler: MfgBackAPIHandler},         // 製造明細から製造待ちへ戻す（同）
		{Pattern: "/api/invoice/new", Handler: InvoiceNewAPIHandler},   // 請求書（2026-10-17・invoice.go）
		{Pattern: "/api/invoice-pdf", Handler: InvoicePDFAPIHandler},  // 請求書の紙（同・invoice_pdf.go）
		// 表の末尾へ行を足す（2026-10-04・管理者だけ——見積から加工製品の外注加工に塗装の行を足す道具が使う・table_append.go）。
		{Pattern: "/api/table/append-rows", Handler: AppendTableRowsAPIHandler},
		// 並べた図面の片方を旧版へ——あとから改定にする（2026-10-04・drawing_supersede.go）。
		{Pattern: "/api/drawing/supersede", Handler: DrawingSupersedeAPIHandler},
		// 部材見積依頼書の紙・備考・送った（FAX・手渡し）（2026-10-03・段2の後半——rfq_pdf.go）。
		{Pattern: "/api/rfq-pdf", Handler: RFQPDFAPIHandler},
		{Pattern: "/api/rfq-pdf-docs", Handler: RFQPDFDocsAPIHandler}, // 資料を綴じた FAX・印刷用（2026-10-05）
		{Pattern: "/api/rfq/note", Handler: RFQNoteAPIHandler},
		// 部材見積依頼の返事——回答を記録・🤖 返事を読む（候補・読む・書く）（2026-10-05・段4の前半——rfq_reply.go）。
		{Pattern: "/api/rfq/answered", Handler: RFQAnsweredAPIHandler},
		{Pattern: "/api/rfq/reply-sources", Handler: RFQReplySourcesAPIHandler},
		{Pattern: "/api/rfq/read-reply", Handler: RFQReadReplyAPIHandler},
		{Pattern: "/api/rfq/apply-reply", Handler: RFQApplyReplyAPIHandler},
		// 部材見積依頼の返事ページを部材見積依頼書に結ぶ（2026-10-05・rfq_reply_page.go）。
		{Pattern: "/api/rfq-reply/link", Handler: RFQReplyLinkAPIHandler},
		{Pattern: "/api/rfq-reply/ocr", Handler: RFQReplyOCRAPIHandler}, // 返事ページから Gemini で読む（2026-10-21 夜・rfq_reply_ocr.go）
		// 部材見積依頼の記録が無い返事から部材見積依頼書ページを作る（2026-10-05・移行期——rfq_reply_make.go）。
		{Pattern: "/api/rfq-reply/draft", Handler: RFQReplyDraftAPIHandler},
		{Pattern: "/api/rfq-reply/make-rfq", Handler: RFQReplyMakeRFQAPIHandler},
		{Pattern: "/api/rfq/sent", Handler: RFQSentAPIHandler},
		// 見積書を作る——装置フォルダから選ぶ（2026-10-03・estimate_folder.go）。
		{Pattern: "/api/estimate/folder-products", Handler: EstimateFolderProductsAPIHandler},
		{Pattern: "/api/estimate/from-folder", Handler: EstimateFromFolderAPIHandler},
		// 未送付の見積書の「送った（FAX・手渡し）」「送付不要」（2026-10-03・estimate_unsent.go）。
		{Pattern: "/api/estimate/mark", Handler: EstimateMarkAPIHandler},
		// 見積書の備考を見積明細表の下で書く（2026-10-03・estimate_note.go）。
		{Pattern: "/api/estimate/note", Handler: EstimateNoteAPIHandler},
		{Pattern: "/api/rfq/needs/move", Handler: RFQNeedsMoveAPIHandler},
		{Pattern: "/api/rfq/needs/remove", Handler: RFQNeedsRemoveAPIHandler},
		{Pattern: "/api/rfq/draft/back", Handler: RFQDraftBackAPIHandler},
		{Pattern: "/api/file-drawings", Handler: FileDrawingsAPIHandler},
		// 整理を待つ図面と既にある加工製品の図面を、Gemini に見比べさせる口（2026-09-30・filing_duplicate.go）。
		// 押したときだけ呼ぶ——同じファイルなら Gemini を呼ばずに答える。
		{Pattern: "/api/compare-drawings", Handler: CompareDrawingsAPIHandler},
		// 加工製品の置き場（製造台帳／社名／装置名称）を用意する口（2026-09-29・
		// ワンノートの移植の道具が使う。木の形を知るのは product_tree.go の1つ）。
		{Pattern: "/api/product-folder", Handler: ProductFolderAPIHandler},
		// ⚠ 受注の行を結び直す管理者の口（/api/order-items/relink）は 2026-10-01 に外した——受注フォルダを
		// 開いたときに結ぶ（backlog.go の linkBacklogRows・利用者「管理者メニューで直すのは編集者にとってきつそうです」）。
		// 解析済みの印（添付ID → 生まれたページ）。読むだけで何も作りません。
		{Pattern: "/api/analyzed", Handler: AnalyzedAPIHandler},
		// 受注残表から受注明細表の1セルを書き換える口（2026-09-21・order_edit.go）。
		// ⚠ **計算ビューで初めての「書ける鏡」**です——押した先が**別のページの
		// 本文**になるので、行番号と `品番` の2つで照合し、いまの値とも突き合わせ、
		// 編集ロックの関門を通します（2026-10-12 からは、道具が `/api/lock` で握っているときだけ断る）。
		{Pattern: "/api/order-item", Handler: OrderItemEditAPIHandler},
		// 部材発注書のPDFを作って、そのページの添付として残す口（2026-09-21・order_pdf.go）。
		// ⚠ **7年保存がこれで済みます**——ファイルがページと一緒に残り、版も監査も付きます。
		{Pattern: "/api/order-pdf", Handler: OrderPDFAPIHandler},
		{Pattern: "/api/order-pdf-docs", Handler: OrderPDFDocsAPIHandler}, // 資料を綴じた FAX・印刷用（2026-09-28）
		// 材質・形状・寸法で材料を探す口（2026-09-21・dimension_search.go）。
		// ⚠ **読めないページは混ぜません**——材料表には誰でも行を書けるので、
		// 絞らないと読めない部材発注書の単価と仕入先が引けてしまいます。
		{Pattern: "/api/material-search", Handler: MaterialSearchAPIHandler},
		// 未手配の一覧から部材発注書を1枚作る口（2026-09-21・our_order_new.go）。2026-09-22 からは発注部材表の足元の
		// 「部材発注書を作る」が呼びます（order_draft_mirror.go）。
		// ⚠ **仕入先が無ければ作りません**——1枚＝1社が部材発注書の単位です。
		{Pattern: "/api/our-order/new", Handler: NewOurOrderAPIHandler},
		// 未発注の表（いまの名前は必要部材表）から**発注部材表**を1つ作る口（2026-09-22・order_draft.go）。
		// ⚠ **行が0でも作ります**——空の表から始められることが、加工製品ページに
		// 無い部材（消耗品・治具）だけを買うときの道です。
		// 発注部材表から1行を外す口（2026-09-22・order_draft.go）。
		// ⚠ **「戻す」は「外す」です**——一覧は毎回計算される鏡なので、
		// ここから消せば**自動的に一覧へ戻ってきます**。
		{Pattern: "/api/our-order/draft/remove", Handler: RemoveOrderDraftRowAPIHandler},
		// 部材発注明細表の1行の `状態` を書き換える口（2026-09-22・order_status.go）。
		// ⚠ **発注済みの印は行に付きます**（ユーザー:「発注書の表の**一品ずつに
		// 発注済みの印**をつけます」）——1枚の紙の中でも、**1品だけ取り消す**・
		// **1品だけ先に納まる**が起こるためです。
		{Pattern: "/api/our-order/line-status", Handler: OrderLineStatusAPIHandler},
		// 部材発注明細表の1行を外して必要部材表へ戻す口（2026-09-24・order_return.go）。
		// ⚠ **取消とは別**——取消は「もう発注しない」（行は残る）、戻すは
		// 「この部材発注書では発注しない」（行が消え、必要部材表でまた選べる）。
		{Pattern: "/api/our-order/return-row", Handler: OrderReturnRowAPIHandler},
		// 部材発注書を出したことを反映する口（2026-09-22・order_send.go）。
		// ⚠ **取消でない行すべて**に `発注済` を付けます。メールは送信の成功が
		// その事実、FAX・手渡しは**人が押したこと**がその事実です。
		{Pattern: "/api/our-order/sent", Handler: OrderSentAPIHandler},
		// 部材発注書の備考の欄を保存する口（2026-10-14・order_pdf_sent.go——見積書の /api/estimate/note と同じ形）。
		{Pattern: "/api/our-order/note", Handler: OrderNoteAPIHandler},
	}
}
