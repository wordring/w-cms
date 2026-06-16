package cms

import "w-cms/internal/database"

// SyncIndex はHTMLファイルを解析し、その結果をSQLiteのインデックス用テーブルに保存します。
func SyncIndex(id string, htmlContent string) error {
	// 手順1: HTML文字列をパースして、typeやpathなどのメタデータを抽出する
	meta := ParseHTMLMaster(id, htmlContent)

	// 手順2: SQLiteの pages テーブルにデータを登録する
	// ★追加: pathカラムも一緒に保存（INSERT）し、更新時（UPDATE）にもpathを上書きするように追加しました。
	_, err := database.DB.Exec(`
		INSERT INTO pages (id, type, path, title, summary) 
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			type = excluded.type,
			path = excluded.path,
			title = excluded.title,
			summary = excluded.summary,
			updated_at = CURRENT_TIMESTAMP
	`, meta.ID, meta.Type, meta.Path, meta.Title, meta.Summary)
	return err
}
