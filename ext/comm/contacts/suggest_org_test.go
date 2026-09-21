package contacts

import "testing"

// 社名の候補は**提案であって判定ではありません**（suggest_org.go の冒頭）。
// ここで固定するのは4つ——拾うこと・実物の題を返すこと・迷ったら黙ること・
// 居なければ読んだ名前をそのまま返すこと。
//
// ⚠ **法人格の表は実物の `config/settings.json` を使います**（TestMain が読む）。
// コアへ試験専用の公開関数を足さない、というこのプロジェクトの割り切りに従いました
// ——`ext/subcon` が `machine_stages` を実物から読むのと同じ形です。
// だから `company_forms` から `株式会社` を消すと、ここが落ちて気づけます。

// TestSuggestOrgFoldsCompanyForm は、**法人格の有無を越えて連絡帳の組織を拾う**ことを
// 固定します。これがご要望の本体です。
func TestSuggestOrgFoldsCompanyForm(t *testing.T) {
	user, _ := setupPartnerTree(t) // 連絡帳／南北スポーツ機械

	for _, read := range []string{
		"株式会社南北スポーツ機械", // 前株
		"南北スポーツ機械(株)",  // 後株
		"㈱南北スポーツ機械",    // NFKC が (株) へ開いてから落ちる
	} {
		s, ok := SuggestOrg(user, read)
		if !ok {
			t.Errorf("%q の候補が出ません", read)
			continue
		}
		// ⚠ **返すのは連絡帳にある実物の題**です（畳んだ形ではない）。
		// ここが畳んだ形になると「見たままと裏が一致」が崩れます。
		if s.Title != "南北スポーツ機械" {
			t.Errorf("%q → %q；連絡帳の実物の題を返すべきです", read, s.Title)
		}
		if s.Exact {
			t.Errorf("%q は題と違うのに Exact になっています", read)
		}
	}
}

// TestSuggestOrgExactNeedsNoFix は、**そのままで良いときはそう言う**ことを固定します。
// 呼ぶ側は欄を光らせずに済みます。
func TestSuggestOrgExactNeedsNoFix(t *testing.T) {
	user, _ := setupPartnerTree(t)

	s, ok := SuggestOrg(user, "南北スポーツ機械")
	if !ok || !s.Exact {
		t.Errorf("題そのままなのに Exact になりません: %+v ok=%v", s, ok)
	}
}

// TestSuggestOrgStaysSilentWhenAmbiguous は、**迷ったら黙る**ことを固定します。
//
// `株式会社あさひ` と `有限会社あさひ` は法人格を落とすと同じ鍵になりますが、
// **実在しうる別会社**です。どちらかを推すと、人が間違ったほうをそのまま押します
// ——`PartnerByTitle` が「2枚あったら引かない」のと同じ判断。
func TestSuggestOrgStaysSilentWhenAmbiguous(t *testing.T) {
	user, _ := setupPartnerTree(t)

	boxID, ok := ContactsBoxPageID()
	if !ok {
		t.Fatal("連絡帳がありません")
	}
	for _, name := range []string{"株式会社あさひ", "有限会社あさひ"} {
		if _, err := ensureChildByTitle(user, boxID, name); err != nil {
			t.Fatalf("%s を作れません: %v", name, err)
		}
	}
	if s, ok := SuggestOrg(user, "あさひ"); ok {
		t.Errorf("別会社が2つ在るのに候補を出しています: %+v", s)
	}
}

// TestOrgNameForPageFoldsWhenAbsent は、⚠ **連絡帳に居なければ法人格を落とす**ことを
// 固定します（2026-09-21 ユーザー:「発注元タグに株式会社が入るのが気になります」）。
//
// ⚠ **これが直した穴です。** それまでは読んだ名前をそのまま返していたので、
// **新しい客先の1通目では一度も揃いませんでした**——連絡帳に居ないうちは
// `株式会社○○` がそのままタグになり、あとから連絡帳に `○○` を作っても
// 完全一致では結ばれません。
func TestOrgNameForPageFoldsWhenAbsent(t *testing.T) {
	user, _ := setupPartnerTree(t)

	if got := OrgNameForPage(user, "株式会社まだ居ない商会"); got != "まだ居ない商会" {
		t.Errorf("OrgNameForPage = %q; 法人格を落とした形を期待します", got)
	}
	// **空は埋めません**——読めなかったことを人に見せます。
	if got := OrgNameForPage(user, "  "); got != "" {
		t.Errorf("空から %q を作っています", got)
	}
}

// TestOrgNameForPageKeepsReadWhenAmbiguous は、⚠ **迷ったら読んだ名前のまま**を
// 固定します。
//
// `株式会社あさひ` と `有限会社あさひ` がどちらも連絡帳に在るとき、畳んだ `あさひ` は
// **どちらでもない第三の名前**です。機械が迷っているときに新しい名前を作らせません
// ——「名寄せを機械にやらせない」の一部です。
func TestOrgNameForPageKeepsReadWhenAmbiguous(t *testing.T) {
	user, _ := setupPartnerTree(t)
	boxID, ok := ContactsBoxPageID()
	if !ok {
		t.Fatal("連絡帳がありません")
	}
	for _, name := range []string{"株式会社あさひ", "有限会社あさひ"} {
		if _, err := ensureChildByTitle(user, boxID, name); err != nil {
			t.Fatalf("%s を作れません: %v", name, err)
		}
	}
	const read = "㈱あさひ"
	if got := OrgNameForPage(user, read); got != read {
		t.Errorf("OrgNameForPage(%q) = %q; 迷ったら読んだ名前のままであるべきです", read, got)
	}
}
