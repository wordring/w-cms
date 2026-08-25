package cms

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// 保存済み文書の版管理（リビジョン／リバート）のテスト。
// 設計の正本は [【考察】アンドゥ・リドゥ.md] §4・§5。
//
// 押さえたいのは4点:
//   - オートセーブは1〜2秒ごとに飛ぶので**毎回版を作らない**（コアレッシング）
//   - 版は**本文HTMLだけ**（サイドカー・権限・親・添付は対象外＝リバートが権限昇格の
//     抜け道にならない）
//   - **5年は消さない**（帳票の保持義務。2026-08-21 決定）
//   - リバートは「古いHTMLをもう一度保存する」だけ（`Sync` が派生を作り直す）

// versionsOf は版の一覧を新しい順に返します。
func versionsOf(t *testing.T, id string) []VersionInfo {
	t.Helper()
	list, err := ListVersions(id)
	if err != nil {
		t.Fatalf("ListVersionsエラー: %v", err)
	}
	return list
}

// TestVersionRecordedOnSave は、保存が版を残すことを検証します。
func TestVersionRecordedOnSave(t *testing.T) {
	setupSaveTest(t)
	postSave(t, "000040", "<h1>初版</h1>")

	list := versionsOf(t, "000040")
	if len(list) != 1 {
		t.Fatalf("版が1つではありません: %d", len(list))
	}
	if list[0].By != "tester" {
		t.Errorf("編集者が記録されていません: %q", list[0].By)
	}
	body, err := ReadVersion("000040", list[0].ID)
	if err != nil {
		t.Fatalf("ReadVersionエラー: %v", err)
	}
	if !strings.Contains(string(body), "初版") {
		t.Errorf("版の中身が違います: %q", body)
	}
}

// TestVersionsAreCoalesced は、連続保存が1つの版へまとめられることを検証します。
// これが効かないと、オートセーブ（1〜2秒ごと）で版が無限に増えます。
func TestVersionsAreCoalesced(t *testing.T) {
	setupSaveTest(t)
	for i := 0; i < 5; i++ {
		postSave(t, "000040", "<h1>連打</h1><p>"+strings.Repeat("あ", i+1)+"</p>")
	}
	if list := versionsOf(t, "000040"); len(list) != 1 {
		t.Errorf("連続保存が %d 版になりました（1版にまとまるはず）", len(list))
	}
}

// TestVersionRecordedWhenAuthorChanges は、編集者が変わったら必ず版を切ることを
// 検証します。誰の書いたものかが混ざると、後から編集者を辿れなくなります。
func TestVersionRecordedWhenAuthorChanges(t *testing.T) {
	setupSaveTest(t)
	postSaveAs(t, "000040", "<h1>アリス</h1>", "alice")
	postSaveAs(t, "000040", "<h1>ボブ</h1>", "bob")

	list := versionsOf(t, "000040")
	if len(list) != 2 {
		t.Fatalf("版が2つではありません: %d", len(list))
	}
	if list[0].By != "bob" || list[1].By != "alice" {
		t.Errorf("編集者の並びが違います: %q / %q", list[0].By, list[1].By)
	}
}

// TestVersionSkipsUnchangedContent は、中身が変わっていない保存で版を増やさない
// ことを検証します（明示チェックポイントが重なっても無駄な版を作らない）。
func TestVersionSkipsUnchangedContent(t *testing.T) {
	setupSaveTest(t)
	const id = "000040"
	const html = "<h1>同じ</h1>"
	postSave(t, id, html)
	before := len(versionsOf(t, id))

	// ロック解放時の明示チェックポイント（中身は変わっていない）。
	if err := RecordVersion(id, "tester", html, true); err != nil {
		t.Fatalf("RecordVersionエラー: %v", err)
	}
	if after := len(versionsOf(t, id)); after != before {
		t.Errorf("中身が同じなのに版が増えました: %d → %d", before, after)
	}
}

// TestVersionForcedCheckpointRecordsChange は、明示チェックポイント（ロック解放時）が
// コアレッシングの窓の中でも版を切ることを検証します。
// これが無いと、10分の窓の内側で編集を終えた人の最後の状態が残りません。
func TestVersionForcedCheckpointRecordsChange(t *testing.T) {
	setupSaveTest(t)
	const id = "000040"
	postSave(t, id, "<h1>途中</h1>")
	if err := RecordVersion(id, "tester", "<h1>最後</h1>", true); err != nil {
		t.Fatalf("RecordVersionエラー: %v", err)
	}
	list := versionsOf(t, id)
	if len(list) != 2 {
		t.Fatalf("明示チェックポイントで版が増えていません: %d", len(list))
	}
	body, _ := ReadVersion(id, list[0].ID)
	if !strings.Contains(string(body), "最後") {
		t.Errorf("最新の版が最後の状態ではありません: %q", body)
	}
}

// TestRevertRestoresBodyOnly は、リバートが**本文だけ**を戻すことを検証します。
// 権限・所有者・親はサイドカーが持つので巻き戻らない——これが「古い版に戻す」が
// 権限昇格やページ移動の抜け道にならない理由です（設計 §5）。
func TestRevertRestoresBodyOnly(t *testing.T) {
	setupSaveTest(t)
	const id = "000040"
	postSave(t, id, "<h1>初版</h1><p>もとの本文</p>")
	old := versionsOf(t, id)[0].ID

	// 権限と所有者を変えてから、本文も書き換える。
	meta, _ := page.ReadSidecar(id)
	meta.Owner = "bob"
	meta.Mode = "700"
	if err := page.WriteSidecar(id, meta); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}
	postSave(t, id, "<h1>第2版</h1><p>書き換えた</p>")

	if err := RevertToVersion(id, old, "tester"); err != nil {
		t.Fatalf("RevertToVersionエラー: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
	if err != nil {
		t.Fatalf("正本を読めません: %v", err)
	}
	if !strings.Contains(string(body), "もとの本文") {
		t.Errorf("本文が戻っていません: %q", body)
	}
	after, ok := page.ReadSidecar(id)
	if !ok {
		t.Fatal("サイドカーを読めません")
	}
	if after.Owner != "bob" || after.Mode != "700" {
		t.Errorf("リバートで権限・所有者が巻き戻りました: owner=%q mode=%q", after.Owner, after.Mode)
	}
}

// TestRevertKeepsCurrentAsVersion は、リバートの前に「いまの内容」が版として
// 残ることを検証します（リバートそのものを取り消せる）。
func TestRevertKeepsCurrentAsVersion(t *testing.T) {
	setupSaveTest(t)
	const id = "000040"
	postSave(t, id, "<h1>初版</h1>")
	old := versionsOf(t, id)[0].ID
	postSaveAs(t, id, "<h1>第2版</h1>", "bob")

	if err := RevertToVersion(id, old, "carol"); err != nil {
		t.Fatalf("RevertToVersionエラー: %v", err)
	}
	found := false
	for _, v := range versionsOf(t, id) {
		b, _ := ReadVersion(id, v.ID)
		if strings.Contains(string(b), "第2版") {
			found = true
		}
	}
	if !found {
		t.Error("リバート前の内容が版として残っていません（リバートを取り消せない）")
	}
}

// TestVersionPruningKeepsFiveYears は、保持年限の規則を検証します。
// **5年は消さない**（日本の帳票保持義務。2026-08-21 決定）。
func TestVersionPruningKeepsFiveYears(t *testing.T) {
	setupSaveTest(t)
	const id = "000040"
	postSave(t, id, "<h1>いま</h1>")

	dir := versionsDir(id)
	// 4年前（残す）と 6年前（消す）の版を手で置く。
	writeFakeVersion(t, dir, time.Now().AddDate(-4, 0, 0), "4年前")
	writeFakeVersion(t, dir, time.Now().AddDate(-6, 0, 0), "6年前")

	if err := PruneVersions(id); err != nil {
		t.Fatalf("PruneVersionsエラー: %v", err)
	}

	var got []string
	for _, v := range versionsOf(t, id) {
		b, _ := ReadVersion(id, v.ID)
		got = append(got, string(b))
	}
	joined := strings.Join(got, "|")
	if !strings.Contains(joined, "4年前") {
		t.Errorf("5年以内の版が消えました: %v", got)
	}
	if strings.Contains(joined, "6年前") {
		t.Errorf("5年を超えた版が残っています: %v", got)
	}
}

// writeFakeVersion は指定時刻の版を直接置きます（年限のテスト用）。
func writeFakeVersion(t *testing.T, dir string, at time.Time, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("versions作成エラー: %v", err)
	}
	if err := writeVersionFiles(dir, versionID(at), VersionInfo{
		At: at.UTC().Format(time.RFC3339), By: "tester",
	}, []byte(body)); err != nil {
		t.Fatalf("版の書き込みエラー: %v", err)
	}
}

// TestVersionsAPIRequiresRead は、版の一覧・取得が read 権限を要求することを
// 検証します（版は本文そのものなので、本文と同じ守りが要る）。
func TestVersionsAPIRequiresRead(t *testing.T) {
	setupSaveTest(t)
	const id = "000041"
	if err := page.WriteSidecar(id, page.PageMeta{Owner: "alice", Mode: "300"}); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}
	if err := SyncIndex(id, "<h1>社外秘</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	if err := RecordVersion(id, "alice", "<h1>社外秘</h1>", true); err != nil {
		t.Fatalf("RecordVersionエラー: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/versions?id="+id, nil)
	req = auth.WithUser(req, &auth.User{Username: "bob", Groups: []string{"other"}})
	rr := httptest.NewRecorder()
	VersionsAPIHandler(rr, req)
	if rr.Code != 403 {
		t.Errorf("read権限の無い相手に版一覧が見えています: code=%d body=%s", rr.Code, rr.Body.String())
	}

	// 所有者には見える。
	req = httptest.NewRequest("GET", "/api/versions?id="+id, nil)
	req = auth.WithUser(req, &auth.User{Username: "alice"})
	rr = httptest.NewRecorder()
	VersionsAPIHandler(rr, req)
	if rr.Code != 200 {
		t.Fatalf("所有者に版一覧が返りません: code=%d body=%s", rr.Code, rr.Body.String())
	}
	var list []VersionInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("JSONを解釈できません: %v (%s)", err, rr.Body.String())
	}
	if len(list) != 1 {
		t.Errorf("版の数が違います: %d", len(list))
	}
}

// TestRevertAPIRequiresWriteAndLock は、リバートが write 権限と編集ロックを
// 要求することを検証します（本文を書き換える操作なので保存と同じ守り）。
func TestRevertAPIRequiresWriteAndLock(t *testing.T) {
	setupSaveTest(t)
	const id = "000042"
	editlock.Locks.ForceRelease(42)
	t.Cleanup(func() { editlock.Locks.ForceRelease(42) })

	if err := page.WriteSidecar(id, page.PageMeta{Owner: "alice", Group: "team", Mode: "330"}); err != nil {
		t.Fatalf("WriteSidecarエラー: %v", err)
	}
	if err := SyncIndex(id, "<h1>初版</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
	if err := RecordVersion(id, "alice", "<h1>初版</h1>", true); err != nil {
		t.Fatalf("RecordVersionエラー: %v", err)
	}
	v := versionsOf(t, id)[0].ID

	// read しか持たない相手は 403。
	rr := postRevert(t, id, v, &auth.User{Username: "carol", Groups: []string{"other"}}, "")
	if rr.Code != 403 {
		t.Errorf("write権限の無い相手のリバートが通りました: code=%d", rr.Code)
	}

	// 他人がロックを保持している間は 409。
	if a := editlock.Locks.TryAcquire(42, "alice", ""); !a.Acquired {
		t.Fatal("alice のロック取得に失敗")
	}
	rr = postRevert(t, id, v, &auth.User{Username: "bob", Groups: []string{"team"}}, "")
	if rr.Code != 409 {
		t.Errorf("他者ロック中のリバートが 409 になりません: code=%d", rr.Code)
	}
}

// postRevert は /api/revert を叩きます。
func postRevert(t *testing.T, id, version string, u *auth.User, token string) *httptest.ResponseRecorder {
	t.Helper()
	payload := `{"page_id":"` + id + `","version":"` + version + `"}`
	req := httptest.NewRequest("POST", "/api/revert", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Lock-Token", token)
	}
	req = auth.WithUser(req, u)
	rr := httptest.NewRecorder()
	RevertAPIHandler(rr, req)
	return rr
}

// TestVersionIDRejectsPathEscape は、版IDでページのフォルダの外を読めない
// ことを検証します（版IDは利用者の入力としてAPIから来る）。
func TestVersionIDRejectsPathEscape(t *testing.T) {
	setupSaveTest(t)
	const id = "000040"
	postSave(t, id, "<h1>初版</h1>")

	for _, bad := range []string{"../../000040", "..", "a/b", `a\b`, ""} {
		if _, err := ReadVersion(id, bad); err == nil {
			t.Errorf("不正な版ID %q が通りました", bad)
		}
	}
}
