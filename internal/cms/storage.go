package cms

import (
	"database/sql"
	"fmt"
	"path/filepath"
)

const IDLength = 6
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
// 次に保存すべき新しいID（6桁のゼロ埋め文字列）を生成します。
func GenerateNextID(db *sql.DB) string {
	var maxID sql.NullInt64
	// idはINTEGER型に変更されたため、単純なORDER BYで正しくソートされます
	err := db.QueryRow("SELECT id FROM pages ORDER BY id DESC LIMIT 1").Scan(&maxID)
	if err != nil || !maxID.Valid {
		// レコードがまだ登録されていない場合は初期値 "000000"
		return "000000"
	}

	next := maxID.Int64 + 1
	return fmt.Sprintf("%0*d", IDLength, next)
}
