package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTableDestsByPage は「表の行き先」をワンノートのセクションと題で選ぶことを固定します（2026-10-15 利用者:
// 「ページ番号依存の処理は問題があるので、原則に従い、ページ階層とタイトルによって処理を振り分けるよう変更して下さい」）。
func TestTableDestsByPage(t *testing.T) {
	var cat catalog
	if err := json.Unmarshal([]byte(`{"pages": {"a": {"title": "取付ベース", "section": "部品A"}, "b": {"title": "取付ベース", "section": "部品B"}, "c": {"title": "ステー", "section": "部品B"}}}`), &cat); err != nil {
		t.Fatal(err)
	}
	tables := map[string][]string{"材料": {"材料表", "外注加工表"}}

	got, err := tableDestsByPage([]tableDestRule{{Section: "部品B", Title: "取付ベース", Tables: tables}, {Title: "ステー", Tables: tables}}, cat)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["b"] == nil || got["c"] == nil || got["a"] != nil {
		t.Errorf("当て先が違います: %v", got)
	}
	for _, c := range []struct {
		rule tableDestRule
		why  string
	}{
		{tableDestRule{Title: "001392", Tables: tables}, "ページ番号では選びません"},
		{tableDestRule{Title: "取付ベース", Tables: tables}, "2 枚あります"},
		{tableDestRule{Section: "部品A", Title: "無い題", Tables: tables}, "目録にそのページがありません"},
	} {
		if _, err := tableDestsByPage([]tableDestRule{c.rule}, cat); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%+v: %v（%q のはず）", c.rule, err, c.why)
		}
	}
}
