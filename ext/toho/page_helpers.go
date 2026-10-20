package toho

// ページを扱う小さな道具（2026-10-20 に filing.go から分けた）——題で子を探す・書けるか・ID の形・題を読む・エスケープ・ページを動かす。

import (
	stdhtml "html"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// findChildByTitle は親の子から題が完全一致するものを1つ探します。
// **完全一致だけ**にするのは、揺れを機械が吸収すると別の顧客が1つに潰れるため
// ——名寄せは人の仕事です（欄を直せば済む）。
func findChildByTitle(parentID, title string) (string, bool) {
	return cms.FindChildByTitle(parentID, title)
}

// canWritePage は書き込み権限の素の判定です（HTTPの口を通さない版）。
func canWritePage(user *auth.User, pageIDInt int) bool {
	return page.GetPerms(pageIDInt).CanWrite(user)
}

// formatID はページIDをゼロ詰め6桁へ整えます。
func formatID(idInt int) string {
	return page.FormatID(idInt)
}

// pageTitleOf は索引から題を引きます（引けなければIDをそのまま返す）。
func pageTitleOf(pageID string) string {
	idInt, err := strconv.Atoi(pageID)
	if err != nil {
		return pageID
	}
	if t := cms.PageTitleByID(idInt); t != "" {
		return t
	}
	return pageID
}

// htmlEscape は本文へ入れる前の逃がしです（サニタイザは安全の網で、
// エスケープの肩代わりはしません——サニタイズ後にHTMLを足す関数と同じ責任）。
func htmlEscape(s string) string { return stdhtml.EscapeString(s) }

// movePage は加工製品ページを行き先の下へ移し、題を図面名称に揃えます。
//
// 題を揃えるのは、**題がページ名だから**——整理の画面で図面名称を直したのに
// ページの題が古いままだと、次に同じ部品が来たとき「既にある」の判定
// （＝改定図面の合図）が効きません。
func movePage(user *auth.User, pageID, newParent, title string) error {
	if err := cms.SetPageH1(pageID, user.Username, htmlEscape(title)); err != nil {
		return err
	}
	_, _, err := cms.SetPageParent(user, pageID, newParent)
	return err
}
