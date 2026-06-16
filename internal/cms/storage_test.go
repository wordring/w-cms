package cms

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestGetPageDir(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{
			name: "5桁のID",
			id:   "00A1B",
			want: filepath.Join("data/master", "00", "00A1B"),
		},
		{
			name: "1桁の短いID",
			id:   "A",
			want: filepath.Join("data/master", "00", "A"),
		},
		{
			name: "2桁のID",
			id:   "01",
			want: filepath.Join("data/master", "01", "01"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetPageDir(tt.id)
			if got != tt.want {
				t.Errorf("GetPageDir(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestGenerateNextID(t *testing.T) {
	// 1. インメモリのSQLiteデータベースを初期化
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("データベース接続エラー: %v", err)
	}
	defer db.Close()

	// 2. テスト用テーブルの作成
	query := `
	CREATE TABLE pages (
		id TEXT PRIMARY KEY,
		type TEXT,
		path TEXT,
		title TEXT,
		summary TEXT
	);`
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("テーブル作成エラー: %v", err)
	}

	// 3. データが何もない場合のテスト（初期値 "00000" のはず）
	t.Run("初期状態でのID生成", func(t *testing.T) {
		got := GenerateNextID(db)
		want := "00000"
		if got != want {
			t.Errorf("GenerateNextID() = %q, want %q", got, want)
		}
	})

	// 4. "00000" を追加した後のテスト（"00001" のはず）
	t.Run("00000存在時の次のID生成", func(t *testing.T) {
		_, err := db.Exec("INSERT INTO pages (id, type) VALUES ('00000', 'test')")
		if err != nil {
			t.Fatalf("テストデータ挿入エラー: %v", err)
		}

		got := GenerateNextID(db)
		want := "00001"
		if got != want {
			t.Errorf("GenerateNextID() = %q, want %q", got, want)
		}
	})

	// 5. Base-36 の文字を含むIDを追加した後のテスト
	t.Run("Base-36文字を含むID存在時の次のID生成", func(t *testing.T) {
		// "00A1B" を挿入 (10進数で 13007)
		// 次は "00A1C" (10進数で 13008) のはず
		_, err := db.Exec("INSERT INTO pages (id, type) VALUES ('00A1B', 'test')")
		if err != nil {
			t.Fatalf("テストデータ挿入エラー: %v", err)
		}

		got := GenerateNextID(db)
		want := "00A1C"
		if got != want {
			t.Errorf("GenerateNextID() = %q, want %q", got, want)
		}
	})

	// 6. Base-36 の桁上がり（繰り上げ）テスト
	t.Run("Base-36の繰り上げが発生する次のID生成", func(t *testing.T) {
		// "00A1Z" を挿入 (10進数で 13031)
		// 次は "00A20" (10進数で 13032) のはず
		_, err := db.Exec("INSERT INTO pages (id, type) VALUES ('00A1Z', 'test')")
		if err != nil {
			t.Fatalf("テストデータ挿入エラー: %v", err)
		}

		got := GenerateNextID(db)
		want := "00A20"
		if got != want {
			t.Errorf("GenerateNextID() = %q, want %q", got, want)
		}
	})
}
