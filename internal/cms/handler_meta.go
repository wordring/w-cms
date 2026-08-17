package cms

// ページの属性（サイドカー由来）・本文の語彙・メンテナンス操作のハンドラ。

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// TagSchemaAPIHandler は、登録済み全プラグインが宣言したカスタム要素の属性契約を返します。
//
// エディタはこれを使って本文をシリアライズします（要素ごとの手書き分岐を持たないため、
// プラグインを追加しただけで新しい要素が正しく保存されるようになる）。サーバー側の
// サニタイズ許可リストも同じ PluginTags() から作られるので、**保存する属性と許可する属性が
// 食い違いようがない**——過去に属性の宣言漏れで値が静かに消える不具合があったための設計。
//
// プラグイン側に個別の実装は不要で、Tags() を宣言すれば自動的にここへ現れます。
// 語彙は秘密情報ではないため認証不要（匿名の閲覧でもエディタの初期化は走る）。
func TagSchemaAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// {"elements": {"p": [...], "m-item": ["cost", "item-id", ...], ...}}
	//
	// 構造HTML（h1・ul・table 等）とカスタム要素の**両方**を返す。エディタはこれを
	// シリアライザの語彙として使うので、サニタイザが通す要素は必ず保存できる。
	// 片方だけ返すと「画面には出るが保存すると消える」要素が生まれる。
	elements := AllowedVocabulary()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"elements": elements,
		"void":     VoidElementNames(), // 終了タグを書かない要素（br・img 等）
		"block_id": BlockIDAttr,        // ブロック単位保存の識別子（data-id）
		// 語彙レジストリ（①）の形式定義。エディタはこれからスラッシュメニューの項目と
		// 挿入骨格（<table data-type> / <dl data-type>）を生成する（語彙モデル §7 の
		// 原則1: 新規挿入時の列定義はレジストリからAPI経由で受け取り、手書きの
		// 形式リストをエディタに置かない）。
		"vocab": VocabDefs(),
	})
}

// PageMetaAPIHandler はサイドパネル表示用に、ページの属性（親・作成/更新情報）を返します。
// 対象ページの read 権限を要求しますが、匿名でも実効公開（page.EffectivePublic）なら許可します
// （子ナビと同様の扱い。認証認可設計.md 10.5）。
func PageMetaAPIHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !page.RequirePageReadOrPublic(w, r, id) {
		return
	}
	idInt, err := strconv.Atoi(id)
	if err != nil {
		http.Error(w, "ページIDが不正です", http.StatusBadRequest)
		return
	}

	var parent sql.NullInt64
	var createdAt, createdBy, updatedAt sql.NullString
	err = database.DB.QueryRow(
		"SELECT parent_id, created_at, created_by, updated_at FROM pages WHERE id = ?", idInt,
	).Scan(&parent, &createdAt, &createdBy, &updatedAt)
	if err != nil {
		http.Error(w, "ページが見つかりません", http.StatusNotFound)
		return
	}

	parentStr := ""
	parentTitle := ""
	if parent.Valid {
		parentStr = fmt.Sprintf("%0*d", page.IDLength, parent.Int64)
		// 親ページの見出し（h1）。左レールの「↑ 親ページへ」リンクに表示する。
		database.DB.QueryRow("SELECT title FROM pages WHERE id = ?", parent.Int64).Scan(&parentTitle)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":           id,
		"parent_id":    parentStr,
		"parent_title": parentTitle,
		"created_at":   createdAt.String,
		"created_by":   createdBy.String,
		"updated_at":   updatedAt.String,
	})
}

// RebuildDBAPIHandler は、HTMLファイルからデータベースを完全に再構築します。
func RebuildDBAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// 全再構築は admin のみ
	if !page.RequireAdmin(w, r) {
		return
	}

	err := RebuildDatabase()
	if err != nil {
		http.Error(w, "Rebuild error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
