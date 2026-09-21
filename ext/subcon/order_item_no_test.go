package subcon

import (
	"reflect"
	"strings"
	"testing"
)

// **品番に何を入れるかは、書面を読まないと決まりません**（2026-09-21 ユーザー:
// 「品番には入りそうなものを入れます。…そうとも限りません。これはGeminiに
// 尋ねてはどうでしょう？」）。品番を持たず図番を品番として送ってくる客先があり、
// 一方で図番とは別に品番を持つ客先もあります。
//
// ⚠ **機械に選ばせるなら、選んだ根拠も返させます**——でないと「言い換えたこと」が
// 値からは分かりません（0c の積み残し）。

// noteOf は本文から「品番の出どころ」の1行だけを取り出します（無ければ空）。
func noteOf(t *testing.T, j *orderJudgment) string {
	t.Helper()
	body := buildOrderPageHTML("000001", "pdf001", j)
	at := strings.Index(body, "<p>解析は、")
	if at < 0 {
		return ""
	}
	end := strings.Index(body[at:], "</p>")
	return body[at : at+end+len("</p>")]
}

// TestItemNoSourceNoteShowsRewording は、**言い換えたら本文に1行残る**ことを
// 固定します。
func TestItemNoSourceNoteShowsRewording(t *testing.T) {
	j := realOrderJudgment()
	for i := range j.Items {
		j.Items[i].ItemNoSource = "図面番号"
	}

	note := noteOf(t, j)
	if note == "" {
		t.Fatalf("言い換えたのに何も残っていません")
	}
	for _, want := range []string{"図面番号", "品番"} {
		if !strings.Contains(note, want) {
			t.Errorf("注意書きに %q がありません: %s", want, note)
		}
	}
}

// TestItemNoSourceNoteSilentWhenSame は、**言い換えていなければ黙る**ことを
// 固定します。
//
// ⚠ **毎回出る注意書きは読まれなくなります**（未処理一覧で一度学んだこと）。
// ⚠ この番人は、列の見出しをパッケージ変数で引いていたときの穴も見ます——
// 変数の初期化は `init()` より先に走るので、語彙が登録される前に引くと**常に空**に
// なり、比較が素通りして**言い換えていないときも1行出ます**。
func TestItemNoSourceNoteSilentWhenSame(t *testing.T) {
	j := realOrderJudgment()
	for i := range j.Items {
		j.Items[i].ItemNoSource = "品番"
	}

	if note := noteOf(t, j); note != "" {
		t.Errorf("言い換えていないのに注意書きが出ています: %s", note)
	}
}

// TestItemNoSourceNoteSilentWhenUnclear は、**根拠が無い／揃わないときは黙る**ことを
// 固定します。
//
// 行ごとに違う列から採っているなら様式を読めていません——**1行で言えないことを
// 1行で言うと嘘になります**。そのときは原本の写しが手掛かりです。
func TestItemNoSourceNoteSilentWhenUnclear(t *testing.T) {
	t.Run("そもそも返ってこなかった", func(t *testing.T) {
		if note := noteOf(t, realOrderJudgment()); note != "" {
			t.Errorf("根拠が無いのに注意書きが出ています: %s", note)
		}
	})
	t.Run("行ごとに違う", func(t *testing.T) {
		j := realOrderJudgment()
		j.Items[0].ItemNoSource = "図面番号"
		j.Items[1].ItemNoSource = "型式"
		if note := noteOf(t, j); note != "" {
			t.Errorf("揃っていないのに1行で言い切っています: %s", note)
		}
	})
}

// TestPromptMentionsEveryJSONKey は、⚠ **頼み文とGoの構造体がずれていない**ことを
// 固定します。
//
// **片方だけ直すと、その項目はエラーにならず常に空になります**——応答に入って
// いないだけなので `json.Unmarshal` は何も言いません。気づくのは「あれ、単位が
// 入っていない」と誰かが思ったときです。
//
// ⚠ **これは「頼んだか」しか見ません**（返ってくるかは Gemini しだい）。それでも、
// **こちらの片手落ちだけ**は確実に捕まえられます。
func TestPromptMentionsEveryJSONKey(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(orderJudgment{}),
		reflect.TypeOf(orderPDFItem{}),
		reflect.TypeOf(drawingJudgment{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			tag := typ.Field(i).Tag.Get("json")
			key, _, _ := strings.Cut(tag, ",")
			if key == "" || key == "-" {
				continue // 応答から読まない欄（`SourceTable` は生から解き直す）
			}
			if !strings.Contains(orderJudgePrompt, key) {
				t.Errorf("⚠ %s.%s の鍵 %q を頼んでいません（常に空で返ります）",
					typ.Name(), typ.Field(i).Name, key)
			}
		}
	}
}
