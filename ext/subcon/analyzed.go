package subcon

// ─────────────────────────────────────────────────────────────────────────
// 解析済みの印（2026-09-06）
//
// ユーザー:「一度解析したファイルには解析済みの印と『図面』などの解析結果を
// 付けては？」「添付ファイルのボタンの横あたりで良いのでは？」
//
// **新しく持つデータはありません。** 解析が作るページには由来の参照タグ
// `受信元：<元ページID>-<添付ID>` が既に入っているので、**逆に引けば
// 「この添付から何が生まれたか」が分かります**（アドレス帳・社名の揺れと同じ形
// ——材料はもう索引にある）。
//
// 印を別に持たない利点が1つあります。**生まれたページを消せば印も消えます**
// ——間違った解析をゴミ箱へ入れたのに「解析済み」が残ると、二度と解析できません。
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

// 加工製品ページの可変タグの名前です（2026-09-18 にヘッダの定義リストから移した）。
//
// ⚠ **`図面番号` は設定で `code` 型**なので、畳んだ一致で引けます（`PagesByTagLoose`）
// ——空白・ハイフン・長音・大小の揺れを越えて当たります。ワンノートの取りこぼしが
// ここでした。`図面名称`・`装置名称`・`客先` は `text`（長音には触らない）。
const (
	DrawingNoTag   = "図面番号"
	DrawingNameTag = "図面名称"
	MachineNameTag = "装置名称"
	ClientNameTag  = "客先"
)

// 受注ページ（顧客の発注書）の可変タグの名前です（2026-09-18 にヘッダから移した）。
//
// ⚠ **`発注書番号` は `code`・`発注日` は `date`**（設定の語彙）。日付は畳んだ値が
// ISO になるので、**範囲で引く口を足すときもそのまま効きます**。
const (
	OrderNoTag     = "発注書番号"
	OrderClientTag = "発注元"
	OrderedAtTag   = "発注日"
	// DueDateTag は納期です。⚠ **行ではなくページのタグ**（2026-09-20 ユーザー:
	// 「納期は、各行ではなく、**表とは別に発注書の最初の方にあります**」）。
	// ヘッダにあるものはページのタグになる——**1文書＝1ページ**の規則どおりです。
	DueDateTag = "納期"
	// 発注書の右下に飛び出している3つの額です（2026-09-20 ユーザー:「変形した表に
	// なっていて、**表の右下の方に、小計、消費税、合計金額が飛び出しています**」）。
	//
	// ⚠ **検算のために取ります**（[checksum.go]）——`Σ金額 = 小計` が**行の落丁を
	// 捕まえる唯一の手段**、`小計 + 消費税 = 合計金額` が税率を当てずに小計と消費税を
	// 確かめる手段です。それ自体、受注額として見たい数でもあります。
	//
	// ⚠ **行ではなくページのタグ**——納期と同じで、書面のヘッダ（表の外）にある値です。
	SubtotalTag = "小計"
	TaxTag      = "消費税"
	TotalTag    = "合計金額"
	// SupplierTag は**弊社の発注書**の相手です（材料屋・加工業者）。⚠ 見積もりや
	// 部材の表にも `仕入先` の列がありますが、**あちらは行ごとの値**で別物です。
	SupplierTag = "仕入先"
)

// MigratingTag は「**移行したまま、まだ人が確かめていない**」ページの印です
// （2026-09-21 ユーザー提案:「移行期ですので、移行完了のタグを書く加工製品ページに
// 書き込んでも良いかもしれません。タグが無ければ新規のページです。移行タグに完了と
// 書いてあれば、**表を修正したので表引きして良い**ということになります」）。
//
// ⚠ **印を付けるのは「未確認」のほうです**（ユーザー選択）。直したら**タグごと消す**
// ので、**印の無いページが正常**になります——移行が終われば跡形もなく消えます。
//
// ⚠ **値ではなく、タグが在ることで止めます。** 誰かが知らない値を書いても
// **止まる側に倒れます**（フェイルクローズ）。値は人が読むためのもの。
//
// **なぜ要るか**: ワンノートの表には、同じ見た目で違う事情の行が混ざっています
// ——材料を買わない行、個数だけ書いてしまった行、ちゃんとした行。⚠ **機械には
// 見分けられません**。人が確かめた印だけが、表引きしてよい根拠になります。
const MigratingTag = "移行中"

// SourceRefTag は解析が書く由来の参照タグの名前です（`受信元`）。
// analyze_pdf.go が書く名前と揃えること——ここがずれると印が出なくなります。
const SourceRefTag = "受信元"

// analyzedResult は添付1件から生まれたページです。
type analyzedResult struct {
	Kind   string `json:"kind"`    // 図面 / 受注
	PageID string `json:"page_id"` // 生まれたページ
	Title  string `json:"title"`
}

// AnalyzedAPIHandler は GET /api/analyzed?page_id=X です。
// そのページの添付のうち、**解析済みのもの**を「添付ID → 結果」で返します。
func AnalyzedAPIHandler(w http.ResponseWriter, r *http.Request) {
	pageID, _, user, ok := cms.GateJSONPageRead(w, r, r.URL.Query().Get("page_id"))
	if !ok {
		return
	}
	out, err := analyzedAttachments(user, pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "調べられません: "+err.Error())
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "analyzed": out})
}

// analyzedAttachments は「添付ID → 生まれたページ」を返します。
func analyzedAttachments(user *auth.User, pageID string) (map[string]analyzedResult, error) {
	rows, err := database.DB.Query(
		`SELECT page_id, value FROM page_tags WHERE name = ? AND value LIKE ?`,
		SourceRefTag, pageID+"-%")
	if err != nil {
		return nil, err
	}
	type hit struct {
		id  int
		val string
	}
	// **先に読み切ってから解釈します**（CanView と VocabBlocksOf が別のクエリを投げる）。
	var found []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.val); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	out := map[string]analyzedResult{}
	for _, h := range found {
		attachID := strings.TrimPrefix(h.val, pageID+"-")
		if attachID == "" || strings.Contains(attachID, "-") {
			continue // 形が違うものは触らない
		}
		if !page.CanView(user, h.id) {
			continue // 見せ分け（C案）——読めないものは黙って落ちる
		}
		kind := kindOfPage(h.id)
		if kind == "" {
			continue
		}
		// 同じ添付から2枚あることは普通は無いが、あれば**先に見つかったほう**を出す
		// （どちらも本物なので、どちらを見せても行き止まりにならない）。
		if _, dup := out[attachID]; dup {
			continue
		}
		out[attachID] = analyzedResult{
			Kind: kind, PageID: formatID(h.id), Title: pageTitleOf(formatID(h.id)),
		}
	}
	return out, nil
}

// kindOfPage はそのページが図面ページか受注ページかを返します（どちらでもなければ空）。
func kindOfPage(pageIDInt int) string {
	// ⚠ **どちらもタグで見ます**（2026-09-18 に業務ブロックのヘッダから可変タグへ移した）。
	// 索引は1度だけ読みます。
	tags, err := cms.TagsOfPage(database.DB, pageIDInt)
	if err != nil {
		return ""
	}
	if cms.FirstTag(tags, DrawingNoTag) != "" {
		return "図面"
	}
	if cms.FirstTag(tags, OrderNoTag) != "" {
		return "受注"
	}
	return ""
}

// 受注明細の列の名前です。⚠ **`vocab.go` の `Label` と揃えること**——索引も本文の
// 書き換えも**見出しの表示文字**で引くので、ずれると黙って当たらなくなります。
//
// ⚠ **`OurItemNoTag` と `ItemNoTag` は別物です**（2026-09-20 ユーザー決定）:
//
//	弊社品番 … **製造製品ページのページID**。弊社の識別・加工中に使う番号
//	品番     … **顧客の言葉**。先方が発注に使った番号（客先によって図番のことも）
//
// 同じ列に混ぜると、顧客の品番で問い合わせが来たときに引けません。
const (
	OurItemNoTag = "弊社品番"
	ItemNoTag    = "品番"
)
