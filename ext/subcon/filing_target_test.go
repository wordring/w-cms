package subcon

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// 整理の欄を打ち替えるたびに「その行き先にページがあるか」を聞く口（2026-09-20）。
//
// ユーザー:「図面名称と装置名称を手がかりに、改定図面や追加図面を認識するはずですが、
// 問題はそれらは**編集者が微妙に書き換える**ことです。整理画面で編集者が書き換える
// たびに、既存のページがあるか**検索しなおす**必要があります」。

func getFilingTarget(t *testing.T, u *auth.User, customer, stage, machine, name string) map[string]any {
	t.Helper()
	q := url.Values{}
	q.Set("customer", customer)
	q.Set("stage", stage)
	q.Set("machine", machine)
	q.Set("name", name)
	req := httptest.NewRequest("GET", "/api/filing-target?"+q.Encode(), nil)
	if u != nil {
		req = auth.WithUser(req, u)
	}
	rr := httptest.NewRecorder()
	FilingTargetAPIHandler(rr, req)
	if rr.Code != 200 {
		t.Fatalf("問い合わせできません: %d %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("応答を読めません: %v %s", err, rr.Body.String())
	}
	return out
}

// TestFilingTargetFindsExistingPage は、**整理済みのページを見つける**ことを固定します。
func TestFilingTargetFindsExistingPage(t *testing.T) {
	const inbox = "000061"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(first, "取付ベース", "")})

	got := getFilingTarget(t, u, "南北スポーツ", "現行", "標準2輪", "取付ベース")
	if got["exists"] != true {
		t.Fatalf("既にあるページを見つけていません: %+v", got)
	}
	if got["page_id"] != first {
		t.Errorf("行き先が違います: %+v", got)
	}
	// **既に載っている図面番号**を添える（人が改定か別図面かを決める手掛かり）。
	nos, _ := got["drawing_nos"].([]any)
	if len(nos) != 1 || nos[0] != "K120-1" {
		t.Errorf("図面番号が添えられていません: %+v", got["drawing_nos"])
	}
}

// TestFilingTargetSaysNoWhenAbsent は、**無いときは無いと言う**ことを固定します。
func TestFilingTargetSaysNoWhenAbsent(t *testing.T) {
	const inbox = "000062"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	got := getFilingTarget(t, u, "まだ知らない客", "現行", "まだ知らない装置", "まだ知らない図面")
	if got["exists"] != false {
		t.Errorf("在ることにしています: %+v", got)
	}
}

// TestFilingTargetCreatesNothing は、**問い合わせただけでは1枚も作らない**ことを
// 固定します。
//
// ⚠ **これがこの口のいちばん危ないところ**です。整理の本体は `ensureChildPage`
// （無ければ作る）で行き先を辿りますが、同じ関数をここで使うと**編集者が図面名称を
// 1文字打つたびに、空の顧客ページ・段ページ・装置ページが増えます**。しかも増えた
// ページは次の問い合わせで「在る」と答えるので、**誤りが自分を正当化します**。
func TestFilingTargetCreatesNothing(t *testing.T) {
	const inbox = "000063"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	// ⚠ **先に1枚整理して「取引先」の箱を作っておきます。** 箱が無いと問い合わせは
	// 辿る前に戻るので、**作る作らない以前にループへ入りません**——最初それで
	// 「作らない」試験が**変異させても通って**いました（測っていたのは早期戻りでした）。
	seed := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(seed, "取付ベース", "")})

	before := countPages(t)
	// 打ちかけの途中のような値で何度も聞く（既にある顧客の下と、まったく新しい行き先）。
	for _, name := range []string{"脚", "脚取", "脚取付", "別の図面"} {
		getFilingTarget(t, u, "南北スポーツ", "現行", "標準2輪", name)
		getFilingTarget(t, u, "新しい客先", "現行", "新しい装置", name)
	}
	if after := countPages(t); after != before {
		t.Errorf("⚠ 問い合わせただけでページが増えています: %d → %d", before, after)
	}
}

// TestFilingTargetNeedsLogin は、ログインしていなければ断ることを固定します。
func TestFilingTargetNeedsLogin(t *testing.T) {
	const inbox = "000064"
	setupFilingTest(t, inbox)

	req := httptest.NewRequest("GET", "/api/filing-target?customer=a&machine=b&name=c", nil)
	rr := httptest.NewRecorder()
	FilingTargetAPIHandler(rr, req)
	if rr.Code == 200 {
		t.Errorf("ログイン無しで答えています: %d %s", rr.Code, rr.Body.String())
	}
}

// TestFilingTargetNormalizesLikeFiling は、**整理の本体と同じ畳み方**で引くことを
// 固定します。
//
// ⚠ 揃えないと、画面は「無い」と言うのに実行すると「在る」になります——**見えている
// ものと起きることが食い違う**、いちばん質の悪いずれ方です。
func TestFilingTargetNormalizesLikeFiling(t *testing.T) {
	const inbox = "000065"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	// 整理は `NormalizeNameForIngest` を通すので、全角の英数や空白は畳まれて入ります。
	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準２輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{{PageID: first, Customer: "南北スポーツ", Stage: "現行",
		MachineName: "標準２輪", DrawingName: "取付ベース"}})

	// 画面から**全角のまま**聞いても、同じページに当たること。
	got := getFilingTarget(t, u, "南北スポーツ", "現行", "標準２輪", "取付ベース")
	if got["exists"] != true {
		t.Errorf("畳み方が整理の本体と揃っていません: %+v", got)
	}
	_ = cms.TopPageID
}
