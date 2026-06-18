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
			id:   "00001",
			want: filepath.Join("data/master", "00", "00001"),
		},
		{
			name: "1桁の短いID",
			id:   "1",
			want: filepath.Join("data/master", "00", "1"),
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

	// 5. 10進数の桁上がりのテスト
	t.Run("10進数での桁上がり（繰り上げ）テスト", func(t *testing.T) {
		// "00009" を挿入。次は "00010" のはず
		_, err := db.Exec("INSERT INTO pages (id, type) VALUES ('00009', 'test')")
		if err != nil {
			t.Fatalf("テストデータ挿入エラー: %v", err)
		}

		got := GenerateNextID(db)
		want := "00010"
		if got != want {
			t.Errorf("GenerateNextID() = %q, want %q", got, want)
		}
	})

	// 6. 大きな値のテスト
	t.Run("より大きな数値IDの連番テスト", func(t *testing.T) {
		// "12345" を挿入。次は "12346" のはず
		_, err := db.Exec("INSERT INTO pages (id, type) VALUES ('12345', 'test')")
		if err != nil {
			t.Fatalf("テストデータ挿入エラー: %v", err)
		}

		got := GenerateNextID(db)
		want := "12346"
		if got != want {
			t.Errorf("GenerateNextID() = %q, want %q", got, want)
		}
	})
}
