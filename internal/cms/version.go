package cms

// ─────────────────────────────────────────────────────────────────────────
// 保存済み文書の版管理（リビジョン／リバート）
//
// 設計の正本は [docs/【考察】アンドゥ・リドゥ.md] §4・§5。編集中の Ctrl+Z（①）とは
// 別物で、こちらは**版履歴からの復元**です。
//
// 置き場は正本と同じファイル層です（`data/master/<prefix>/<id>/versions/`）。
// 本システムは**正本＝ファイル／DBは派生**なので、履歴もファイル層に置くのが自然で、
// **リバート＝「古いHTMLをもう一度保存する」だけ**になります（`Sync` がDBを作り直す）。
// 悲観ロックで1ページ＝単一書き手なので、履歴は**線形**（マージ不要）です。
//
// 対象は**本文HTMLだけ**（設計 §5）。サイドカー（親・所有者・権限・公開）・添付バイナリは
// 対象外です。これは容量の都合ではなく**安全のための線引き**で、リバートで過去の権限や
// 親が復活しないこと＝「古い版に戻す」が権限昇格やページ移動の抜け道にならないことを
// 構造的に保証します。
//
// 3つの約束:
//
//  1. **毎回の保存で版を作らない**（コアレッシング）。オートセーブは1〜2秒ごとに飛ぶので、
//     同じ編集者の連続保存は versionCoalesceWindow の間**最新の内容へ置き換え**て
//     ひとまとめにします（窓の起点は版ID＝作成時刻。窓を超えたら次の版が始まる）。
//     編集者が変わったときは必ず切ります——誰の書いたものかが混ざると後から辿れないからです。
//
//     かつては「窓の内側の保存は**捨てる**」方式で、1セッションが〈最初の保存＝
//     ほぼ編集前の内容〉と〈ロック解放時のチェックポイント＝最終状態〉の**ほぼ同じ2版**を
//     残していました（「版の履歴が細かすぎます」——2026-08-31 ユーザー指摘）。
//     置き換え方式なら窓の版が常に最新を持つため、チェックポイントは同内容で自然に
//     スキップされ、**1セッション＝1版**になります。
//  2. **5年は消さない**（日本の帳票保持義務。2026-08-21 ユーザー決定）。年限で切るので、
//     世代数の上限は設けません。
//  3. **各版は自己完結**（gzip フル圧縮・差分チェーンなし）。復元にチェーンが要らず、
//     1つ壊れても他へ波及しません。Go 標準に diff/patch が無い、という事情とも合います。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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
	// オートセーブ（1〜2秒）をそのまま版にすると履歴が使い物にならなくなるため。
	versionCoalesceWindow = 10 * time.Minute

	// versionRetention は版を残す年限です。**5年**（日本の帳票保持義務・2026-08-21 決定）。
	// うるう年で目減りしないよう 366 日で数えます——「5年は消さない」が要件なので、
	// 端数は必ず**長い側**へ倒します。
	versionRetention = 5 * 366 * 24 * time.Hour

	// versionTimeLayout は版のファイル名に使う時刻表記です。
	// RFC3339 は `:` を含みファイル名に使えないので、詰めた形にします。
	versionTimeLayout = "20060102T150405Z"
)

func init() {
	// 編集の区切り（ロックを手放したとき）に明示チェックポイントを打つ。
	// コアレッシングの窓（10分）の内側で編集を終えた人の最終形を残すための契機で、
	// 中身が最新の版と同じなら何も起きない（RecordVersion が重複を作らない）。
	editlock.OnRelease = func(pageIDInt int, username string) {
		id := fmt.Sprintf("%0*d", page.IDLength, pageIDInt)
		body, err := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
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
// ここの仕事です（添付の `safeAttachmentName` と同じ役目）。時刻由来の形しか
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
// 戻した記録が別の版であるべき）だけが使います。どちらの場合も、
// **中身が最新の版と同じなら何もしません**——ロック解放時のチェックポイントは
// 置き換え方式では常に同内容になるため、これで自然に消えます。
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

	sum := sha256.Sum256([]byte(html))
	hash := hex.EncodeToString(sum[:])

	if len(list) > 0 {
		newest := list[0]
		// 中身が同じなら、どの契機でも版は増やさない。
		if newest.Hash == hash {
			return nil
		}
		if !force && newest.By == author {
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
	if err := writeVersionFiles(dir, id, info, []byte(html)); err != nil {
		return err
	}
	// 年限を超えた版はここで落とす。ページ単位なので走査量が増え続けることはなく、
	// 背後で回す掃除役を持たずに済む。
	return PruneVersions(pageID)
}

// writeVersionFiles は版の2ファイル（本文の gzip と情報の JSON）を書きます。
// **本文を先に書き、情報を後に書く**——途中で落ちても「情報はあるが本文が無い」版を
// 作らないためです（ListVersions は情報ファイルを起点に数えます）。
func writeVersionFiles(dir, id string, info VersionInfo, body []byte) error {
	info.ID = id
	if info.Hash == "" {
		sum := sha256.Sum256(body)
		info.Hash = hex.EncodeToString(sum[:])
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
	// 版は本文HTMLなので、保存時の上限（8MiB）を超えることは無い。
	// 壊れた・細工された gzip で無制限に展開しないよう上限を掛ける。
	return io.ReadAll(io.LimitReader(zr, maxJSONBodyBytes))
}

// PruneVersions は年限（5年）を超えた版を消します。
func PruneVersions(pageID string) error {
	list, err := ListVersions(pageID)
	if err != nil {
		return err
	}
	pageID, _ = page.NormalizeID(pageID)
	dir := versionsDir(pageID)
	cutoff := time.Now().Add(-versionRetention)
	for _, v := range list {
		at, err := time.Parse(time.RFC3339, v.At)
		if err != nil {
			// 時刻が読めない版は消さない（消してよいと判断できないので残す側へ倒す）。
			continue
		}
		if at.Before(cutoff) {
			os.Remove(filepath.Join(dir, v.ID+".html.gz"))
			os.Remove(filepath.Join(dir, v.ID+".json"))
		}
	}
	return nil
}

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

	htmlPath := filepath.Join(page.GetPageDir(pageID), pageID+".html")
	if current, err := os.ReadFile(htmlPath); err == nil {
		// いまの内容を必ず残す（窓の内側でも force で切る）。
		if err := RecordVersion(pageID, author, string(current), true); err != nil {
			return err
		}
	}

	// 書き戻す本文もサニタイズを通す。過去の版は当時の許可リストで通ったもので、
	// 許可が狭まっていれば**いまの規則で**濾すのが正しい（保存経路と同じ扱い）。
	safeHTML := Sanitize(string(body))
	if err := page.WriteFileAtomic(htmlPath, []byte(safeHTML), 0644); err != nil {
		return err
	}
	// 更新日時は「今」前進する（サイドカーが正本。リバートも内容の変更なので）。
	if _, err := page.BumpUpdatedAt(pageID); err != nil {
		return err
	}
	if err := SyncIndex(pageID, safeHTML); err != nil {
		return err
	}
	// 戻した内容も版として残す（履歴が「いま何が正本か」で終わるように）。
	return RecordVersion(pageID, author, safeHTML, true)
}
