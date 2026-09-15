package cms

// ページの木を作る汎用の道具（2026-09-15 に intake.go から分けた）。
//
// **通信箱のためだけのものではありません**——下請け（ext/subcon）の解析・整理・改訂、
// アドレス帳（ext/comm/contacts）の取引先ページ、受注の `受注／年／月` がみな使います。
// intake.go（通信箱の取り込み）に同居していたので、通信の語彙を `ext/comm` へ移すと
// これらがまとめて壊れるところでした（組み替えの3段目の下ごしらえ・
// docs/【考察】通信拡張と下請け拡張への組み替え.md §3.2）。

import (
	"database/sql"
	"html"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// CreateChildPage はページ作成の芯です（権限は親から継承・サニタイズ・索引まで。
// 監査は呼び手が録る——取り込みと解析で行為名が違うため）。
// 取り込み（IntakeContext）とPDF解析ボタン（analyze_pdf.go）が共用します。
func CreateChildPage(parentID, owner, bodyHTML string) (string, error) {
	parentInt, err := strconv.Atoi(parentID)
	if err != nil {
		return "", err
	}
	newID, err := reserveNewPageID(sql.NullInt64{Int64: int64(parentInt), Valid: true})
	if err != nil {
		return "", err
	}

	safeHTML := Sanitize(bodyHTML)
	dir := page.GetPageDir(newID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if err := page.WriteFileAtomic(filepath.Join(dir, newID+".html"), []byte(safeHTML), 0644); err != nil {
		return "", err
	}

	pp := page.GetPerms(parentInt)
	if err := page.EnsureSidecar(newID, owner, pp.Group, pp.Mode, parentID); err != nil {
		log.Printf("取り込みページのサイドカー作成に失敗 page=%s: %v", newID, err)
	}
	if err := SyncIndex(newID, safeHTML); err != nil {
		return "", err
	}
	return newID, nil
}

// setSortKey は作ったページの並び順キーをサイドカーへ書きます。
//
// **取り込みは順序を知っています**（メールなら受信日時）。それを入れておけば、
// 月フォルダの中が届いた順に並びます——取り込んだ順ではなく。
// 人があとでドラッグで並べ替えれば、同じ欄が上書きされるだけです。
func setSortKey(pageID, key string) {
	if key == "" {
		return
	}
	meta, ok := page.ReadSidecar(pageID)
	if !ok {
		return
	}
	meta.SortKey = key
	if err := page.WriteSidecar(pageID, meta); err != nil {
		log.Printf("並び順キーを書けませんでした page=%s: %v", pageID, err)
	}
}

// ensureDateFolderUnder は root の下に「年／月」を必要なだけ作ります。
func ensureDateFolderUnder(rootID, owner string, t time.Time) (string, error) {
	local := t.In(time.Local)
	year, err := ensureFolderUnder(rootID, owner, local.Format("2006年"))
	if err != nil {
		return "", err
	}
	return ensureFolderUnder(year, owner, local.Format("01月"))
}

// IsDateFolderTitle は年・月フォルダの題かを返します（`2026年` / `09月`）。
//
// **ここに置くのは、作る側（ensureDateFolderUnder）の隣だから**です。形を変えるなら
// 両方を同時に直すことになり、片方だけずれません。未処理の一覧が「フォルダは仕事
// ではない」と判断するのに使います（view_unhandled.go）。
//
// 題で見るのは割り切りです——人が「2026年」という名前のページを作れば、それも
// フォルダとみなされます。**そういうページは実際フォルダ**なので、実害はありません。
func IsDateFolderTitle(title string) bool {
	t := strings.TrimSpace(title)
	if _, err := time.Parse("2006年", t); err == nil {
		return true
	}
	_, err := time.Parse("01月", t)
	return err == nil
}

// ensureFolderUnder は親の下に題の一致する子を探し、無ければ作ります。
//
// **一致は完全一致だけ**です。取り込みは無人で何度も走るので、揺れを吸収すると
// 気づかないうちに似た名前のフォルダが増えます。
//
// フォルダは IntakeContext の created へ入りません——SaveAttachment / UpdatePage
// の対象は「この取り込みで作った記録ページ」に限る、という最小権限の線を崩さない
// ためです（そもそもここは IntakeContext を通らない）。
func ensureFolderUnder(parentID, owner, title string) (string, error) {
	parentInt, err := strconv.Atoi(parentID)
	if err != nil {
		return "", err
	}
	var id int
	if err := database.DB.QueryRow(
		`SELECT id FROM pages WHERE parent_id = ? AND title = ? ORDER BY id ASC LIMIT 1`,
		parentInt, title).Scan(&id); err == nil {
		return page.FormatID(id), nil
	}
	newID, err := CreateChildPage(parentID, owner, "<h1>"+html.EscapeString(title)+"</h1>")
	if err != nil {
		return "", err
	}
	auth.Audit(owner, "intake.folder", newID+" under "+parentID+" ("+title+")")
	return newID, nil
}

// EnsureTopLevelBox は題の一致するトップ直下のページを返し、無ければ作ります。
//
// **箱を作るのはコア、名前を決めるのは使う側**です。通信箱（MailBoxTitle）は
// 「そこへ落とすと取り込みが走る」機能の入口なので人が意図して置きますが、
// **ただの置き場（取引先・受注など）は行き止まりにする理由がありません**。
// 業務の言葉（「受注」）を持つのは拡張の側で、ここには置きません。
func EnsureTopLevelBox(title, owner string) (string, error) {
	if id, ok := TopLevelPageByTitle(title); ok {
		return id, nil
	}
	return CreateChildPage(TopPageID, owner, "<h1>"+html.EscapeString(title)+"</h1>")
}

// EnsureDateFolders は root の下に年フォルダ・月フォルダを用意し、月フォルダを返します。
//
// 通信箱の取り込みが使う ensureDateFolderUnder と**同じ芯**です——受注の置き場も
// 年月で並べる（2026-09-06 ユーザー決定）ので、作法を2つ持たないよう口を開けました。
func EnsureDateFolders(rootID, owner string, t time.Time) (string, error) {
	return ensureDateFolderUnder(rootID, owner, t)
}
