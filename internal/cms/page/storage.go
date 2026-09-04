// Package page はページの**置き場所と属性**を担います。
//
// UNIX に倣って「ファイルの内容＝本文HTML」「ファイルの属性＝サイドカー」を分けます。
//
//   - 本文 …… data/master/<先頭2桁>/<id>/<id>.html
//   - 属性 …… 同じフォルダの <id>.meta.json（[PageMeta]。所有者・グループ・mode・
//     公開フラグ・親ページID・作成/更新情報）
//   - 版   …… 同じフォルダの versions/（cms パッケージが書く）
//   - 添付 …… files/ サブフォルダ（PDF・画像・汎用添付。2026-08-31 以前の添付は直下に残る）
//
// **サイドカーが権限の正本**で、cms.db の page_perms はそこから再生成される派生です。
// 本文保存APIは権限に一切触れません——「本文を編集できる人が自分の権限を昇格させる」
// 経路を構造的に断つためで、サイドカーを書き換えるのは権限変更APIとページ新規作成だけです。
//
// 認可の関門（[RequirePageRead]・[RequirePageWrite]・[RequirePageReadOrPublic]・
// [RequireAdmin]）と、添付の配信（[DataFileHandler]）もここにあります。
// 判定そのものは mode の3桁（owner/group/other × read/write）と、匿名公開の
// パスゲート（[EffectivePublic]＝自分と全先祖が public）で決まります。
//
// ⚠ **読み口は正本ではなく派生**です——[GetPerms] も [EffectivePublic] も cms.db を
// 読みます。サイドカーと索引がずれると**認可は索引に従う**ので、parent_id の喪失は
// そのまま公開範囲の問題になります。
package page

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const IDLength = 6
const MasterDir = "data/master"

// NormalizeID は受け取った page_id 文字列をゼロ詰め6桁の正規形へ揃えます。
// 数値として解釈できない・負数の場合は ok=false。
//
// 権限判定は strconv.Atoi を通すため "0012" や "+12" でも 12 として通るが、
// ファイルパスに受け取った文字列をそのまま使うと data/master/00/0012/ のような
// **別ディレクトリへ正本が書かれて名前が揺れる**（2026-08-05 監査の指摘）。
// パスやサイドカーに id を使うハンドラは、入口でこの関数を通すこと。
func NormalizeID(id string) (string, bool) {
	n, err := strconv.Atoi(id)
	if err != nil || n < 0 {
		return "", false
	}
	return fmt.Sprintf("%0*d", IDLength, n), true
}

// GetPageDir は ID (例: "00A1B") を受け取り、階層化された保存先パス (例: "data/master/00/00A1B") を返します。
// 1つのフォルダに数万ファイルが集中してOSが重くなるのを防ぐための関数です。
func GetPageDir(id string) string {
	// IDの先頭2文字を親フォルダ（プレフィックス）として使用する
	if len(id) < 2 {
		return filepath.Join(MasterDir, "00", id)
	}
	prefix := id[:2]
	return filepath.Join(MasterDir, prefix, id)
}

// TrashDir は削除したページの退避先です。
//
// 削除は**物理削除ではなくゴミ箱への移動**にします（2026-08-20 決定）。
// 自動判定で作ったページを取り消せることが要件なら、その取り消し自体も
// 取り返しがつく必要があるからです（docs/【考察】通信記録処理.md §2.7④「常に柔軟性」）。
// DB再構築（RebuildDatabase）は data/master だけを走査するので、移すだけで索引からも消えます。
const TrashDir = "data/trash"

// GetTrashDir は削除したページの移動先パスを返します（GetPageDir と同じ階層化）。
func GetTrashDir(id string) string {
	if len(id) < 2 {
		return filepath.Join(TrashDir, "00", id)
	}
	return filepath.Join(TrashDir, id[:2], id)
}

// WriteFileAtomic は「同じフォルダの一時ファイルへ書いて rename」でファイルを
// 置き換えます。書き込み中にプロセスが落ちても、path には**元の内容がそのまま残る**
// （半分だけ書けた切り詰めファイルにならない）ことが保証です。
//
// 正本（本文HTML・サイドカー・添付・版・設定）を書く経路は必ずこれを使うこと。
// 「サイドカーが読めないときは自動で治さない」（2026-08-22 決定）は「読めない＝
// 運用者が見るべき異常」という前提であり、書き込み自身が切り詰め破損を作れる
// os.WriteFile（O_TRUNC）のままでは、その前提を自分で壊してしまいます。
//
// rename は同一ボリューム内で原子的です（Windows でも MoveFileEx の置き換え）。
// 一時ファイルを同じフォルダに作るのは、フォルダをまたぐ rename が原子性を
// 失うため。エラー時は一時ファイルを消して path に触れません。
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功後は何もしない（もう存在しない）

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp は 0600 で作るため、意図した権限へ揃えてから公開する。
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// AttachmentsDirName は添付ファイルの置き場（ページフォルダ内のサブフォルダ）です。
//
// **正本（<id>.html・<id>.meta.json・versions/）と添付を同じフォルダに置かない**
// のが構造の要です（2026-08-31 ユーザー指摘「.json禁止は、アップロードファイルと
// jsonが同じフォルダにあることが原因では？」——そのとおりで、同居をやめれば
// 「添付の名前がサイドカーを上書きする」穴は**名前の検査に頼らず**塞がる）。
// 拡張子の許可リストは以後、安全の門ではなく運用の方針になる。
// 既存の添付（ページフォルダ直下）は動かさない——配信は両方の場所を受ける。
const AttachmentsDirName = "files"

// AttachmentDir は添付の保存先ディレクトリを返します。
func AttachmentDir(id string) string {
	return filepath.Join(GetPageDir(id), AttachmentsDirName)
}

// AttachmentPath は添付の実ファイルの場所を返します。新しい置き場（files/）を先に、
// 無ければ旧（ページフォルダ直下）を見ます。どちらにも無ければ ok=false。
// 読む側（PDF解析・ZIP目録）が共用し、「files/ → 直下」の順序をここ1箇所で持ちます。
func AttachmentPath(id, fileName string) (path string, ok bool) {
	for _, dir := range []string{AttachmentDir(id), GetPageDir(id)} {
		p := filepath.Join(dir, fileName)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// AttachmentURLFor は添付の配信アドレスを返します。
//
// 形は **`/<ページID>/<ファイル名>`**（2026-08-31 ユーザー決定「実際に保存される場所を
// 推測されたくないですし、見た目もすっきりさせたい」）。物理配置（data/master/…/files/）は
// URLに現れない——配信は RootHandler がページURLの下の名前空間として受け、
// 認可（read／実効公開）を通して files/ から返す。
// 旧形（/data/master/…）のリンクは互換として DataFileHandler が配信し続ける。
func AttachmentURLFor(pageID, fileName string) string {
	return "/" + pageID + "/" + fileName
}

// GeneratedAttachmentID は添付の識別子を生成します。
//
// **この識別子は3役を兼ねます**: 保存名（<id>.<拡張子>）・配信URLの名前
// （/<ページID>/<id>.<拡張子>）・本文のリンクブロックの data-id。
// 「ファイル名も元のファイル名ではなく、生成したものを使いたい」
// 「ファイル名とdata-id、IDを一致させるアイデアはどうですか？」（2026-08-31 ユーザー）。
//
// **形はブロックIDと同じ4桁の base36 です**（2026-09-04 変更。それまでは時刻ミリ秒の
// base36 で8桁だった）。参照 `ページID-ID` が飛ぶ先は**常に本文のブロック**なので
// （`#<data-id>` のアンカー。添付そのものを指す経路はどこにも無い）、
// **添付だけ別の採番規則にしておく理由がありませんでした**——ユーザー:
// 「添付IDに対してジャンプする先はブロックですから、添付IDでは無く、最初から
// ブロックへジャンプしては？」「ファイル名とブロックのidを一緒にしては？」。
// これで受信元タグも社内コードも同じ見た目（`010678-a7k2`）になります。
//
// **一意でなければならない範囲が2つある**ので、両方を避けます:
//
//   - `files/` の中（**拡張子を問わず**。`a7k2.pdf` と `a7k2.dxf` が同居すると
//     本文に同じ data-id が2つ生まれる）
//   - **本文の data-id**（ブロックの採番と同じ名前空間に入ったため）
//
// 尽きたときは時刻ミリ秒の長い形へ逃がします——**衝突させるより長いほうがまし**。
// なお**既存の8桁の添付はそのまま**です。ファイル名はURLでもあるので、
// 改名すると本文のリンクが全部切れます。
func GeneratedAttachmentID(pageID, ext string) string {
	taken := takenAttachmentIDs(pageID)
	const chars = "0123456789abcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, 4)
	for attempt := 0; attempt < 50; attempt++ {
		if _, err := rand.Read(buf); err != nil {
			break
		}
		id := make([]byte, 4)
		for i, b := range buf {
			id[i] = chars[int(b)%len(chars)]
		}
		if !taken[string(id)] {
			return string(id)
		}
	}
	return strconv.FormatInt(time.Now().UnixMilli(), 36)
}

// takenAttachmentIDs は、そのページで既に使われている識別子を集めます
// （files/ の名前＝拡張子を落としたもの と、本文の data-id）。
func takenAttachmentIDs(pageID string) map[string]bool {
	taken := map[string]bool{}
	if entries, err := os.ReadDir(AttachmentDir(pageID)); err == nil {
		for _, e := range entries {
			name := e.Name()
			taken[strings.TrimSuffix(name, filepath.Ext(name))] = true
		}
	}
	// 本文は読めなくても止めません——添付の採番は本文保存とは別の操作なので、
	// ここで失敗させると「本文が壊れていると添付もできない」になります。
	body, err := os.ReadFile(filepath.Join(GetPageDir(pageID), pageID+".html"))
	if err != nil {
		return taken
	}
	for _, m := range blockIDAttrRe.FindAllSubmatch(body, -1) {
		taken[string(m[1])] = true
	}
	return taken
}

// blockIDAttrRe は本文の data-id を拾います（cms.NewBlockID と同じ形。
// **同じ名前空間を2箇所で採番する**ので、拾い方も揃えておくこと）。
var blockIDAttrRe = regexp.MustCompile(`data-id="([0-9a-z]+)"`)
