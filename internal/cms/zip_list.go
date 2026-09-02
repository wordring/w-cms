package cms

// ─────────────────────────────────────────────────────────────────────────
// ZIP添付の目録（2026-09-01 ユーザー要望「ZIPの中身が見れるようには
// なりませんか？」）
//
// 添付の .zip の**中身の一覧だけ**を返します。標準ライブラリ archive/zip の
// セントラルディレクトリ読みなので**展開はしない**——ZIP爆弾（小さな入力が
// 巨大に膨らむ細工）を踏まず、応答は名前とサイズの目録に限られます。
// 個々のファイルの取り出しは作っていません（展開は攻撃面が別枠で増えるため、
// 要望が出たときに設計する——docs/【考察】添付ファイルの表示と操作.md）。
//
// 和文ZIPの実情: Windows の右クリック圧縮はエントリ名を Shift_JIS で書き、
// UTF-8フラグを立てません（NonUTF8）。x/text で復号します（開発方針 §1 の承認済み依存）。
//
// 認可は添付の配信（ServeCleanAttachment）と同じ**閲覧側**の関門
// （RequirePageReadOrPublic）——読めるページの添付は目録も読める、それだけ。
// ─────────────────────────────────────────────────────────────────────────

import (
	"archive/zip"
	"encoding/json"
	"net/http"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"

	"w-cms/internal/cms/page"
)

// zipListMaxEntries は応答に載せる最大件数です（残りは truncated で知らせる）。
const zipListMaxEntries = 500

// zipEntry は目録の1行です。
type zipEntry struct {
	Name string `json:"name"`
	Size uint64 `json:"size"` // 展開後サイズ（ヘッダの申告値）
}

// ZipListAPIHandler は GET /api/zip-list?page_id=&file= です。
func ZipListAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pageID, ok := page.NormalizeID(r.URL.Query().Get("page_id"))
	if !ok {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}
	// 添付の配信と同じ閲覧側の関門（匿名は実効公開のときだけ）。
	if !page.RequirePageReadOrPublic(w, r, pageID) {
		return
	}
	// 名前の検査は全アップロード口と同じ関門を通す（本文・サイドカーの名指し拒否と
	// トラバーサルの正規化。拡張子は .zip だけ）。
	fileName, err := SafeAttachmentName(pageID, r.URL.Query().Get("file"),
		map[string]bool{".zip": true}, "ZIP以外の目録は返せません")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	zipPath, found := page.AttachmentPath(pageID, fileName)
	if !found {
		http.Error(w, "添付が見つかりません", http.StatusNotFound)
		return
	}

	entries, total, err := listZipEntries(zipPath)
	if err != nil {
		http.Error(w, "ZIPとして読めません: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success":   true,
		"entries":   entries,
		"total":     total,
		"truncated": total > len(entries),
	})
}

// listZipEntries は ZIP の目録（ファイルのみ・ディレクトリ行は畳む）を返します。
func listZipEntries(path string) ([]zipEntry, int, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, 0, err
	}
	defer zr.Close()

	entries := make([]zipEntry, 0, len(zr.File))
	total := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue // ファイルのパスに含まれるので行としては出さない
		}
		total++
		if len(entries) >= zipListMaxEntries {
			continue
		}
		entries = append(entries, zipEntry{Name: decodeZipName(f.Name, f.NonUTF8), Size: f.UncompressedSize64})
	}
	return entries, total, nil
}

// decodeZipName はエントリ名を UTF-8 へ直します。UTF-8フラグの無い名前は
// Shift_JIS とみなす（Windows の右クリック圧縮の実情）。復号できなければ原文のまま。
func decodeZipName(name string, nonUTF8 bool) string {
	if !nonUTF8 {
		return name
	}
	decoded, _, err := transform.String(japanese.ShiftJIS.NewDecoder(), name)
	if err != nil {
		return name
	}
	return decoded
}
