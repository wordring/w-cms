package cms

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
)

const IDLength = 5
const MasterDir = "data/master"

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

// GenerateNextID はデータベースから現在登録されている最大のIDを取得し、
// 次に保存すべき新しいID（5桁の10進数連番）を生成します。
// 主キーのインデックスを活用してミリ秒以下で最大IDを取得します。
func GenerateNextID(db *sql.DB) string {
	var maxID string
	err := db.QueryRow("SELECT id FROM pages ORDER BY id DESC LIMIT 1").Scan(&maxID)
	if err != nil {
		// レコードがまだ登録されていない場合は初期値 "00000"
		return "00000"
	}

	// 取得した10進数文字列を数値にデコード
	maxVal, err := strconv.ParseInt(maxID, 10, 64)
	if err != nil {
		return "00000"
	}

	// 最大値に+1し、10進数文字列に戻して指定桁数(5桁)で0埋めする
	next := maxVal + 1
	return fmt.Sprintf("%0*s", IDLength, strconv.FormatInt(next, 10))
}
