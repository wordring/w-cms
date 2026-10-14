package database

// データの場所（WCMS_DATA_DIR・2026-10-14・【要求】運用 §3）。

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDataDirDefaultsToData は、⚠ **渡さなければ `data`**（いままでと同じ）で、空の値も既定に戻ることを固定します。
func TestDataDirDefaultsToData(t *testing.T) {
	t.Cleanup(func() { SetDataDir("") })
	if got := DataDir(); got != "data" {
		t.Errorf("既定のデータの場所が %q です（data のはず）", got)
	}
	t.Setenv(DataDirEnv, "")
	if got := UseDataDirFromEnv(); got != "data" {
		t.Errorf("空の WCMS_DATA_DIR で %q になりました（data のはず）", got)
	}
	t.Setenv(DataDirEnv, "./data")
	if got := UseDataDirFromEnv(); got != "data" {
		t.Errorf("WCMS_DATA_DIR=./data で %q になりました（data のはず）", got)
	}
	if got := DataPath("mail", "a.json"); got != filepath.Join("data", "mail", "a.json") {
		t.Errorf("DataPath が %q です", got)
	}
}

// TestDataDirFromEnv は、WCMS_DATA_DIR を渡すとその場所になり、DataPath もその下を返すことを固定します。
func TestDataDirFromEnv(t *testing.T) {
	t.Cleanup(func() { SetDataDir("") })
	other := filepath.Join(t.TempDir(), "site", "data")
	t.Setenv(DataDirEnv, other)
	if got := UseDataDirFromEnv(); got != other {
		t.Errorf("WCMS_DATA_DIR=%s で %q になりました", other, got)
	}
	if got := DataPath("cms.db"); got != filepath.Join(other, "cms.db") {
		t.Errorf("DataPath(cms.db) が %q です", got)
	}
}

// TestDBsGoToDataDir は、3つのDB（cms.db・tables.db・auth.db）がデータの場所に作られ、
// 作業ディレクトリの data/ には何も作らないことを固定します。
func TestDBsGoToDataDir(t *testing.T) {
	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })
	other := filepath.Join(t.TempDir(), "site", "data")
	SetDataDir(other)
	t.Cleanup(func() { SetDataDir("") })

	if err := InitDB(); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { DB.Close(); DB = nil })
	if err := InitTablesDB(); err != nil {
		t.Fatalf("InitTablesDB: %v", err)
	}
	t.Cleanup(func() { TablesDB.Close(); TablesDB = nil })
	// 表の写しは表を同期するときに作られる（開いただけではファイルが無い）——1つ書いて作らせる。
	if _, err := TablesDB.Exec(`CREATE TABLE IF NOT EXISTS t (x)`); err != nil {
		t.Fatalf("表の写しに書けません: %v", err)
	}
	if err := InitAuthDB(); err != nil {
		t.Fatalf("InitAuthDB: %v", err)
	}
	t.Cleanup(func() { AuthDB.Close(); AuthDB = nil })

	for _, name := range []string{"cms.db", "tables.db", "auth.db"} {
		if _, err := os.Stat(filepath.Join(other, name)); err != nil {
			t.Errorf("%s がデータの場所にありません: %v", name, err)
		}
	}
	if _, err := os.Stat("data"); !os.IsNotExist(err) {
		t.Errorf("⚠ 作業ディレクトリの data/ が作られました（%v）", err)
	}
}
