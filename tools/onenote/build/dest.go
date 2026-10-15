package main

import (
	"fmt"
	"regexp"
	"strings"
)

// 表の行き先をワンノートのページのセクション（階層）と題で選ぶ（2026-10-15）。
//
// 利用者:「ページ番号依存の処理は問題があるので、原則に従い、ページ階層とタイトルによって処理を振り分けるよう変更して下さい」。
// それまでの鍵は w-cms のページ番号で、環境ごとに番号が違う（家で作り直すと別の品物のページに当たる）。

// pageNumberRe はページ番号らしい鍵です（6桁の数・頭の / は許す）——もう鍵にしない。
var pageNumberRe = regexp.MustCompile(`^/?\d{1,6}$`)

// tableDestsByPage は「表の行き先」を、目録のページ（目録の鍵）ごとの行き先にします。
//
//   - セクションと題が両方合うページに当てる。セクションが空なら題だけで選ぶ（目録でその題が1つのときだけ——
//     2つ以上あればどれか分からないので止まる）。
//   - 当たるページが無い・題がページ番号に見える件は止まる（黙って効かない設定にしない）。
func tableDestsByPage(rules []tableDestRule, cat catalog) (map[string]map[string][]string, error) {
	out := map[string]map[string][]string{}
	for i, r := range rules {
		title, sec := strings.TrimSpace(r.Title), strings.TrimSpace(r.Section)
		where := fmt.Sprintf("%s の「表の行き先」の %d 件目", settingsName, i+1)
		if title == "" || pageNumberRe.MatchString(title) {
			return nil, fmt.Errorf("%s: ページはワンノートのセクションと題で書いてください（ページ番号では選びません）——%q", where, title)
		}
		var hits []string
		for id, p := range cat.Pages {
			if p.Gone || strings.TrimSpace(p.Title) != title {
				continue
			}
			if sec != "" && strings.TrimSpace(p.Section) != sec {
				continue
			}
			hits = append(hits, id)
		}
		switch {
		case len(hits) == 0:
			return nil, fmt.Errorf("%s: 目録にそのページがありません（セクション %q・題 %q）", where, sec, title)
		case len(hits) > 1:
			return nil, fmt.Errorf("%s: 題 %q のページが %d 枚あります——セクションも書いてください", where, title, len(hits))
		}
		out[hits[0]] = r.Tables
	}
	return out, nil
}

// stringList は何度でも書ける引数です（-only "題" -only "題"）。
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }
