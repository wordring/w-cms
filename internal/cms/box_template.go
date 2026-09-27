package cms

// ─────────────────────────────────────────────────────────────────────────
// 置き場はテンプレートから作る（2026-09-27）
//
// 利用者:「先ほどお話ししたように、コードにハードコーディングせず、テンプレート駆動にしたい
// ということです」「テンプレートが無ければ作れないまで行きます。ハードコーディングを無くしたい」。
//
// トップ直下の置き場（通信箱・連絡帳・取引先・受注・発注など）は、**同じ題のテンプレート**
// （テンプレート置き場の下の葉）を**純粋にコピー**して作ります。**テンプレートが無ければ
// 作りません**——それまでは拡張のコードが置き場の本文（`RequiredPage.Body`）を持っていて、
// 「この位置にこの鏡」がコードに焼き込まれていました。いまは置き場の形は**人が直せる
// テンプレート**が正本で、コードには言葉（題）しかありません。
//
// ⚠ **テンプレート置き場そのもの**はテンプレートの入れ物なので、見出しだけで作ります
// （ここだけは卵と鶏）。
// ⚠ テンプレートは**環境ごとのデータ**です（Git に入らない）——別の環境へは
// `tools/templates.js` で書き出して運びます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ErrNoBoxTemplate は、置き場を作るテンプレートが無いことを表します。
var ErrNoBoxTemplate = errors.New("置き場を作るテンプレートがありません" +
	"（テンプレート置き場の下に、置き場と同じ題のテンプレートを作ってください）")

// BoxTemplateID は、題 title の置き場を作るテンプレート（テンプレート置き場の下の**葉**で、
// 題が同じもの）のページIDを返します。無ければ `ErrNoBoxTemplate`、同じ題の葉が2枚以上
// あれば、どちらを使うか決められないのでエラーです（黙って片方を選ぶと、直したのに
// 効かない、が起きます）。
//
// ⚠ 見るのは**全部の**テンプレートです（閲覧者の権限で絞りません）——置き場を作るのは
// 仕組みの仕事で、作る人がテンプレートを読めるかどうかとは別の問いです。辿るのは
// **読み切ってから返す** `ChildPages`（行を読みながら別の問い合わせを投げない）。
func BoxTemplateID(title string) (string, error) {
	title = strings.TrimSpace(title)
	rootID, ok := templateRootID()
	if !ok {
		return "", ErrNoBoxTemplate
	}
	rootInt, err := strconv.Atoi(rootID)
	if err != nil {
		return "", ErrNoBoxTemplate
	}
	var hits []string
	var walk func(parent, depth int) error
	walk = func(parent, depth int) error {
		if depth >= maxTemplateDepth {
			return nil
		}
		kids, err := ChildPages(database.DB, parent)
		if err != nil {
			return err
		}
		for _, k := range kids {
			grand, err := ChildPages(database.DB, k.ID)
			if err != nil {
				return err
			}
			if len(grand) > 0 { // 枝（分類）は降りる。テンプレートは葉だけ
				if err := walk(k.ID, depth+1); err != nil {
					return err
				}
				continue
			}
			if strings.TrimSpace(k.Title) == title {
				hits = append(hits, page.FormatID(k.ID))
			}
		}
		return nil
	}
	if err := walk(rootInt, 0); err != nil {
		return "", err
	}
	switch len(hits) {
	case 0:
		return "", ErrNoBoxTemplate
	case 1:
		return hits[0], nil
	default:
		return "", fmt.Errorf("「%s」のテンプレートが%d枚あります（%s）——どれで作るか決められません。1枚にしてください",
			title, len(hits), strings.Join(hits, "・"))
	}
}

// boxBodyFromTemplate は、題 title の置き場を作るときの本文を返します——同じ題のテンプレートの
// 純粋なコピー（ブロックIDだけ外す・`CopyTemplateBody`）。テンプレートが無ければ
// `ErrNoBoxTemplate` を包んで返します（題を添えて）。
func boxBodyFromTemplate(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == TemplateRootTitle {
		return "<h1>" + html.EscapeString(title) + "</h1>", nil
	}
	tid, err := BoxTemplateID(title)
	if err != nil {
		if errors.Is(err, ErrNoBoxTemplate) {
			return "", fmt.Errorf("「%s」: %w", title, err)
		}
		return "", err
	}
	raw, err := ReadPageBody(tid)
	if err != nil {
		return "", fmt.Errorf("「%s」のテンプレート %s を読めません: %w", title, tid, err)
	}
	return CopyTemplateBody(raw), nil
}
