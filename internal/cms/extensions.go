package cms

// ─────────────────────────────────────────────────────────────────────────
// 載っている拡張の名簿（2026-09-15）
//
// 拡張は `init()` で自分の名前を登録します。使い道は2つ:
//
//  1. **起動ログ**——ビルドタグの付け外しは目に見えないので、「入っているつもりで
//     入っていない」を起動時に確かめられるようにします（もとは `cmd/w-cms` の
//     `loadedExtensions` で、取り付け口の `ext_*.go` が名前を足していました）。
//  2. **画面の出し分け**——`/api/tag-schema` の `extensions` で知らせます。
//     拡張の画面はコアの `assets/app.js` に居るので、**ビルドタグでサーバーから外しても
//     ボタンは残り、押すと404がエラーの通知になっていました**（「🤖 解析」「✉️ 返信」
//     「未分類へ戻す」）。組み替えの §4.1 案A（docs/【考察】通信拡張と下請け拡張への組み替え.md）。
//
// **名前は拡張が自分で名乗ります**——取り付け口ではなく拡張の中で登録するので、
// import の連鎖で入った拡張（`subcon` が連れてくる `comm/contacts`）も名簿に載ります。
// ─────────────────────────────────────────────────────────────────────────

import "sort"

// ExtensionInfo は載っている拡張1つです。
type ExtensionInfo struct {
	ID   string // 画面の出し分けに使う名前（`subcon`・`comm/mail` など。パッケージのパスと揃える）
	Name string // 起動ログに出す日本語の名前
}

// extensionRegistry は登録された拡張です。**`init()` の中からだけ**登録します
// （サーバーが動き出す前に揃っている前提なので、ロックを持ちません）。
var extensionRegistry = map[string]ExtensionInfo{}

// RegisterExtension は拡張を名簿に載せます（拡張の `init()` から呼ぶ）。
// 同じ ID を2度登録するのは取り付けの誤りなので止めます。
func RegisterExtension(id, name string) {
	if _, dup := extensionRegistry[id]; dup {
		panic("拡張 " + id + " が2度登録されました")
	}
	extensionRegistry[id] = ExtensionInfo{ID: id, Name: name}
}

// Extensions は載っている拡張を ID の順に返します。
func Extensions() []ExtensionInfo {
	out := make([]ExtensionInfo, 0, len(extensionRegistry))
	for _, e := range extensionRegistry {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ExtensionIDs は載っている拡張の ID を順に返します（**空でも nil ではなく空の配列**
// ——JSON で `null` を返すと、画面が「知らされていない」と取り違えるため）。
func ExtensionIDs() []string {
	ids := make([]string, 0, len(extensionRegistry))
	for _, e := range Extensions() {
		ids = append(ids, e.ID)
	}
	return ids
}
