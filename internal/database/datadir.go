package database

// ─────────────────────────────────────────────────────────────────────────
// データの場所（`WCMS_DATA_DIR`・2026-10-14）
//
// 【要求】運用 §3 の表: `WCMS_DATA_DIR` は `data/` の場所で、既定は `./data` です。
// 1台に2つの w-cms を同居させるとき（§4 の2インスタンス運用）に、データ置き場を分けます。
//
// **`data/` の下を使う所は、ここから引きます**——本文（master）・ゴミ箱（trash）・
// 3つのDB（cms.db・tables.db・auth.db）・証明書（tls）・メールの保管（mail）・
// 初期化の控え（_reset-<日時>）。相対パスの `"data"` を直に書かないこと
// （書くと、場所を変えたインスタンスがそこだけ別の場所へ書きます）。
//
// ⚠ **環境変数を読むのは起動のとき1回だけ**（[UseDataDirFromEnv]・main と道具が呼ぶ）。
// 呼ばなければ既定の `data` のままです——試験は一時ディレクトリへ移ってから相対の
// `data` を使うので、開発者の環境に `WCMS_DATA_DIR` が残っていても、本物のデータへ
// 書きません。
//
// ⚠ 添付のURL `/data/master/...` は**URLの名前**で、置き場所ではありません。
// 本文に書き込まれて残るので、データの場所を変えても変えません（配信する口
// `page.DataFileHandler` が、ここから引いた場所へ読み替えます）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// DataDirEnv はデータの場所を渡す環境変数の名前です。
const DataDirEnv = "WCMS_DATA_DIR"

// DefaultDataDir は既定のデータの場所です（作業ディレクトリの `data`）。
const DefaultDataDir = "data"

// dataDir はいまのデータの場所です（起動の前に1回決め、あとは読むだけ）。
var dataDir atomic.Pointer[string]

// DataDir はいまのデータの場所を返します（既定は `data`）。
func DataDir() string {
	if p := dataDir.Load(); p != nil {
		return *p
	}
	return DefaultDataDir
}

// DataPath はデータの場所の下のパスを返します（例: DataPath("master") → `data/master`）。
func DataPath(elem ...string) string {
	return filepath.Join(append([]string{DataDir()}, elem...)...)
}

// SetDataDir はデータの場所を決めます。空なら既定の `data` に戻します。
// 起動のとき（[UseDataDirFromEnv]）と、場所を変えて確かめる試験だけが呼びます。
func SetDataDir(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = DefaultDataDir
	}
	dir = filepath.Clean(dir)
	dataDir.Store(&dir)
}

// UseDataDirFromEnv は環境変数 `WCMS_DATA_DIR` からデータの場所を決め、それを返します
// （無ければ・空なら既定の `data`）。main と、`data/` を読む道具が最初に1回呼びます。
func UseDataDirFromEnv() string {
	SetDataDir(os.Getenv(DataDirEnv))
	return DataDir()
}
