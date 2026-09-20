package contacts

import "testing"

// 未登録の連絡先の「組織」欄が、**連絡帳にある実物の題**を初期値にすることを固定します。
//
// ⚠ **ここが表記ゆれの発生源でした**（2026-09-20）。同じドメインを持つ組織が1つも
// 無いとき、欄の初期値はメールの表示名そのまま（`株式会社南北スポーツ機械`）に
// なります。連絡帳に `南北スポーツ機械` が居ても、登録の口は**完全一致**でしか
// 寄せないので（`PartnerByTitle`）、押した瞬間に**会社ページが2枚**できます。
//
// **2枚できること自体より、黙って結ばれないほうが重い**——加工製品の整理は
// `PartnerByTitle`（完全一致）で2つの木を結び、結べないときは異常と見なさずに
// 黙って戻ります（`linkPartner`）。誰も気づけません。

// TestOrgCandidatesIncludesFoldedMatch は、**法人格違いの組織が候補に入る**ことを
// 固定します。候補に入れば初期値もそれになります（`contactRowInitials` は
// 「『個人』以外がちょうど1つならそれ」を採るため）。
func TestOrgCandidatesIncludesFoldedMatch(t *testing.T) {
	user, companyID := setupPartnerTree(t) // 連絡帳／南北スポーツ機械

	// ドメインを持つ組織は1つも無い（＝候補が出ない場面）。表示名だけが手掛かり。
	c := UnknownContact{
		Address:     "sato@example-sports.co.jp",
		Domain:      "example-sports.co.jp",
		Name:        "株式会社南北スポーツ機械",
		SuggestName: "株式会社南北スポーツ機械",
	}

	cands := orgCandidates(user, c)

	var found bool
	for _, o := range cands {
		if o.Title == "南北スポーツ機械" {
			found = true
			if o.ID != companyID {
				t.Errorf("候補のIDが連絡帳の組織と違います: %q（欲しいのは %q）", o.ID, companyID)
			}
		}
		// ⚠ **畳んだ形や読んだ名前を候補にしてはいけません**——候補は必ず
		// 「連絡帳にある実物の題」です。ここが崩れると、押した先に別のページができます。
		if o.Title == "株式会社南北スポーツ機械" {
			t.Errorf("読んだ名前が候補に混じっています: %q", o.Title)
		}
	}
	if !found {
		t.Fatalf("法人格違いの組織が候補に出ません: %+v", cands)
	}

	// **初期値までそれになる**（人が押すだけで正しい組織へ入る）。
	org, _ := contactRowInitials(c, cands)
	if org != "南北スポーツ機械" {
		t.Errorf("組織欄の初期値が %q です（欲しいのは連絡帳の題）", org)
	}
}

// TestOrgCandidatesKeepsUnknownNameAsIs は、**連絡帳に居ない社名は推測で書き換えない**
// ことを固定します。新しい客先は居ないのが正常で、そのときは読んだ名前のまま人が直します。
func TestOrgCandidatesKeepsUnknownNameAsIs(t *testing.T) {
	user, _ := setupPartnerTree(t)

	c := UnknownContact{
		Address:     "info@example.co.jp",
		Domain:      "example.co.jp",
		Name:        "株式会社まだ知らない製作所",
		SuggestName: "株式会社まだ知らない製作所",
	}
	cands := orgCandidates(user, c)
	org, _ := contactRowInitials(c, cands)
	if org != "株式会社まだ知らない製作所" {
		t.Errorf("知らない社名が書き換わりました: %q", org)
	}
}
