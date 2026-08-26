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
