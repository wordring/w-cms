// Package contacts はアドレス帳です——取引の相手を1枚のページで持ち、
// メールアドレスから「誰か」を言い当てます。
//
// ── なぜ独立した拡張なのか（2026-09-12 ユーザー決定・案B）──────────────
//
// **メールプラグインの持ち物ではありません**（測って出た判定・4つの根拠は
// [docs/【考察】アドレス帳の作り直し.md] §5b）:
//
//  1. `ext/comm/mail` からアドレス帳への参照は**ゼロ**でした。使っているのは
//     `ext/subcon` の整理と、API のルート登録だけ。
//  2. 拡張どうしは import しない決まりだったので、`ext/comm/mail` に置くと
//     **業務側から呼べません**。
//  3. `-tags nomail` で整理が壊れます——`ext/subcon` は `!minimal` なので
//     nomail でも入り、部品階層は `取引先／社名／段／装置名称／図面名称` と
//     **取引先の下に生えます**。メールを外したら階層ごと消えるのは筋が通りません。
//  4. **同一性は情報源より長生きします。** FAXしか来ない客先・電話だけの客先には
//     メールアドレスがありません。CTI が来れば電話番号から同じ相手を引きます。
//
// > **メールが供給するのは連絡先。アドレス帳が持つのは同一性。使うのは業務側。**
//
// コアにも置きません。`go build -tags minimal`（**他社へ配る素の w-cms**）が
// `取引先`・`顧客`・`仕入先`・`自社` を知っている必要はないからです。
//
// ── ビルドタグは付けません ─────────────────────────────────────────
//
// **使う側が連れてきます**（`ext/subcon` が import する）。タグを増やすと
// 「板金は入れたがアドレス帳は外した」という**組み合わせが作れてしまい**、
// 整理が取引先ページを作れずに黙って壊れます。依存は import で表します。
//
// **これが拡張どうしの最初の import です**（それまで `ext/` 間の import はゼロ）。
// 解禁したのは案Bの決定そのもので、ビルドタグを増やさない限り複雑さは増えません。
//
// ── コアとの境目 ───────────────────────────────────────────────────
//
// コアはアドレス帳を知りません。差し込みは3つのフックだけです（`init` を参照）:
// 語彙（`RegisterVocab`）・計算ビュー（`RegisterView`）・アドレスの解決
// （`RegisterContactResolver`）。**どれも任意**で、積まなければ素の w-cms が
// そのまま動きます（`email` 型のタグは素のテキストで出るだけ）。
package contacts

import (
	"w-cms/internal/cms"
)

// contactsPlugin はルートを持ち込むためのプラグインです。
//
// **表もスキーマも持ちません**——アドレス帳のデータは `メールアドレス`・`取引` の
// タグで、行き先は `page_tags` です（D-1: 専用テーブルは作らない）。
type contactsPlugin struct{}

func init() { cms.Register(contactsPlugin{}) }

func (contactsPlugin) Name() string     { return "contacts" }
func (contactsPlugin) Schema() []string { return nil }
func (contactsPlugin) Tables() []string { return nil }

// **観察係ではありません**（`Triggers`/`OnElement` を持たない）——アドレス帳は
// 本文を読まず、コアが書いた `page_tags` を引くだけだからです。

// Routes はアドレス帳の口です。**main.go への直書きをやめてここへ寄せました**
// （2026-09-15）——ルートも拡張と一緒に出入りします。
func (contactsPlugin) Routes() []cms.Route {
	return []cms.Route{
		// メールから拾った相手をページにする。
		{Pattern: "/api/contacts/register", Handler: RegisterContactAPIHandler},
		// 分類の取り消し（未分類へ戻す）。**ページは消しません**——押し間違いの
		// 取り消しが別の押し間違いでページを消すことになっては割に合わないため。
		{Pattern: "/api/contacts/unfile", Handler: UnfileContactAPIHandler},
	}
}
