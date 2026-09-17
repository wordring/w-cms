package contacts

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// 登録の口（POST /api/contacts/register）のテスト——2026-09-17 に「組織」「担当者」の
// 2欄へ畳んだあとの形。固定するのは:
//   - 新しい組織＋担当者を1回で作れる（取引とドメインは組織、アドレスは人）
//     ⚠ `domains` を送る画面はもうありません（2026-09-17 にチェックを外した）。
//     口は残っているので、ここが**その口の番人**です。
//   - 題の完全一致で既にある組織へ寄る（コンボボックスに無い名前を打っても会社が2枚にならない）
//   - 「個人」は取引が人に付き、ドメインは付かない。担当者が無ければ断る
//   - 候補の並び（同じドメインの組織 →「個人」）と欄の初期値

func postRegister(t *testing.T, u *auth.User, body map[string]any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/contacts/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req = auth.WithUser(req, u)
	rr := httptest.NewRecorder()
	RegisterContactAPIHandler(rr, req)
	var res map[string]any
	json.Unmarshal(rr.Body.Bytes(), &res)
	return rr.Code, res
}

func bodyOfPage(t *testing.T, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(page.GetPageDir(id), id+".html"))
	if err != nil {
		t.Fatalf("ページ %s を読めません: %v", id, err)
	}
	return string(b)
}

func TestRegisterNewOrgWithPerson(t *testing.T) {
	setupPartnerBox(t)
	u := &auth.User{Username: "alice", IsAdmin: true}
	code, res := postRegister(t, u, map[string]any{
		"name": "南北スポーツ機械", "relation": RelationCustomer,
		"person_name": "山田 太郎", "addresses": []string{"yamada@example-sports.co.jp"},
		"domains": []string{"example-sports.co.jp"},
	})
	if code != 200 || res["success"] != true || res["created"] != true {
		t.Fatalf("登録できません: %d %+v", code, res)
	}
	if res["title"] != "南北スポーツ機械／山田 太郎" {
		t.Errorf("題が違います: %v", res["title"])
	}
	orgID, ok := PartnerByTitle(u, "南北スポーツ機械")
	if !ok {
		t.Fatal("組織ページができていません")
	}
	org := bodyOfPage(t, orgID)
	if !strings.Contains(org, "<dt>"+RelationTag+"</dt><dd>"+RelationCustomer+"</dd>") ||
		!strings.Contains(org, "<dt>"+DomainTag+"</dt><dd>example-sports.co.jp</dd>") {
		t.Errorf("組織に取引とドメインがありません:\n%s", org)
	}
	if strings.Contains(org, EmailTag) {
		t.Errorf("担当者が居るのに組織にアドレスが付きました:\n%s", org)
	}
	person := bodyOfPage(t, res["page_id"].(string))
	if !strings.Contains(person, "<dd>yamada@example-sports.co.jp</dd>") {
		t.Errorf("人のページにアドレスがありません:\n%s", person)
	}
	meta, _ := page.ReadSidecar(res["page_id"].(string))
	if meta.ParentID != orgID {
		t.Errorf("人が組織の直下にいません: %+v", meta)
	}
}

func TestRegisterByTitleMergesIntoExisting(t *testing.T) {
	box := setupPartnerBox(t)
	u := &auth.User{Username: "alice", IsAdmin: true}
	partnerPage(t, "000201", box, "コニック金型センター", RelationSupplier, "info@konic.example")

	// 候補に無い名前を打った想定（page_id 無し）。題が一致するので既存へ寄る。
	code, res := postRegister(t, u, map[string]any{
		"name": "コニック金型センター", "relation": RelationCustomer,
		"addresses": []string{"order@konic.example"},
	})
	if code != 200 || res["merged"] != true || res["page_id"] != "000201" {
		t.Fatalf("既にある組織へ寄りません（会社が2枚になる）: %d %+v", code, res)
	}
	body := bodyOfPage(t, "000201")
	if !strings.Contains(body, "<dd>order@konic.example</dd>") {
		t.Errorf("アドレスが足されていません:\n%s", body)
	}
	if strings.Count(body, RelationCustomer) != 0 {
		t.Errorf("既存の組織の取引を書き換えました:\n%s", body)
	}
}

func TestRegisterPersonalPutsRelationOnPerson(t *testing.T) {
	setupPartnerBox(t)
	u := &auth.User{Username: "alice", IsAdmin: true}

	// 担当者が無い「個人」は断る（人の器なので）。
	if code, _ := postRegister(t, u, map[string]any{
		"name": PersonalOrgTitle, "relation": RelationCustomer,
		"addresses": []string{"yamada@yahoo.co.jp"},
	}); code != 400 {
		t.Errorf("担当者の無い「個人」を受け付けました: %d", code)
	}

	code, res := postRegister(t, u, map[string]any{
		"name": PersonalOrgTitle, "relation": RelationCustomer, "person_name": "山田太郎",
		"addresses": []string{"yamada@yahoo.co.jp"}, "domains": []string{"yahoo.co.jp"},
	})
	if code != 200 || res["created"] != true {
		t.Fatalf("個人を登録できません: %d %+v", code, res)
	}
	orgID, ok := PartnerByTitle(u, PersonalOrgTitle)
	if !ok {
		t.Fatal("「個人」の組織ページができていません")
	}
	org := bodyOfPage(t, orgID)
	if strings.Contains(org, RelationTag) || strings.Contains(org, DomainTag) {
		t.Errorf("「個人」に取引かドメインが付きました（共有ドメインを個人に結ぶことになる）:\n%s", org)
	}
	person := bodyOfPage(t, res["page_id"].(string))
	if !strings.Contains(person, "<dt>"+RelationTag+"</dt><dd>"+RelationCustomer+"</dd>") ||
		!strings.Contains(person, "<dd>yamada@yahoo.co.jp</dd>") {
		t.Errorf("人のページに取引とアドレスがありません:\n%s", person)
	}

	// 2人目は同じ「個人」の下へ（組織は増えない）。
	code, res = postRegister(t, u, map[string]any{
		"name": PersonalOrgTitle, "relation": RelationCustomer, "person_name": "鈴木花子",
		"addresses": []string{"hanako@gmail.example"},
	})
	if code != 200 || res["created"] != false {
		t.Fatalf("2人目で「個人」が2枚になりました: %d %+v", code, res)
	}
	meta, _ := page.ReadSidecar(res["page_id"].(string))
	if meta.ParentID != orgID {
		t.Errorf("2人目が同じ「個人」の下にいません: %+v", meta)
	}
}

func TestOrgCandidatesAndInitials(t *testing.T) {
	box := setupPartnerBox(t)
	u := &auth.User{Username: "alice", IsAdmin: true}
	partnerPageWith(t, "000201", box, "南北スポーツ機械", RelationCustomer,
		nil, []string{"example-sports.co.jp"})
	partnerPage(t, "000202", box, "コニック金型センター", RelationSupplier)
	newPage(t, "000210", "<h1>山田 太郎</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode, ParentID: "000201"})

	// ドメインの持ち主が1つ → 候補は［その組織, 個人］、組織欄はそれ、担当者欄は表示名。
	c := UnknownContact{Address: "sato@example-sports.co.jp", Domain: "example-sports.co.jp",
		Name: "平井 一郎", SuggestName: "平井 一郎",
		DomainOwners: []PartnerRef{{ID: "000201", Title: "南北スポーツ機械"}}}
	cands := orgCandidates(u, c)
	var titles []string
	for _, o := range cands {
		titles = append(titles, o.Title)
	}
	if got := strings.Join(titles, "|"); got != "南北スポーツ機械|"+PersonalOrgTitle {
		t.Errorf("候補が違います: %q", got)
	}
	if len(cands[0].Persons) != 1 || cands[0].Persons[0].Title != "山田 太郎" {
		t.Errorf("担当者の候補が違います: %+v", cands[0].Persons)
	}
	if cands[1].ID != "" {
		t.Errorf("まだ無い「個人」は ID 無しのはず: %+v", cands[1])
	}
	org, person := contactRowInitials(c, cands)
	if org != "南北スポーツ機械" || person != "平井 一郎" {
		t.Errorf("初期値が違います: org=%q person=%q", org, person)
	}

	// 持ち主が2つ（共有ドメイン）→ 組織欄は空（機械は選ばない）。会社の口は担当者が空。
	c2 := UnknownContact{Address: "order@shared.example", Domain: "shared.example", Name: "order@shared.example",
		DomainOwners: []PartnerRef{{ID: "000201", Title: "南北スポーツ機械"}, {ID: "000202", Title: "コニック金型センター"}}}
	org, person = contactRowInitials(c2, orgCandidates(u, c2))
	if org != "" || person != "" {
		t.Errorf("共有ドメインで機械が選びました: org=%q person=%q", org, person)
	}

	// 候補が無く、表示名が社名らしい → 組織欄にその名前。
	c3 := UnknownContact{Address: "info@new.example", Domain: "new.example", Name: "株式会社ニュー", SuggestName: "株式会社ニュー"}
	org, person = contactRowInitials(c3, orgCandidates(u, c3))
	if org != "株式会社ニュー" || person != "" {
		t.Errorf("社名らしい表示名が組織欄に入りません: org=%q person=%q", org, person)
	}
}
