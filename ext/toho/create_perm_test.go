package toho

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 書類を作る先へ書けない人は、書類を作れない（2026-10-21。利用者:「直します」——【考察】文書の食い違い §7.2）。
// それまでは GateJSONPost（ログインしているか）しか見ず、読むだけの人でも置き場や製造ページの下に書類を作れた。

func childCount(t *testing.T, parentID string) int {
	t.Helper()
	kids, err := cms.ChildPages(database.DB, pageNum(parentID))
	if err != nil {
		t.Fatal(err)
	}
	return len(kids)
}

// TestNewBuiltPageNeedsWriteOnParent は、親へ書けなければ何も作らずに errNoWrite（403）を返すことを固定します。
func TestNewBuiltPageNeedsWriteOnParent(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "root", Mode: "302"}) // 持ち主のほかは読むだけ
	reader := &auth.User{Username: "bob"}
	built := false
	_, err := newBuiltPage("000500", reader, "納品書", func(string) (string, error) {
		built = true
		return "<h1>x</h1>", nil
	})
	var nw *errNoWrite
	if !errors.As(err, &nw) {
		t.Fatalf("書けない親の下に作れてしまいます: err = %v", err)
	}
	if got := createFailStatus(err, http.StatusInternalServerError); got != http.StatusForbidden {
		t.Errorf("状態 = %d（403 のはず）", got)
	}
	if built || childCount(t, "000500") != 0 {
		t.Errorf("断ったのにページを作り始めました（built=%v・子 %d）", built, childCount(t, "000500"))
	}

	// 書ける人（持ち主）は作れる。
	owner := &auth.User{Username: "root"}
	id, err := newBuiltPage("000500", owner, "納品書", func(string) (string, error) { return "<h1>納品書</h1>", nil })
	if err != nil || id == "" || childCount(t, "000500") != 1 {
		t.Fatalf("持ち主が作れません: id=%q err=%v", id, err)
	}
}

// TestRecordFolderOrNeedsWrite は、親（製造ページ など）か「記録」へ書けなければ、フォルダを作る前に断ることを固定します。
func TestRecordFolderOrNeedsWrite(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "root", Mode: "302"})
	when := time.Date(2026, 10, 21, 9, 0, 0, 0, time.Local)
	reader := &auth.User{Username: "bob"}
	var nw *errNoWrite

	// 親があるとき: 親へ書けなければ断る。
	if _, err := recordFolderOr("000500", reader, RecordWorkFolder, when); !errors.As(err, &nw) {
		t.Fatalf("書けない親を返しました: %v", err)
	}

	// 親が無いとき: 「記録」へ書けなければ、年・月のフォルダを作らずに断る。
	admin := &auth.User{Username: "root", IsAdmin: true}
	if _, err := recordFolderOr("", admin, RecordWorkFolder, when); err != nil {
		t.Fatal(err)
	}
	box, err := cms.EnsureTopLevelBox(RecordBoxTitle, "root")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB.Exec(`UPDATE page_perms SET owner = 'root', mode = '302' WHERE page_id = ?`, pageNum(box)); err != nil {
		t.Fatal(err)
	}
	before := childCount(t, box)
	later := when.AddDate(0, 2, 0) // まだ無い月——作らせたら子が増える
	_, err = recordFolderOr("", reader, RecordWorkFolder, later)
	if !errors.As(err, &nw) {
		t.Fatalf("書けない「記録」の下に置き場を用意しました: %v", err)
	}
	if got := createFailStatus(err, http.StatusInternalServerError); got != http.StatusForbidden {
		t.Errorf("状態 = %d（403 のはず）", got)
	}
	if after := childCount(t, box); after != before {
		t.Errorf("断ったのに「記録」の下にフォルダが増えました（%d → %d）", before, after)
	}
}
