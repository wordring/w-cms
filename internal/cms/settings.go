package cms

// ─────────────────────────────────────────────────────────────────────────
// 設定ファイル（config/settings.json）
//
// **正本はファイル。DBには置きません。** `cms.db` は全再構築で全テーブルが消える
// 派生索引なので、設定をDBにだけ置くと設定も消えます（docs/アーキテクチャとDBスキーマ.md
// §8.2 と §9 の決定ログ D-5）。「文書（ファイル）が主」の原則の、設定への適用です。
//
// 住人は**7つ**——見出し語の辞書（`vocabulary`）・添付の上限と拡張子・
// **拡張が持ち込む節**（`extensions`）・文字の置き換え表・WebDAV に見せない題・
// WebDAV で書ける題
// （**足したらこの数も直すこと**。WebDAV の2つが抜けたまま残っていました。
// 2026-09-15 に `装置の段` が `extensions.subcon` へ出たので、3つ目を書き替えました
// ——**数は7のままなので、内訳を読まないと気づけない形**でした）。
// ユーザーの決定（2026-08-30）:「**運用中に増やしてDB再構築します**」——語を増やす
// たびに再ビルドが要らないよう、ファイルに置きます。増やしたら **DB再構築**
// （`POST /api/rebuild-db`）で読み直され、既存ページの索引にも反映されます。
//
// ── 2026-09-07 の変更: 既定値をコードから無くし、ファイルを必須にしました ──
//
// ユーザー:「コードに埋め込まれる規定を無くし、Settings.jsonを必須にし、Githubに
// 入れてはどうでしょう？」。それまでは Goコードの map が既定値を持ち、ファイルが
// 無ければそれを書き出す作りでした。やめた理由は2つです。
//
//  1. **同じことを2か所が言っていた。** コードに語を足しても、**既にファイルのある
//     環境には届きません**（ファイルの中身が既定を置き換えるため）。2026-09-06 に
//     `ref`・`datetime` を足したとき、実際にこれで参照リンクが消えかけました。
//  2. **不揃いだった。** `type_inference` だけが「置き換え」、他の3つは「未指定なら
//     既定」——どちらか分からない設定が4つ並んでいました。
//
// いまは**ファイルが唯一の正本**です。置き場所も `data/`（環境ごとの状態）から
// `config/`（製品の構成）へ移しました——ページ・DB・トークンとは種類が違うものです。
// **Git管理**なので、語を足せば `git pull` で全環境へ届きます。
//
// **秘密は入れません。** 鍵・トークンの類は環境変数（`~/OneDrive/デスクトップ/w-cms.env`）
// が持ちます。このファイルは公開リポジトリに入るので、**秘密を書けば公開されます**。
//
// 壊れたとき・無いときは**起動を止めます**。既定値で黙って埋めると、運用者が足した語が
// 消えて**集計の内容が黙って変わります**（§8.4「派生から正本へ書き戻さない」と同じ流儀）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// SettingsPath は設定ファイルの位置です（正本）。
const SettingsPath = "config/settings.json"

// Settings は config/settings.json の中身です。
//
// 項目を増やすときは、**既存のファイルを読めなくしないこと**——読み込みは
// 未知のキーを弾く（打ち間違いを黙って無視しないため）ので、新しいキーは
// 「無ければ既定値」で動くように書きます。
type Settings struct {
	// Vocabulary は**見出し語の辞書**です。1語につき1件で、型と選択肢を一緒に持ちます。
	//
	// **もとは2つの表に割れていました**（`type_inference` と `tag_enums`。
	// 2026-09-14 ユーザー:「語彙の設定に名前と型をセットで登録しては？」）。
	// 割れていたせいで **`在籍` と `取引` は選択肢だけあって型が無く**、既定の `text`
	// 扱いのままでした——選択肢を持つなら `enum` のはずなのに、そう書く場所が
	// どちらの表にも無かったのです。
	//
	// **業務ブロックの列は最初からこの形**でした（`VocabColumn` は `Label`＋`Type`＋
	// `Enum` を1件で持つ）。タグだけが2つの表に散っていたので、揃えました。
	//
	// 選択肢は**縛りではなく、見分けるための表**です（2026-09-13 ユーザー:「語彙に
	// 無いものは背景色で区別すればよいのでは？」）。ここに無い値も**そのまま書けます**
	// ——画面が色で「見慣れない値」と知らせるだけで、拒否はしません。本文は人が書くもので、
	// 編集モードで何でも打てる以上、拒否は入口でしか効かない見せかけの守りだからです
	// （語彙モデル §5.1: **検証して通知する。拒否はしない**）。
	//
	// **値で機械が分岐するものはここに置きません**（`段` はフォルダ名になり、
	// `取引：自社` は照合から外す判断に使うので、そちらは表引きで閉じます）。
	Vocabulary map[string]VocabWord `json:"vocabulary"`

	// VocabFormats は**運用者が足す表の形式**です（2026-09-20 ユーザー決定:
	// 「表の形式も settings.json から足せるようにします」）。
	//
	// ⚠ **「登録された語彙だけDBに入る」と対の決定**です（同日）。それまでは
	// 「登録されていなくても索引に載る」が運用者の抜け道でしたが、そこを塞いだので
	// **正面の道を開けました**——塞いだだけだと、**運用者が自分の表をDBに入れる道が
	// 無くなります**（「語彙とプラグインは運用者が追加できることが要件」・2026-08-26）。
	//
	// タグの語（`vocabulary`）と同じく `git pull` で全環境へ届きます。
	// ⚠ **足したらDB再構築**で効きます（`vocabulary` と同じ）。
	//
	// ⚠ **コードの宣言と同じ名前は書けません**（`RegisterVocab` が二重登録で落とすのと
	// 同じ理由——どちらが効いているのか画面から分からず、列の型だけが静かに変わる）。
	VocabFormats []VocabDef `json:"vocab_formats,omitempty"`

	// MaxUploadMiB は添付1件あたりの上限（MiB）です。0（未指定）なら既定の32
	// （「サイズ上限32MiBは設定で変えられるように」——2026-08-31 ユーザー決定）。
	MaxUploadMiB int `json:"max_upload_mib,omitempty"`

	// VersionRetentionYears は版を残す年限です。**0（未指定）なら消しません**
	// （2026-09-23 ユーザー決定:「**5年で消す動作をやめましょう**」）。
	//
	// ⚠ **年限は1つに決められません。** 帳票の保持義務は**個人5年・法人7年・
	// 過去に問題を起こしていた法人は最大10年**で、⚠ **設置した先が個人か法人かは
	// w-cms には分かりません**（w-cms は開発元でない企業も設置するソフトウェアです）。
	//
	// ⚠ **既定を「消さない」にしたのは、間違えたときの被害が対称でないからです**:
	//
	//	短すぎた … **保持義務を破る。しかも消えた版は戻らない**
	//	長すぎた … ディスクを使う（gzip 後は小さい）
	//
	// ⚠ **後から年限を延ばしても、既に消えた版は戻りません。** だから初日から
	// 安全な側に倒しておき、**運用者が自分の事情に合わせて短くする**のが正しい向きです。
	//
	// ⚠ **それまでは 5年で消していました**——この会社は法人なので**2年足りません**でした。
	VersionRetentionYears int `json:"version_retention_years,omitempty"`

	// AttachmentExtensions は汎用の添付として受ける拡張子です（ドットつき小文字）。
	// **未指定なら1つも受けません**（既定の一覧はありません——設定が唯一の正本）。
	// いま `config/settings.json` に書いてあるのは14種です（【考察】ワンノート移行.md §3-4）。
	// 画像と .pdf は専用の口（中身検査つき）があるため、ここに書いても汎用の口は
	// 受けません。**.json は書けません**——添付の置き場は files/ に分離済みで
	// 構造上は無害だが、正本と同じ拡張子を添付に混ぜる運用そのものを断つ。
	AttachmentExtensions []string `json:"attachment_extensions,omitempty"`

	// Extensions は**拡張が持ち込む設定の節**です（節の名前 → 中身）。
	//
	// 2026-09-15 ユーザー決定（拡張の組み替え §4.2）:「拡張が自分の節を登録」。
	// それまで `machine_stages` は下請け（ext/subcon）だけが使うのに**型がコアにありました**
	// ——`DisallowUnknownFields` があるので、型をコアから外すだけで起動が止まります。
	// 中身の読み方と検査は**拡張が持ちます**（RegisterSettingsSection）。コアが知るのは
	// 「節がある」ことだけです。**語彙（vocabulary）は分けません**——型は名前で決まり、
	// 辞書は1つのほうが衝突に気づけます。
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`

	// applies は、全部の節の検査が通ったあとに効かせる反映です（読み込みの差し替えと同時）。
	// **1つでも壊れていたら何も変えない**ための2段構え（検査 → 反映）。
	applies []func()

	// CharFolding は**比較の前に置き換える文字**です（`Φ`→`φ` など）。
	//
	// ユーザー:「材料や図面にΦという記号が多く出てきます。大文字小文字などの揺れを
	// 正規化しましょう。こういった文字は、設定できた方が良いかも」（2026-09-06）。
	//
	// **NFKC では畳めません**——`Φ`（U+03A6）と `φ`（U+03C6）は大小の違いで、
	// Unicode の正規化は大小を変換しないためです。直径記号にいたっては
	// `⌀`・`Ø`・`φ` と**別系統の文字**が同じ意味で使われます。どれを同じとみなすかは
	// **業種の知識**なので、コードではなく設定に置きます。
	//
	// **未指定なら畳みません**（既定の表はありません——2026-09-07 に既定値をコードから
	// 無くしたときに `defaultCharFolding` も消えました。ここのコメントだけが
	// 「未指定なら既定」と言い続けていたので、2026-09-14 に直しています）。
	// **足したらDB再構築で効きます**——見出し語の辞書と同じ流儀。
	CharFolding map[string]string `json:"char_folding,omitempty"`

	// WebDAVHidden は WebDAV に見せないページの題です（トップ直下でなくても効きます）。
	//
	// ユーザー:「設定で見せないとするもの以外は見せて良いのでは？」（2026-09-07）。
	// **コアは業務の言葉を知りません**——`通信箱` を名前で特別扱いするコードを書くと、
	// 仕組みの側に語彙が漏れます。未指定なら**全部見せます**。
	WebDAVHidden []string `json:"webdav_hidden,omitempty"`

	// CompanyForms は社名から落とす**法人格**です（`株式会社`・`(株)`・`有限会社` …）。
	//
	// ユーザー:「見たままと裏の動作が一致してほしいので、できればページを作る時に
	// 表記ゆれを無くしたい。株式会社や有限会社、（株）などを無くした社名が連絡帳に
	// あれば、それを提案するような形で、編集者の承認を得てはどうでしょう？」（2026-09-20）。
	//
	// ⚠ **これは索引に入りません。** `char_folding` は比較値（`norm_value`）に効きますが、
	// こちらは**候補を探すときだけ**に使います。落とした形を保存すると、画面の
	// 「株式会社南北…」と裏の「南北…」が食い違い、**見たままと裏が一致しなくなる**
	// ——それを避けるのがこの設計の目的なので、保存しては本末転倒です。
	// ページに入るのは、人が承認した**連絡帳にある実物の題**です。
	//
	// **前株・後株のどちらも落とします**（`株式会社○○` も `○○(株)` も実データにある）。
	// NFKC が `㈱`→`(株)` まで開くので、表には開いたあとの形を書けば足ります。
	//
	// **未指定なら何も落としません**（既定の表はありません——設定が唯一の正本）。
	CompanyForms []string `json:"company_forms,omitempty"`

	// WebDAVReadOnly は WebDAV から**書き換えられない**ページの題です（祖先まで効きます）。
	//
	// ユーザー:「編集するCADファイルは弊社の物です。メール由来のものではありません」
	// （2026-09-07）——届いた添付は**届いた事実の証拠**なので書き換えさせません。
	// コアが `通信箱` を名前で知ってはいけないので、**題を設定に書いてもらいます**。
	WebDAVReadOnly []string `json:"webdav_readonly,omitempty"`
}

// settings は読み込み済みの設定です。**nil のあいだは設定が空のものとして振る舞います**
// ——辞書も拡張子も段も無い状態で、コード内の既定値は `MaxUploadBytes` の32MiBだけです
// （テストや、LoadSettings を呼ばない経路のため。2026-09-07 に既定値をコードから
// 無くしたとき、このコメントだけが「既定値が使われる」と言い続けていました）。
//
// 中身のマップは読み込み後**書き換えません**。差し替えは常にポインタごと行うので、
// 参照側はロックの外でマップを読んで構いません。
var (
	settingsMu sync.RWMutex
	settings   *Settings
)

// LoadSettings は設定ファイルを読み込み、以後の参照先にします。
// **起動時に無ければ止めます**（既定値をコードに持たないため。2026-09-07）。
func LoadSettings() error {
	s, err := readSettings(SettingsPath)
	if err != nil {
		// **起動時は止めます**（settings がまだ nil）。
		//
		// **読み直しのときは、いま持っているものを守ります。** DB再構築は運用中に
		// 走るので、そこでファイルが見当たらないからと語彙を空にすると、**索引が
		// 静かに痩せます**——設定を消したことより、集計が黙って変わるほうが害が大きい。
		settingsMu.RLock()
		loaded := settings != nil
		settingsMu.RUnlock()
		if loaded && errors.Is(err, os.ErrNotExist) {
			log.Printf("設定ファイル %s が見当たりません。読み込み済みの設定のまま続けます", SettingsPath)
			return nil
		}
		return err
	}
	settingsMu.Lock()
	settings = s
	charFolder = newCharFolder(s.CharFolding)
	settingsMu.Unlock()
	s.applySections()
	return nil
}

// LoadSettingsFrom は指定した場所から設定を読み込みます。
//
// **作業ディレクトリに依らずに読む**ための口です。`LoadSettings` は
// `config/settings.json` を相対で読むので、一時ディレクトリへ移る経路（テスト）や、
// 別の場所に設定を置く運用では、こちらを使います。
func LoadSettingsFrom(path string) error {
	s, err := readSettings(path)
	if err != nil {
		return err
	}
	settingsMu.Lock()
	settings = s
	charFolder = newCharFolder(s.CharFolding)
	settingsMu.Unlock()
	s.applySections()
	return nil
}

// readSettings は設定ファイルを読みます。**無ければ止めます**（2026-09-07）。
//
// 既定値で作り直す作りをやめたのは、コードとファイルが同じことを2か所で言うのを
// 断つためです。ファイルはGit管理なので、クローンすれば必ず在ります——**無いのは
// 異常**（消した・別の場所から起動した）で、黙って埋めるより止めるほうが親切です。
func readSettings(path string) (*Settings, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// **`%w` で包みます**——呼ぶ側（LoadSettings）が「無い」と「壊れている」を
		// `errors.Is` で見分けます。包み忘れると読み直しの守りが効きません。
		return nil, fmt.Errorf("設定ファイル %s がありません（Git管理の必須ファイルです。"+
			"リポジトリの根から起動しているか確かめ、消したなら git checkout で戻してください）: %w",
			path, err)
	}
	if err != nil {
		return nil, fmt.Errorf("設定ファイル %s を読めません: %w", path, err)
	}

	var s Settings
	dec := json.NewDecoder(bytes.NewReader(raw))
	// 打ち間違えたキー（vocabulaly など）を黙って無視しない。
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("設定ファイル %s の書式が不正です（手で直してください）: %w", path, err)
	}
	if err := s.validate(path); err != nil {
		return nil, err
	}
	if err := s.parseSections(path); err != nil {
		return nil, err
	}
	return &s, nil
}

// SettingsSectionParser は拡張の設定の節を読んで検査し、**効かせる関数**を返します。
//
// raw は `extensions.<名前>` の中身で、**節が無ければ nil** が来ます（そのときは空の設定を
// 効かせてください——読み直しで節を消した運用者の意図どおりにするため）。
// エラーを返すと**起動を止め、読み直しなら何も変えません**。返した関数は、ファイル全体の
// 検査が通ったあとで呼ばれます。
type SettingsSectionParser func(raw json.RawMessage) (apply func(), err error)

// settingsSections は拡張が登録した設定の節です。**`init()` の中からだけ**登録します
// （LoadSettings より前に揃っている前提なので、ロックを持ちません）。
var settingsSections = map[string]SettingsSectionParser{}

// RegisterSettingsSection は拡張の設定の節を登録します（拡張の `init()` から呼ぶ）。
//
// 節の名前は拡張の名前にします（`subcon`・`comm` など）。同じ名前を2度登録するのは
// 取り付けの誤りなので止めます。
func RegisterSettingsSection(name string, parse SettingsSectionParser) {
	if _, dup := settingsSections[name]; dup {
		panic("設定の節 " + name + " が2度登録されました")
	}
	settingsSections[name] = parse
}

// parseSections は登録された節を全部検査し、反映を集めます。
//
// **載っていない拡張の節は止めずに読み飛ばします**——同じ `config/settings.json` を
// `-tags minimal`（素の w-cms）でも使えるようにするためです。打ち間違えた節の名前も
// ここへ落ちるので、黙らずにログへ出します。
func (s *Settings) parseSections(path string) error {
	for name := range s.Extensions {
		if _, ok := settingsSections[name]; !ok {
			log.Printf("設定 %s の extensions.%s を読み飛ばします（この名前の拡張は、このビルドに入っていません）", path, name)
		}
	}
	names := make([]string, 0, len(settingsSections))
	for name := range settingsSections {
		names = append(names, name)
	}
	sort.Strings(names) // エラーの出る順を毎回同じにする
	for _, name := range names {
		apply, err := settingsSections[name](s.Extensions[name])
		if err != nil {
			return fmt.Errorf("%s: extensions.%s: %w", path, name, err)
		}
		if apply != nil {
			s.applies = append(s.applies, apply)
		}
	}
	return nil
}

// applySections は拡張の節を効かせます（設定の差し替えの直後に呼ぶ）。
func (s *Settings) applySections() {
	// ⚠ **設定は読み直されることがあります**（DB再構築が `LoadSettings` を通る）。
	// 毎回 `RegisterVocab` を呼ぶと**二重登録でその場で落ちます**ので、
	// 「設定から来たぶんだけ入れ替える」口を通します。
	SetSettingsVocabFormats(s.VocabFormats)
	for _, apply := range s.applies {
		apply()
	}
}

// validate は設定の中身を検査します。**不正なら止めます**——読み飛ばすと、
// 書いたつもりの語が効かないまま集計だけが変わります。
func (s Settings) validate(path string) error {
	// ⚠ **形式の検査は語の前に**——名前が空や重複のまま先へ進むと、
	// どの宣言が効いているのか分からなくなります。
	seen := map[string]bool{}
	for i, d := range s.VocabFormats {
		if strings.TrimSpace(d.Type) == "" {
			return fmt.Errorf("%s: vocab_formats[%d] に形式名（type）がありません", path, i)
		}
		if strings.TrimSpace(d.DisplayName) == "" {
			return fmt.Errorf("%s: vocab_formats[%d]（%s）に表示名がありません"+
				"——**見える文字が形式を宣言する**ので、caption や機能見出しに使う名前が要ります",
				path, i, d.Type)
		}
		if seen[d.Type] {
			return fmt.Errorf("%s: vocab_formats に形式名 %q が2つあります", path, d.Type)
		}
		seen[d.Type] = true
		// ⚠ **コードの宣言だけを見ます**（設定由来のぶんは除く）。素朴に
		// `VocabDefByType` で見ると、**読み直しのとき前回の自分と衝突**します
		// ——実データのDB再構築が HTTP 500 で落ちて気づきました。
		if codeDeclaredVocabType(d.Type) {
			return fmt.Errorf("%s: 形式名 %q はコードが既に宣言しています"+
				"（別の名前にしてください。同じ名前だと、どちらが効いているのか画面から分かりません）",
				path, d.Type)
		}
		for j, c := range d.Columns {
			if strings.TrimSpace(c.Label) == "" {
				return fmt.Errorf("%s: vocab_formats[%d]（%s）の %d 列目に見出しがありません",
					path, i, d.Type, j)
			}
			if !validColumnTypes[c.Type] {
				return fmt.Errorf("%s: vocab_formats[%d]（%s）の列 %q の型 %q は使えません（使えるのは %s）",
					path, i, d.Type, c.Label, c.Type, strings.Join(validColumnTypeNames(), " / "))
			}
		}
	}
	for word, w := range s.Vocabulary {
		if strings.TrimSpace(word) == "" {
			return fmt.Errorf("%s: vocabulary に空の見出し語があります", path)
		}
		if !validColumnTypes[w.Type] {
			// **使える型はここに書き写しません**——写すと型を足した日に片方が古くなります
			// （2026-09-13 に `datetime`・`ref`・`email` が抜けたまま残っていました）。
			return fmt.Errorf("%s: vocabulary の %q に未知の列型 %q があります（使えるのは %s）",
				path, word, w.Type, strings.Join(validColumnTypeNames(), " / "))
		}
		// **選択肢を持てるのは enum だけ**です。ほかの型に書いてあったら、書いた人は
		// 効くつもりでいます——黙って無視すると、画面の色分けが出ないことに気づけません。
		if len(w.Values) > 0 && w.Type != ColEnum {
			return fmt.Errorf("%s: vocabulary の %q は型が %q なのに選択肢があります"+
				"（選択肢を持てるのは %q だけです）", path, word, w.Type, ColEnum)
		}
		if w.Type == ColEnum && len(w.Values) == 0 {
			return fmt.Errorf("%s: vocabulary の %q が %q なのに選択肢がありません", path, word, ColEnum)
		}
		seen := map[string]bool{}
		for _, v := range w.Values {
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("%s: vocabulary の %q に空の選択肢があります", path, word)
			}
			if seen[v] {
				return fmt.Errorf("%s: vocabulary の %q に選択肢 %q が重複しています", path, word, v)
			}
			seen[v] = true
		}
	}
	if s.MaxUploadMiB < 0 {
		return fmt.Errorf("%s: max_upload_mib が負です", path)
	}
	for from, to := range s.CharFolding {
		if strings.TrimSpace(from) == "" {
			return fmt.Errorf("%s: char_folding に空の置き換え元があります", path)
		}
		if from == to {
			return fmt.Errorf("%s: char_folding の %q が自分自身への置き換えになっています", path, from)
		}
		// **置き換え先が別の置き換え元だと、順序で結果が変わります**（`Φ`→`φ`→`f` の類）。
		// 書いた時点で断るほうが、あとで「なぜか畳まれない」を追うより安いです。
		if _, chained := s.CharFolding[to]; chained {
			return fmt.Errorf("%s: char_folding の %q → %q は、さらに置き換えられる文字を指しています", path, from, to)
		}
	}
	for _, ext := range s.AttachmentExtensions {
		e := strings.ToLower(strings.TrimSpace(ext))
		if !strings.HasPrefix(e, ".") || len(e) < 2 {
			return fmt.Errorf("%s: attachment_extensions の %q はドットつき拡張子（例 .dxf）で書いてください", path, ext)
		}
		if e == ".json" {
			return fmt.Errorf("%s: attachment_extensions に .json は書けません（正本と同じ拡張子を添付に混ぜない）", path)
		}
	}
	return nil
}

// activeTypeInference はいま効いている推論辞書を返します。
// 返したマップは書き換えないこと（参照側が共有しています）。
func activeTypeInference() map[string]ColumnType {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings == nil {
		return nil
	}
	out := make(map[string]ColumnType, len(settings.Vocabulary))
	for word, w := range settings.Vocabulary {
		out[word] = w.Type
	}
	return out
}

// VocabularyDict は見出し語の辞書の写しを返します（`/api/tag-schema` が配ります）。
// エディタは**サーバーと同じ1つの辞書**で型を解決し、選択肢の外の値を薄黄で知らせます
// （形式知識の3原則の1: エディタに手書きの語彙を置かない——語彙モデル §7）。
//
// **写しを返します**——`Values` まで複製するので、受け取った側が書き換えても
// 設定は壊れません。
func VocabularyDict() map[string]VocabWord {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	out := map[string]VocabWord{}
	if settings == nil {
		return out
	}
	for word, w := range settings.Vocabulary {
		out[word] = VocabWord{Type: w.Type, Values: append([]string(nil), w.Values...)}
	}
	return out
}

// MaxUploadBytes は設定の添付1件あたりの上限（バイト）を返します。
//
// **ここだけコードに数を持ちます。** 設定を読む前でも上限ゼロで受けてしまわないため
// ——上限は語彙ではなく安全の柵で、「無ければ無制限」が最悪の既定になります。
func MaxUploadBytes() int64 {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings != nil && settings.MaxUploadMiB > 0 {
		return int64(settings.MaxUploadMiB) << 20
	}
	return 32 << 20
}

// GenericAttachmentExts は設定の汎用添付の拡張子集合を返します。
func GenericAttachmentExts() map[string]bool {
	settingsMu.RLock()
	var list []string
	if settings != nil {
		list = settings.AttachmentExtensions
	}
	settingsMu.RUnlock()
	out := make(map[string]bool, len(list))
	for _, e := range list {
		out[strings.ToLower(strings.TrimSpace(e))] = true
	}
	return out
}

// charFolder はいま効いている置き換え表の Replacer です。
//
// **設定が差し替わったときだけ作り直します**（Replacer の生成は安くないので、
// 正規化のたびに作るのは無駄）。settings と同じロックで守ります。
var charFolder *strings.Replacer

// activeCharFolder は置き換え用の Replacer を返します（設定が無ければ nil＝畳まない）。
func activeCharFolder() *strings.Replacer {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	return charFolder
}

// newCharFolder は表から Replacer を作ります（空なら nil）。
func newCharFolder(m map[string]string) *strings.Replacer {
	if len(m) == 0 {
		return nil
	}
	// **並びを固定します**——map の走査順は毎回変わるので、そのまま渡すと
	// 同じ設定でも実行ごとに置き換えの優先順位が変わりえます。
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(m)*2)
	for _, k := range keys {
		pairs = append(pairs, k, m[k])
	}
	return strings.NewReplacer(pairs...)
}

// WebDAVHidden は WebDAV に見せないページの題です（設定 `webdav_hidden`）。
//
// ユーザー:「通信箱も取引先もプラグインの領域ですが、クリックして開くは w-cms 本体の
// 機能なので、**設定で見せないとするもの以外は見せて良いのでは？**」（2026-09-07）。
//
// **コアは業務の言葉を知りません。** `通信箱` を特別扱いするコードを書くと、
// 仕組みの側に語彙が漏れます——見せる／見せないは運用者が名前で指定します。
// 既定は**空**（全部見せる）。
func WebDAVHidden() []string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings == nil {
		return nil
	}
	return settings.WebDAVHidden
}

// hiddenWebDAVTitles は引きやすい形（集合）で返します。
func hiddenWebDAVTitles() map[string]bool {
	list := WebDAVHidden()
	out := make(map[string]bool, len(list))
	for _, t := range list {
		if v := strings.TrimSpace(t); v != "" {
			out[v] = true
		}
	}
	return out
}

// WebDAVReadOnly は WebDAV から書き換えられないページの題です（設定 `webdav_readonly`）。
func WebDAVReadOnly() []string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings == nil {
		return nil
	}
	return settings.WebDAVReadOnly
}

// readOnlyWebDAVTitles は引きやすい形（集合）で返します。
func readOnlyWebDAVTitles() map[string]bool {
	list := WebDAVReadOnly()
	out := make(map[string]bool, len(list))
	for _, t := range list {
		if v := strings.TrimSpace(t); v != "" {
			out[v] = true
		}
	}
	return out
}

// CompanyForms は社名から落とす法人格の一覧です（設定の `company_forms`）。
//
// ⚠ **索引には効きません**——候補を探すときだけに使います（理由は
// normalize_company.go の冒頭）。既定は**空**で、何も落としません。
func CompanyForms() []string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings == nil {
		return nil
	}
	return settings.CompanyForms
}

// VersionRetention は版を残す年限を返します。**0 なら消しません**（既定）。
//
// ⚠ **うるう年で目減りしないよう 366日で数えます**——「N年は消さない」が要件なので、
// 端数は必ず**長い側**へ倒します。
func VersionRetention() time.Duration {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings == nil || settings.VersionRetentionYears <= 0 {
		return 0
	}
	return time.Duration(settings.VersionRetentionYears) * 366 * 24 * time.Hour
}
