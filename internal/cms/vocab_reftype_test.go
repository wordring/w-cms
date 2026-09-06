package cms

import "testing"

// TestNormalizeRef は参照値の正規形を固定します（2026-09-06 に ref 型を追加）。
//
// **読めなかったら ok=false**——これが役に立ちます。`norm_value IS NULL` の参照タグを
// 数えれば、形の壊れた参照がSQL1本で出ます。
func TestNormalizeRef(t *testing.T) {
	ok := []struct{ in, want string }{
		{"010173-skr1", "010173-skr1"},
		{" 010173-skr1 ", "010173-skr1"}, // 前後の空白
		{"010173ーskr1", "010173-skr1"},  // 全角長音をハイフンとして打った
		{"010173－skr1", "010173-skr1"},  // 全角ハイフン
		{"010173", "010173"},            // ページ全体を指す形
	}
	for _, c := range ok {
		got, valid := NormalizeValue(ColRef, c.in)
		if !valid || got != c.want {
			t.Errorf("NormalizeValue(ref, %q) = %q, %v; want %q, true", c.in, got, valid, c.want)
		}
	}

	// 参照として読めない値は併記しない（生は残る）。
	for _, in := range []string{"", "260602-102-3", "abc-123", "12345", "1234567"} {
		if got, valid := NormalizeValue(ColRef, in); valid {
			t.Errorf("NormalizeValue(ref, %q) = %q, true; want ok=false", in, got)
		}
	}
}

// TestNormalizeDateTime は日時が **UTC へ揃う**ことを固定します。
//
// 並べ替えは辞書順で行われるので、帯（オフセット）の違う値が混ざると順序が静かに
// 狂います。実データはいま全件 +09:00 なので、**壊れていないだけ**でした。
func TestNormalizeDateTime(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2024-05-22T11:53:55+09:00", "2024-05-22T02:53:55Z"},
		{"2024-05-22T02:53:55Z", "2024-05-22T02:53:55Z"},
		// 帯の違う2つが同じ瞬間を指すなら、畳んだ値も同じになる
		{"2024-05-22T04:53:55+02:00", "2024-05-22T02:53:55Z"},
	}
	for _, c := range cases {
		got, ok := NormalizeValue(ColDateTime, c.in)
		if !ok || got != c.want {
			t.Errorf("NormalizeValue(datetime, %q) = %q, %v; want %q, true", c.in, got, ok, c.want)
		}
	}

	// 帯なしは**この機械のいる場所の時刻**として読む（目の前の時計を見て打つため）。
	if got, ok := NormalizeValue(ColDateTime, "2026-09-06 14:30"); !ok || got == "" {
		t.Errorf("帯なしの日時を読めていません: %q, %v", got, ok)
	}
	// 日付だけは datetime ではない（date 型の仕事）。
	if got, ok := NormalizeValue(ColDateTime, "2026-09-06"); ok {
		t.Errorf("日付だけを日時として読んでいます: %q", got)
	}
}

// TestRefTagDeclaredByDictionary は、**どのタグが参照かを推論辞書が決める**ことを
// 固定します（2026-09-06 ユーザー:「名前が『受信元』であれば値は『参照』」）。
//
// もとは Goコードの中の表（pageRefTags）でした。辞書へ移したことで、運用者が
// 自分の参照タグを足せます——ここが壊れると、その自由が黙って失われます。
func TestRefTagDeclaredByDictionary(t *testing.T) {
	for _, name := range []string{"受信元", "返信元", "相手"} {
		if InferColumnType(name) != ColRef {
			t.Errorf("%s が参照として宣言されていません: %q", name, InferColumnType(name))
		}
		if !isPageRefTag(name) {
			t.Errorf("%s がページ参照として扱われていません", name)
		}
	}
	// 宣言の無い語は参照ではない——発注書番号が参照に化けた事故（2026-09-04）の再発防止。
	for _, name := range []string{"発注書番号", "図面番号", "差出人"} {
		if isPageRefTag(name) {
			t.Errorf("%s を参照として扱っています（誤爆）", name)
		}
	}
}
