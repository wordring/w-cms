package cms

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// パンくず（本文の上の帯）のテスト。固定するのは3つ:
//   - 先祖の道が**根に近い順**で出る（現在のページは入れない）
//   - **読めない先祖は題を伏せる**（リンクにもしない）——認可の話なので最重要
//   - 先祖の無いページ（トップ）では空を返し、帯ごと畳まれる

// TestBreadcrumbShowsAncestorPath は、道が根に近い順で出ることを検証します。
func TestBreadcrumbShowsAncestorPath(t *testing.T) {
	setupTemplateAPITest(t)
	// トップ(000000) → 業務(000030) → 受注(000031) → 現在(000032)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	newPage(t, "000030", "<h1>業務</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})
	newPage(t, "000031", "<h1>受注</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: "000030"})
	newPage(t, "000032", "<h1>いまここ</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: "000031"})

	got := RenderBreadcrumb(&auth.User{Username: "alice"}, 32)
	for _, want := range []string{
		`<a class="crumb" href="/000000">トップ</a>`,
		`<a class="crumb" href="/000030">業務</a>`,
		`<a class="crumb" href="/000031">受注</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("パンくずに %q がありません:\n%s", want, got)
		}
	}
	// 根に近い順（トップ → 業務 → 受注）。
	if i, j := strings.Index(got, "トップ"), strings.Index(got, "受注"); i > j {
		t.Errorf("順序が根から遠い順になっています:\n%s", got)
	}
	// 現在のページは入れない（真下の h1 と同じ言葉が2度出るのを避ける）。
	if strings.Contains(got, "いまここ") {
		t.Errorf("現在のページがパンくずに入っています:\n%s", got)
	}
}

// TestBreadcrumbHidesUnreadableAncestor は、**読めない先祖の題を伏せる**ことを
// 検証します。read は per-page なので「子は読めるが親は読めない」が起こり得ます
// （匿名では起きません——実効公開は親チェーンの AND のため）。
func TestBreadcrumbHidesUnreadableAncestor(t *testing.T) {
	// mode の桁は 0〜3（2=読み・3=読み書き。Unix風の 7 は無効値で 0 に落ちる）。
	setupTemplateAPITest(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: "332"})
	// 中間は alice だけが読める（other の桁が 0）。
	newPage(t, "000040", "<h1>秘密の分類</h1>", page.PageMeta{
		Owner: "alice", Mode: "300", ParentID: TopPageID})
	// 葉は誰でも読める（other も読める）。
	newPage(t, "000041", "<h1>公開の子</h1>", page.PageMeta{
		Owner: "alice", Mode: "332", ParentID: "000040"})

	bob := RenderBreadcrumb(&auth.User{Username: "bob"}, 41)
	if strings.Contains(bob, "秘密の分類") {
		t.Errorf("読めない先祖の題が漏れています:\n%s", bob)
	}
	if !strings.Contains(bob, `class="crumb-hidden"`) {
		t.Errorf("伏せた先祖の印がありません:\n%s", bob)
	}
	if strings.Contains(bob, `href="/000040"`) {
		t.Errorf("読めない先祖がリンクになっています:\n%s", bob)
	}
	// 所有者には見える（伏せるのは権限の無い相手にだけ）。
	if alice := RenderBreadcrumb(&auth.User{Username: "alice"}, 41); !strings.Contains(alice, "秘密の分類") {
		t.Errorf("所有者にも伏せてしまっています:\n%s", alice)
	}
}

// TestBreadcrumbEmptyAtTop は、先祖の無いページで空を返すことを検証します
// （帯は CSS の :empty で畳まれる）。
func TestBreadcrumbEmptyAtTop(t *testing.T) {
	setupTemplateAPITest(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	if got := RenderBreadcrumb(&auth.User{Username: "alice"}, 0); got != "" {
		t.Errorf("トップページで空になりません: %q", got)
	}
}

// TestBreadcrumbEscapesTitle は、題のエスケープを検証します
// （サニタイズの後にHTMLを足す関数の1つ——エスケープ責任は自分で負う）。
func TestBreadcrumbEscapesTitle(t *testing.T) {
	setupTemplateAPITest(t)
	newPage(t, TopPageID, "<h1>トップ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode})
	newPage(t, "000050", "<h1>A&amp;B &lt;script&gt;</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})
	newPage(t, "000051", "<h1>子</h1>", page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: "000050"})

	got := RenderBreadcrumb(&auth.User{Username: "alice"}, 51)
	if strings.Contains(got, "<script>") {
		t.Errorf("題がエスケープされていません:\n%s", got)
	}
	if !strings.Contains(got, "A&amp;B") {
		t.Errorf("題が入っていません:\n%s", got)
	}
}
