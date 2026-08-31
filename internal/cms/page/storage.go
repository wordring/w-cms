// Package page はページの**置き場所と属性**を担います。
//
// UNIX に倣って「ファイルの内容＝本文HTML」「ファイルの属性＝サイドカー」を分けます。
//
//   - 本文 …… data/master/<先頭2桁>/<id>/<id>.html
//   - 属性 …… 同じフォルダの <id>.meta.json（[PageMeta]。所有者・グループ・mode・
//     公開フラグ・親ページID・作成/更新情報）
//   - 版   …… 同じフォルダの versions/（cms パッケージが書く）
//   - 添付 …… 同じフォルダに直接（PDF・画像）
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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// AttachmentURLFor は**新しい**添付の配信アドレスを返します（files/ 配下）。
// 既存の本文には直下形（DataURLFor）のリンクが残っており、配信は両方を受けます。
func AttachmentURLFor(pageID, fileName string) string {
	if len(pageID) < 2 {
		return ""
	}
	return "/data/master/" + pageID[:2] + "/" + pageID + "/" + AttachmentsDirName + "/" + fileName
}
