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
