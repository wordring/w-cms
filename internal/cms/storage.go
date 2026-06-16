package cms

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// IDLength は生成する Base-36 IDの桁数（5桁 = 約6000万件対応）
const IDLength = 5

// MasterDir は製品データを保存するルートディレクトリ
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
// 次に保存すべき新しいID（5桁のBase-36）を生成します。
// 主キーのインデックスを活用してミリ秒以下で最大IDを取得します。
func GenerateNextID(db *sql.DB) string {
	var maxID string
	err := db.QueryRow("SELECT id FROM pages ORDER BY id DESC LIMIT 1").Scan(&maxID)
	if err != nil {
		// レコードがまだ登録されていない場合は初期値 "00000"
		return "00000"
	}

	// 取得したBase-36文字列を数値にデコード
	maxVal, err := strconv.ParseInt(maxID, 36, 64)
	if err != nil {
		return "00000"
	}

	// 最大値に+1し、Base-36文字列に戻して指定桁数(5桁)で0埋めする
	next := maxVal + 1
	return fmt.Sprintf("%0*s", IDLength, strings.ToUpper(strconv.FormatInt(next, 36)))
}
