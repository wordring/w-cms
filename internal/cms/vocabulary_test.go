package cms

import (
	"strings"
	"testing"
)

// ── 本文の語彙（サニタイザの許可リスト）の到達状態 ────────────────────────

// TestNoCustomElementVocabulary は本文の語彙にカスタム要素（<m-*>）が1つも
// 無いことを検証します。
//
// 語彙モデルへの移行完了（2026-08-20）でカスタム要素はゼロになり、プラグインが
// 語彙を宣言する仕組み（必須メソッド Tags()・PluginTags()・htmldoc.New への注入）
// ごと撤去しました。新しい形式は ①語彙レジストリ（vocab.go）へ data-type として
// 足します——カスタム要素は作りません（2026-08-17 決定・即時発効）。
// このテストはその後戻りを検出します。
func TestNoCustomElementVocabulary(t *testing.T) {
	for el := range AllowedVocabulary() {
		if strings.HasPrefix(el, "m-") || strings.Contains(el, "-") {
			t.Errorf("カスタム要素 %q が本文の語彙に入っています（新しい形式は vocab.go の data-type で足すこと）", el)
		}
	}
}

// TestVocabularyKeepsStructuralAndMarkers は、語彙が構造HTMLと `data-*` マーカーで
// 成り立っていることを検証します。マーカー属性はレジストリ駆動の形式
// （table/dl/section[data-type]・th/dd[data-field]・section[data-src]）の要で、
// ここが落ちると保存のたびに形式が壊れます。
func TestVocabularyKeepsStructuralAndMarkers(t *testing.T) {
	allowed := AllowedVocabulary()

	for _, el := range []string{"h1", "p", "table", "dl", "dd", "th", "section", "a"} {
		if _, ok := allowed[el]; !ok {
			t.Errorf("構造HTMLの %q が語彙から消えています", el)
		}
	}

	cases := []struct{ element, attr string }{
		{"table", "data-type"}, {"dl", "data-type"}, {"section", "data-type"},
		{"th", "data-type"}, {"th", "data-field"}, {"dd", "data-field"},
		{"section", "data-src"},
	}
	for _, c := range cases {
		if !contains(allowed[c.element], c.attr) {
			t.Errorf("マーカー属性 %s[%s] が語彙にありません: %v", c.element, c.attr, allowed[c.element])
		}
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
