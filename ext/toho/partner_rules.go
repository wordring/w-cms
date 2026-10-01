package toho

// ─────────────────────────────────────────────────────────────────────────
// 取引先ごとの加工製品の決まり——整理で新しく置くページに品番・品名・弊社品番を書く（2026-10-01）
//
// 利用者:「整理ボタンを押してプーリーを追加したのですが、品名や弊社品番のタグが出来ず、品番に図面番号も入りません」。
//
// ワンノートの移植の道具（tools/onenote/build）は、取引先ごとの決まり（2026-09-28 利用者:「〈ある取引先〉に限っては、
// 加工製品の品番に図面番号を入れてください」「品名タグを作って値として図面名称を入れてください」）と、弊社品番＝ページ
// 番号（同:「弊社品番としてタグにページ番号を入れてください。検索できるようにです」）を書いていた。整理（メールの図面を
// 🤖解析で読んで置く道）は知らなかったので、同じ取引先の加工製品でも、置いた道によってタグが違っていた。
//
// **決まりは取引先のページのタグに書きます**（`取引先／社名` のページ）——取引先の名前は実データなので、リポジトリの
// 設定（config/settings.json）には書けない。ページに書けば、人が見て直せる:
//
//	品番の決め方：図面番号   … 加工製品の品番（題の下のタグ）に図面番号を入れる
//	品名の決め方：図面名称   … 加工製品の品名（題の下のタグ）に図面名称を入れる
//
// 弊社品番（このページの番号）は**どの取引先でも**書く（ワンノートの道具と同じ）。
//   - 書くのは整理で**新しく置いたとき**だけ（図面追加・図面改定は既にあるページの品番・品名をそのまま——改定の品名は
//     【要求】東邦拡張 §13 の決まりで足す）。
//   - **空いているときだけ**書く——人が書いた品番・品名は上書きしない。
//   - 知らない値（`品番の決め方：品番` など）は書かずに、整理の結果の文で知らせる（黙って既定に戻さない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"regexp"
	"strconv"
	"strings"

	stdhtml "html"

	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// 取引先のページに書く決まりのタグと、書ける値。
const (
	PartNoRuleTag   = "品番の決め方"
	ItemNameRuleTag = "品名の決め方"
	ruleDrawingNo   = "図面番号"
	ruleDrawingName = "図面名称"
)

// partnerRule は取引先ごとの加工製品の決まりです。
type partnerRule struct {
	PartNoIsDrawingNo     bool
	ItemNameIsDrawingName bool
	Unknown               []string // 分からない値（「品番の決め方：○○」の形で）
}

// partnerRuleFor は取引先の名前（`取引先／社名` のページの題）から決まりを読みます（ページが無ければ決まり無し）。
func partnerRuleFor(customer string) partnerRule {
	if customer == "" {
		return partnerRule{}
	}
	if boxID, ok := CustomerBoxPageID(); ok {
		if customerID, found := findChildByTitle(boxID, customer); found {
			return partnerRuleOf(customerID)
		}
	}
	return partnerRule{}
}

// partnerRuleOf は取引先のページ（customerID）のタグから決まりを読みます（読めなければ決まり無し）。
func partnerRuleOf(customerID string) partnerRule {
	var r partnerRule
	id, err := strconv.Atoi(customerID)
	if err != nil {
		return r
	}
	tags, err := cms.TagsOfPage(database.DB, id)
	if err != nil {
		return r
	}
	read := func(tag, want string, set *bool) {
		for _, v := range tags[tag] {
			switch v = strings.TrimSpace(v); v {
			case "":
			case want:
				*set = true
			default:
				r.Unknown = append(r.Unknown, tag+"："+v)
			}
		}
	}
	read(PartNoRuleTag, ruleDrawingNo, &r.PartNoIsDrawingNo)
	read(ItemNameRuleTag, ruleDrawingName, &r.ItemNameIsDrawingName)
	return r
}

// withProductRules は新しく置いた加工製品ページの本文に、弊社品番（pageID）と、決まりがあれば品番（drawingNo）・
// 品名（name）を書いた本文を返します。どれも空いているときだけ。
func withProductRules(body, pageID, drawingNo, name string, r partnerRule) string {
	body = withTagIfEmpty(body, OurItemNoTag, pageID)
	if r.PartNoIsDrawingNo {
		body = withTagIfEmpty(body, ItemNoTag, strings.TrimSpace(drawingNo))
	}
	if r.ItemNameIsDrawingName {
		body = withTagIfEmpty(body, ItemNameTag, strings.TrimSpace(name))
	}
	return body
}

// withTagIfEmpty はタグ name に値が1つも無ければ value を書きます（値があれば、人の値なのでそのまま）。
func withTagIfEmpty(body, name, value string) string {
	if value == "" {
		return body
	}
	re := regexp.MustCompile(`<dt>` + regexp.QuoteMeta(stdhtml.EscapeString(name)) + `</dt>\s*<dd>([^<]*)</dd>`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		if strings.TrimSpace(m[1]) != "" {
			return body
		}
	}
	return withTagValues(body, name, []string{value}, false)
}
