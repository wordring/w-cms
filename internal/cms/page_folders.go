package cms

// ページの木を作る汎用の道具（2026-09-15 に intake.go から分けた）。
//
// **通信箱のためだけのものではありません**——下請け（ext/toho）の解析・整理・改訂、
// アドレス帳（ext/comm/contacts）の取引先ページ、拡張が組む年月のフォルダ（東邦の `記録／年／月`）がみな使います。
// intake.go（通信箱の取り込み）に同居していたので、通信の語彙を `ext/comm` へ移すと
// これらがまとめて壊れるところでした（組み替えの3段目の下ごしらえ・
// docs/考察/【考察】通信拡張と下請け拡張への組み替え.md §3.2）。

import (
	"database/sql"
	"html"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// CreateChildPage はページ作成の芯です（権限は親から継承・サニタイズ・索引まで。
// 監査は呼び手が録る——取り込みと解析で行為名が違うため）。
// 取り込み（IntakeContext）とPDF解析ボタン（analyze_pdf.go）が共用します（いまは置き場・年月のフォルダ〔EnsureFolder・
// EnsureTopLevelBox・CreateMissingRequiredPages〕と、拡張が書類のページを作る口もここを通ります）。
func CreateChildPage(parentID, owner, bodyHTML string) (string, error) {
	parentInt, err := strconv.Atoi(parentID)
	if err != nil {
		return "", err
	}
	newID, err := reserveNewPageID(sql.NullInt64{Int64: int64(parentInt), Valid: true})
	if err != nil {
		return "", err
	}

	safeHTML := fillFileViewNamesAs(owner, Sanitize(bodyHTML))
	dir := page.GetPageDir(newID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if err := page.WriteFileAtomic(page.BodyPath(newID), []byte(safeHTML), 0644); err != nil {
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

// SetSortKey は作ったページの並び順キーをサイドカーへ書きます。
//
// **取り込みは順序を知っています**（メールなら受信日時）。それを入れておけば、
// 月フォルダの中が届いた順に並びます——取り込んだ順ではなく。
// 人があとでドラッグで並べ替えれば、同じ欄が上書きされるだけです。
func SetSortKey(pageID, key string) {
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
	y, m := DateFolderTitles(t)
	year, err := ensureFolderUnder(rootID, owner, y)
	if err != nil {
		return "", err
	}
	return ensureFolderUnder(year, owner, m)
}

// DateFolderTitles は年・月フォルダの題を返します（`2026年`・`09月`。IsDateFolderTitle と同じ形）。
//
// 拡張が自分で年月のフォルダを組むときに使います（2026-10-16・東邦の「記録」——フォルダの本文に
// 拡張の言葉〔`ページ種類`〕を入れるため。コアはその言葉を知りません）。形はここ1か所に置きます。
func DateFolderTitles(t time.Time) (year, month string) {
	local := t.In(time.Local)
	return local.Format("2006年"), local.Format("01月")
}

// EnsureFolder は親の下に題 title の子を探し、無ければ本文 bodyHTML で作ります（一致は完全一致だけ）。
//
// bodyHTML は作るときだけ使います（既にあれば触りません）。本文に何を入れるか（タグなど）は呼び手の言葉です
// ——コアは置き方だけを持ちます（2026-10-16）。
func EnsureFolder(parentID, owner, title, bodyHTML string) (string, error) {
	folderMu.Lock()
	defer folderMu.Unlock()
	if id, found := FindChildByTitle(parentID, title); found {
		return id, nil
	}
	newID, err := CreateChildPage(parentID, owner, bodyHTML)
	if err != nil {
		return "", err
	}
	auth.Audit(owner, "intake.folder", newID+" under "+parentID+" ("+title+")")
	return newID, nil
}

// folderMu は、フォルダと箱の「探して無ければ作る」を1つずつ通すロックです。
// ⚠ メールの自動の読み込み・FAX・電話が同時に届くと、どちらも「11月が無い」と見て2つ作り、あとの記録は
// 古いほうへ入って、新しいほうへ入った記録が置き去りになる（2026-10-22 の見直し）。
var folderMu sync.Mutex

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
	return EnsureFolder(parentID, owner, title, "<h1>"+html.EscapeString(title)+"</h1>")
}

// EnsureTopLevelBox は題の一致するトップ直下のページを返し、無ければ作ります。
//
// **箱を作るのはコア、名前を決めるのは使う側**です。通信箱（MailBoxTitle）は
// 「そこへ落とすと取り込みが走る」機能の入口なので人が意図して置きますが、
// **ただの置き場（製造・案件など）は行き止まりにする理由がありません**。
// 業務の言葉（「案件」——2026-10-19 までは「受注」）を持つのは拡張の側で、ここには置きません。
func EnsureTopLevelBox(title, owner string) (string, error) {
	folderMu.Lock()
	defer folderMu.Unlock()
	if id, ok := TopLevelPageByTitle(title); ok {
		return id, nil
	}
	// **同じ題のテンプレートをコピーして作る。無ければ作らない**（2026-09-27・利用者:「テンプレート
	// が無ければ作れないまで行きます」）——管理画面の「足りない置き場を作る」と同じ道（box_template.go）。
	body, err := boxBodyFromTemplate(title)
	if err != nil {
		return "", err
	}
	return CreateChildPage(TopPageID, owner, body)
}

// EnsureDateFolders は root の下に年フォルダ・月フォルダを用意し、月フォルダを返します。
//
// 通信箱の取り込みが使う ensureDateFolderUnder と**同じ芯**です——受注の置き場も（2026-10-16 までは）
// 年月で並べる（2026-09-06 ユーザー決定）ので、作法を2つ持たないよう口を開けました。
func EnsureDateFolders(rootID, owner string, t time.Time) (string, error) {
	return ensureDateFolderUnder(rootID, owner, t)
}
