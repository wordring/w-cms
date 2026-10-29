package cms

// ─────────────────────────────────────────────────────────────────────────
// 保存済み文書の版管理（リビジョン／リバート）
//
// 設計の正本は [docs/考察/【考察】アンドゥ・リドゥ.md] §4・§5。編集中の Ctrl+Z（①）とは
// 別物で、こちらは**版履歴からの復元**です。
//
// 置き場は正本と同じファイル層です（`data/master/<prefix>/<id>/versions/`）。
// 本システムは**正本＝ファイル／DBは派生**なので、履歴もファイル層に置くのが自然で、
// **リバート＝「古いHTMLをもう一度保存する」だけ**になります（`SyncIndex` がDBを作り直す）。
// 履歴は**線形**です（マージ不要）。2026-10-12 までは悲観ロックで1ページ＝単一書き手だったため、
// いまは同じページへの書き込みをページごとの書き込みの錠（body_lock.go）が1つずつ通すためです。
// 2人が同じページを書けるようになったので、1つの版の中身には別の人の書いたブロックも含まれます
// （版の `By` は、その版を最後に保存した人です）。
//
// 対象は**本文HTMLだけ**（設計 §5）。サイドカー（親・所有者・権限・公開）・添付バイナリは
// 対象外です。これは容量の都合ではなく**安全のための線引き**で、リバートで過去の権限や
// 親が復活しないこと＝「古い版に戻す」が権限昇格やページ移動の抜け道にならないことを
// 構造的に保証します。
//
// 3つの約束:
//
//  1. **毎回の保存で版を作らない**（コアレッシング）。保存はブロックを去るたびに飛ぶので
//     （2026-10-12 昼まではオートセーブが1〜2秒ごとに飛んでいました）、
//     同じ編集者の連続保存は versionCoalesceWindow の間**最新の内容へ置き換え**て
//     ひとまとめにします（窓の起点は版ID＝作成時刻。窓を超えたら次の版が始まる）。
//     編集者が変わったときは必ず切ります——誰の書いたものかが混ざると後から辿れないからです。
//
//     かつては「窓の内側の保存は**捨てる**」方式で、1セッションが〈最初の保存＝
//     ほぼ編集前の内容〉と〈ロック解放時のチェックポイント＝最終状態〉の**ほぼ同じ2版**を
//     残していました（「版の履歴が細かすぎます」——2026-08-31 ユーザー指摘）。
//     置き換え方式なら窓の版が常に最新を持つため、チェックポイントは同内容で自然に
//     スキップされ、**1セッション＝1版**になります。
//     2026-10-12 から、編集権（ロック）が無くなったので、区切りは画面が閲覧モードへ切り替えたとき・
//     ページを離れるときに `POST /api/version-checkpoint` で打ちます（handler_version.go）。区切った版は
//     閉じ（`Sealed`）、あとの保存はそこへ畳まずに次の版を始めます。
//  2. ⚠ **版は消しません。消す機構も持ちません**（2026-09-23 ユーザー決定:
//     「**5年で消す動作をやめましょう**」「**消さないの意味は、消す機構も持たないで
//     良いです。もしも消す必要が出来たら、エージェントで消します**」）。
//
//     ⚠ **年限を1つに決められないことが分かったのが、この決定の中身です**——帳票の
//     保持義務は**個人5年・法人7年・過去に問題を起こしていた法人は最大10年**で、
//     **設置した先がどれかは w-cms には分かりません**（w-cms は開発元でない企業も
//     設置するソフトウェアです）。⚠ **そして消えた版は戻りません。**
//
//     ⚠ **「設定で 0 なら消さない」でもありません**——機構そのものを持たないので、
//     **設定の書き間違いでも消えません**。**消してよいと判断できる場所が、この
//     ファイルの中に1つもない**という形にしてあります。
//     ⚠ **それまでは 5年で消していました**——2026-08-21 の決定は「5年は消さない」という
//     **下限**の話だったのに、実装は**上限**になっていました（この会社は法人なので
//     2年足りません）。
//  3. **各版は自己完結**（gzip フル圧縮・差分チェーンなし）。復元にチェーンが要らず、
//     1つ壊れても他へ波及しません。Go 標準に diff/patch が無い、という事情とも合います。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

const (
	// versionsDirName はページのフォルダ内で版を置く場所です。
	versionsDirName = "versions"

	// versionCoalesceWindow は同じ編集者の連続保存をひとまとめにする長さです。
	// 保存のたびに版にすると履歴が使い物にならなくなるため（2026-10-12 昼まではオートセーブが1〜2秒ごと。
	// いまはブロックを去るたび）。
	versionCoalesceWindow = 10 * time.Minute

	// versionTimeLayout は版のファイル名に使う時刻表記です。
	// RFC3339 は `:` を含みファイル名に使えないので、詰めた形にします。
	versionTimeLayout = "20060102T150405Z"
)

func init() {
	// 編集の区切り（ロックを手放したとき）に明示チェックポイントを打つ。
	// コアレッシングの窓（10分）の内側で編集を終えた人の最終形を残すための契機で、
	// 中身が最新の版と同じなら何も起きない（RecordVersion が重複を作らない）。
	// 2026-10-12 から画面はロックを取らないので、これが動くのは道具が /api/lock を手放したときだけ。
	// 画面の書き終わりの区切りは VersionCheckpointAPIHandler（handler_version.go）が打つ。
	editlock.OnRelease = func(pageIDInt int, username string) {
		id := page.FormatID(pageIDInt)
		// 読んで版にするまでを、ページの書き込みのロックの中で（人の保存と重なると、古い本文が最新の版になる・2026-10-22 の見直し）。
		// 呼ばれるのは /api/unlock からだけで、本文のロックを持ったまま呼ばれることは無い。
		defer LockPageBody(id)()
		body, err := os.ReadFile(page.BodyPath(id))
		if err != nil {
			return // 本文が無いページ（削除直後など）は放っておく
		}
		if err := RecordVersion(id, username, string(body), true); err != nil {
			log.Printf("ロック解放時の版記録に失敗しました page=%s: %v", id, err)
		}
	}
}

// VersionInfo は版1つ分の情報です（`<版ID>.json` の中身がそのまま届きます）。
type VersionInfo struct {
	ID   string `json:"id"`   // 版ID（= ファイル名の幹。時刻由来）
	At   string `json:"at"`   // その内容が正本になった時刻（RFC3339・UTC）
	By   string `json:"by"`   // 保存した利用者
	Size int    `json:"size"` // 本文のバイト数（圧縮前）
	Hash string `json:"hash"` // 本文の SHA-256（重複を作らないための判定に使う）
	// Sealed は区切りを打った版（2026-10-12）——同じ人の10分以内の保存でも、この版へは畳まない（次の保存は新しい版）。
	// 書き終えたとき（閲覧モードへ切り替えた・ページを離れた）の形を、あとの保存で上書きさせないため。
	Sealed bool `json:"sealed,omitempty"`
}

// versionsDir はページの版置き場を返します。
func versionsDir(pageID string) string {
	return filepath.Join(page.GetPageDir(pageID), versionsDirName)
}

// versionID は時刻から版IDを作ります。
func versionID(at time.Time) string {
	return at.UTC().Format(versionTimeLayout)
}

// safeVersionID は利用者から届いた版IDを検証します。
//
// 版IDはAPIの引数として外から来るので、**ページのフォルダの外を指させない**ことが
// ここの仕事です（添付の `SafeAttachmentName` と同じ役目）。時刻由来の形しか
// 受け付けないので、パス要素も拡張子も混ざりようがありません。
func safeVersionID(v string) (string, error) {
	if v == "" {
		return "", errors.New("版IDが指定されていません")
	}
	if strings.ContainsAny(v, `/\.`) {
		return "", errors.New("版IDが不正です")
	}
	if _, err := time.Parse(versionTimeLayout, v); err != nil {
		return "", errors.New("版IDが不正です")
	}
	return v, nil
}

// RecordVersion は本文を版として残します。
//
// force が false のときはコアレッシングが効きます——同じ編集者の直近の版が
// versionCoalesceWindow 以内（起点は版ID＝作成時刻）なら、**その版を最新の内容へ
// 置き換え**ます。true は必ず新しい版を切る**区切り**で、リバート（直前の退避と
// 戻した記録が別の版であるべき）と、書き終わりのチェックポイント（ロック解放時・
// 2026-10-12 からは /api/version-checkpoint）が使います。どちらの場合も、
// **中身が最新の版と同じなら新しい版は作りません**——チェックポイントは
// 置き換え方式では常に同内容になるため、その版を閉じる（`Sealed`）だけです。
func RecordVersion(pageID, author, html string, force bool) error {
	pageID, ok := page.NormalizeID(pageID)
	if !ok {
		return errors.New("ページIDが不正です")
	}
	dir := versionsDir(pageID)
	list, err := ListVersions(pageID)
	if err != nil {
		return err
	}

	hash := sha256Hex([]byte(html))

	if len(list) > 0 {
		newest := list[0]
		// 中身が同じなら、どの契機でも版は増やさない。
		// 区切り（force）なら、その版を閉じる——中身が同じでも、次の保存が上書きしないように（Sealed）。
		if newest.Hash == hash {
			if force && !newest.Sealed {
				info := newest
				info.Sealed = true
				return writeVersionFiles(dir, newest.ID, info, []byte(html))
			}
			return nil
		}
		if !force && newest.By == author && !newest.Sealed {
			// 窓の起点は**版ID（作成時刻）**。At を起点にすると置き換えのたびに
			// 窓が滑り、書き続ける限り永遠に1版へ畳まれてしまう。IDなら
			// 「10分ごとに次の版」という粒度が保たれる。
			if at, err := time.Parse(versionTimeLayout, newest.ID); err == nil &&
				time.Since(at) < versionCoalesceWindow {
				// 連続保存はひとまとめ＝この版を最新の内容へ置き換える。
				// At は実際の内容の時刻へ進める（IDは窓の起点として据え置き）。
				info := newest
				info.At = time.Now().UTC().Format(time.RFC3339)
				info.Size = len(html)
				info.Hash = hash
				return writeVersionFiles(dir, newest.ID, info, []byte(html))
			}
		}
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	now := time.Now()
	id := versionID(now)
	// 同じ秒に2つ作ろうとしたら1秒ずらす（版IDは時刻そのものなので衝突しうる）。
	for i := 0; i < 60; i++ {
		if _, err := os.Stat(filepath.Join(dir, id+".json")); os.IsNotExist(err) {
			break
		}
		now = now.Add(time.Second)
		id = versionID(now)
	}

	info := VersionInfo{
		ID:   id,
		At:   now.UTC().Format(time.RFC3339),
		By:   author,
		Size: len(html),
		Hash: hash,
	}
	return writeVersionFiles(dir, id, info, []byte(html))
}

// writeVersionFiles は版の2ファイル（本文の gzip と情報の JSON）を書きます。
// **本文を先に書き、情報を後に書く**——途中で落ちても「情報はあるが本文が無い」版を
// 作らないためです（ListVersions は情報ファイルを起点に数えます）。
func writeVersionFiles(dir, id string, info VersionInfo, body []byte) error {
	info.ID = id
	if info.Hash == "" {
		info.Hash = sha256Hex(body)
	}
	if info.Size == 0 {
		info.Size = len(body)
	}

	// 版は書き直さない一度きりのファイルだが、書き込み中に落ちると壊れた
	// アーカイブが「ある」ことになってしまうため、原子的に置く。
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(body); err != nil {
		zw.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	gzPath := filepath.Join(dir, id+".html.gz")
	if err := page.WriteFileAtomic(gzPath, buf.Bytes(), 0644); err != nil {
		return err
	}

	meta, err := json.Marshal(info)
	if err != nil {
		os.Remove(gzPath)
		return err
	}
	if err := page.WriteFileAtomic(filepath.Join(dir, id+".json"), meta, 0644); err != nil {
		os.Remove(gzPath)
		return err
	}
	return nil
}

// ListVersions は版を**新しい順**に返します。読めない版は黙って飛ばします
// （1つ壊れても履歴全体が使えなくなるのは困るため。各版は自己完結）。
func ListVersions(pageID string) ([]VersionInfo, error) {
	pageID, ok := page.NormalizeID(pageID)
	if !ok {
		return nil, errors.New("ページIDが不正です")
	}
	dir := versionsDir(pageID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []VersionInfo{}, nil
		}
		return nil, err
	}
	out := []VersionInfo{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var info VersionInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		info.ID = id // ファイル名を正とする（中身の id とずれても迷わない）
		if _, err := os.Stat(filepath.Join(dir, id+".html.gz")); err != nil {
			continue // 本文が無い版は無かったことにする
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// ReadVersion は版の本文を返します。
func ReadVersion(pageID, version string) ([]byte, error) {
	pageID, ok := page.NormalizeID(pageID)
	if !ok {
		return nil, errors.New("ページIDが不正です")
	}
	id, err := safeVersionID(version)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(versionsDir(pageID), id+".html.gz"))
	if err != nil {
		return nil, fmt.Errorf("その版はありません")
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("版を読めません: %v", err)
	}
	defer zr.Close()
	// 壊れた・細工された gzip で無制限に展開しないよう上限を掛ける。
	// ⚠ 上限を超えたら黙って切らずに断る——機械が書いた本文は 8MiB を超えることがあり、切ったまま戻すと
	// 後ろが消える（2026-10-22 の見直し）。上限は、ふだんの本文よりずっと大きく取る。
	b, err := io.ReadAll(io.LimitReader(zr, maxVersionBytes+1))
	if err != nil {
		return nil, fmt.Errorf("版を読めません: %v", err)
	}
	if len(b) > maxVersionBytes {
		return nil, fmt.Errorf("版が大きすぎて読めません（%d MiB まで）", maxVersionBytes>>20)
	}
	return b, nil
}

// maxVersionBytes は、版を開くときの上限です（展開したあとの大きさ）。
const maxVersionBytes = 64 << 20

// RevertToVersion は選んだ版を現在の本文として書き戻します。
//
// **戻すのは本文だけ**です（設計 §5）。サイドカーは触らないので、権限・所有者・親・公開は
// 現状のまま——だから「古い版に戻す」が権限昇格の抜け道になりません。
// 書き戻す前に**いまの内容を版として残す**ので、リバートそのものも取り消せます。
func RevertToVersion(pageID, version, author string) error {
	pageID, ok := page.NormalizeID(pageID)
	if !ok {
		return errors.New("ページIDが不正です")
	}
	body, err := ReadVersion(pageID, version)
	if err != nil {
		return err
	}

	// ページごとの書き込みの錠（body_lock.go・2026-10-12）——戻すのも「ほかの書き手の1人」（問い7）。
	defer LockPageBody(pageID)()
	htmlPath := page.BodyPath(pageID)
	current, curErr := os.ReadFile(htmlPath)

	// 書き戻す本文もサニタイズを通す。過去の版は当時の許可リストで通ったもので、
	// 許可が狭まっていれば**いまの規則で**濾すのが正しい（保存経路と同じ扱い）。
	safeHTML := Sanitize(string(body))
	if curErr == nil {
		// 作るときに決める語は、古い版へ戻しても、管理者でない人にはいまの値のまま（2026-10-27・fixed_tag.go）。
		safeHTML, _ = keepFixedTagsByName(author, string(current), safeHTML)
		defer noteBlockWriters(pageID, author, string(current), safeHTML)
		// いまの内容を必ず残す（窓の内側でも force で切る）。
		if err := RecordVersion(pageID, author, string(current), true); err != nil {
			return err
		}
	}
	if err := page.WriteFileAtomic(htmlPath, []byte(safeHTML), 0644); err != nil {
		return err
	}
	// 更新日時は「今」前進する（サイドカーが正本。リバートも内容の変更なので）。
	updatedAt, err := page.BumpUpdatedAt(pageID)
	if err != nil {
		return err
	}
	// 開いている人へ知らせる——索引と版まで済ませてから（page_events.go）。
	defer notifyPageUpdated(pageID, author, updatedAt)
	if err := SyncIndex(pageID, safeHTML); err != nil {
		return err
	}
	// 戻した内容も版として残す（履歴が「いま何が正本か」で終わるように）。
	return RecordVersion(pageID, author, safeHTML, true)
}
