package toho

// 図面ブロックとページのタグの値を、文字で書き換える（2026-10-20 に filing.go から分けた）——整理で直した社名・装置名称・図面名称を本文へ戻す。

import (
	"regexp"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// syncDrawingFields は図面ブロックの `客先`・`装置名称`・`図面名称` を、整理の画面で
// 人が決めた値に揃えます。
//
// **索引のためです。** ③計算も将来の検索も `vocab_index` の `field`＝見出しの表示文字で
// 引くので（D-1）、本文が誤読のままだと階層をいくら直しても引けません。
//
// **書き換えるのは最初の図面ブロックだけ**です。改定で合流した古い図面は子ページへ
// 移っており（mergeAsRevision）、このページに残る図面は最新の1つ——`装置名称` と
// `客先` はそこにしか現れません。
//
// **値に印が付いていたら触りません**（`[^<]*` に当たらない＝人がリンクや強調を
// 書いた）。機械が人の手入れを踏み潰さないための線引きで、そのときは黙って
// そのままにします——階層は直るので、探せなくなるのは1件だけです。
func syncDrawingFields(user *auth.User, pageID, customer, machine, name string) error {
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return err
	}
	// **変わらないなら書きません**——版と更新日時を無駄に進めない。
	// ⚠ 書くときは、錠の中で読み直した本文から組み直します（2026-10-19 夜・rebuildBody）。
	_, _, err = rebuildBody(pageID, user.Username, body, func(cur string) (string, error) {
		fixed := cur
		for _, f := range []struct{ field, value string }{
			{ClientNameTag, customer},
			{MachineNameTag, machine},
			{DrawingNameTag, name},
		} {
			// ⚠ **空は「消したい」ではありません**（2026-09-20）。渡さなかった項目は
			// 触らない、という意味です——空で上書きすると、**二つ目の図面として追加する
			// とき、溶接図の図面名称が消えます**（呼ぶ側が名称だけ渡さない形にしている）。
			// 受注の `syncOrderClient` と同じ約束です。
			if strings.TrimSpace(f.value) == "" {
				continue
			}
			fixed = setDrawingField(fixed, f.field, f.value)
		}
		return fixed, nil
	})
	return err
}

// setDrawingField は図面ブロックの項目を書き換えます。**無ければ足します**。
//
// 足すのは、解析が**読めなかった項目を書かない**ためです——実データでは7枚のうち
// 3枚に `客先` の行がありませんでした。値を直すだけだと、その3枚は今後も
// `客先` で引けません（無い行は索引にも無い）。`客先` は図面ブロックの正式な列
// （vocab.go の drawing）なので、埋めるのは形を壊しません。
//
// 足す位置は**ヘッダの `dl` の末尾**です。`客先` は列の並びでも最後なので、
// たいてい定義どおりの順になります（`装置名称` が欠けていた場合だけ後ろへ付きますが、
// 並びは見た目だけの話で、索引も③計算も見出しの文字で引きます）。
func setDrawingField(body, field, value string) string {
	if fixed := replaceFirstFieldValue(body, field, value); fixed != body {
		return fixed
	}
	if strings.Contains(body, "<dt>"+field+"</dt>") {
		return body // 在るが差し替えなかった（同じ値・または人が印を書いている）
	}
	// 図面ブロックの**可変タグの `dl`** を探します（2026-09-18 に素の `<dl>` から移した
	// ——検索の口が読む表は `page_tags` のほうなので）。
	h2 := strings.Index(body, "<h2>図面</h2>")
	if h2 < 0 {
		return body
	}
	open := cms.FirstTagListOpen(body[h2:]) // 属性（data-id）付きの並びにも当たる
	if open < 0 {
		return body
	}
	open += h2
	close := strings.Index(body[open:], "</dl>")
	if close < 0 {
		return body
	}
	close += open
	return body[:close] + "<dt>" + field + "</dt><dd>" + htmlEscape(value) + "</dd>" + body[close:]
}

// replaceFirstFieldValue は最初の `<dt>名前</dt><dd>値</dd>` の値を差し替えます。
//
// この形の正規表現はこのファイルの既定の作法です（`drawingNoRe`・`sourceRefRe`）
// ——図面ブロックは機械が書くので形が決まっており、DOMへ通して組み直すと
// **本文の他の場所まで書式が変わります**（正本なので、触っていない所は1バイトも
// 変えたくない）。
func replaceFirstFieldValue(body, field, value string) string {
	// **空欄は `<br/>` です**（サニタイズが空の `dd` に入れる）。実データの7枚のうち
	// 3枚の `客先` がこの形で、`[^<]*` だけで見ていたときは**素通りしていました**
	// ——行は在るのに値が無く、索引にも入らないので `客先` で永久に引けません。
	// ⚠ **`</dt>` と `<dd>` のあいだの字下げを許します**（2026-10-20）——人が一度保存したページは字下げされ（罠の節）、
	// 詰めた形だけを見ていたときは、整理で直した客先・装置名称・発注元が黙って書き戻されていなかった。
	re := regexp.MustCompile(`<dt>` + regexp.QuoteMeta(field) + `</dt>\s*<dd>(<br\s*/?>|[^<]*)</dd>`)
	loc := re.FindStringSubmatchIndex(body)
	if loc == nil {
		return body
	}
	return body[:loc[2]] + htmlEscape(value) + body[loc[3]:]
}

// hasTagPair は、本文に「name：value」のタグ（`<dt>name</dt><dd>value</dd>`）があるかです。字下げを許します（2026-10-20）
// ——詰めた形だけで見ていたときは、人が保存した社名のページで「もう結んである」が見えず、整理のたびに `連絡帳` のタグが増えていた。
func hasTagPair(body, name, value string) bool {
	re := regexp.MustCompile(`<dt>\s*` + regexp.QuoteMeta(name) + `\s*</dt>\s*<dd>\s*` + regexp.QuoteMeta(value) + `\s*</dd>`)
	return re.MatchString(body)
}
