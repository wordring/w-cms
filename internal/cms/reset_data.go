package cms

// ─────────────────────────────────────────────────────────────────────────
// データの初期化——管理画面のボタン（2026-09-16）
//
// ユーザー:「**現在は開発中なので、データの移行は必要ありません。むしろ管理ページに
// データの初期化ボタンが欲しいくらいです**」。
//
// 実際、この日の一掃は手作業でした——サーバーを止め、`data/master` を消し、
// `data/trash` を空にし、`data/cms.db*` を消して起動し直し、置き場を作り直す。
// **30分かかり、途中で「消してはいけないもの」を3つ見分ける必要がありました**。
// それをボタン1つにします。
//
// ── 消すもの・残すもの ──
//
//	消す … ページ（data/master）・ゴミ箱（data/trash）・派生索引（cms.db の中身）
//	残す … data/mail（メールのトークン）・auth.db（利用者）・data/tls（証明書）
//
// **残す3つは「消すと次の一歩が踏めなくなるもの」**です。トークンを消せばメールを
// 取り込めず、利用者を消せばログインできず、証明書を作り直すと**各PCの信頼ストアに
// 入れた指紋と食い違います**（全PCで入れ直しになる）。ここを人に判断させないために、
// **コードで線を引きます**。
//
// ── 取り消せること ──
//
// **消す前に `data/_reset-<日時>/` へ丸ごと移します。** 削除がゴミ箱への移動なのと
// 同じ規律です（§2.7④ 可逆性）——「初期化」は取り返しのつかない操作に見えますが、
// **見えるところに控えが残っていれば、間違えて押しても戻せます**。
// 控えは自動では消しません（ディスクを見て人が消す）。
//
// ── 歯止め ──
//
//   - **admin だけ**
//   - **合言葉を打つ**（`初期化`）。確認ダイアログは連打で飛ばせますが、
//     打ち込みは飛ばせません。⚠ 押し間違いで実データが消えるボタンなので、
//     ここは**わざと面倒に**してあります
//   - **監査記録**（`data.reset`）。控えの場所も残す
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// ResetConfirmWord は初期化に要る合言葉です（画面がそのまま案内に使います）。
const ResetConfirmWord = "初期化"

// ResetSummary は初期化の結果です（画面へ返す形）。
type ResetSummary struct {
	Pages      int    `json:"pages"`       // 控えへ移したページ数
	BackupDir  string `json:"backup_dir"`  // 控えの場所（取り消しに使う）
	Rebuilt    bool   `json:"rebuilt"`     // 派生索引を作り直したか
	KeptNotice string `json:"kept_notice"` // 残したもの（画面にそのまま出す）
}

// ResetData はページのデータを初期化します（**控えを取ってから**）。
//
// 呼ぶ前に admin を確かめること（`ResetDataAPIHandler` が見ています）。
func ResetData(username string) (ResetSummary, error) {
	var sum ResetSummary
	sum.KeptNotice = "メールのトークン（data/mail）・利用者（auth.db）・証明書（data/tls）は残しました。"

	master := page.MasterDir
	// 控えの場所。**`data/` の直下**に置きます——隠すと、消したことに気づいた人が
	// 探せません（ディスクを見れば分かる場所に置く）。
	stamp := time.Now().Format("20060102-150405")
	backup := filepath.Join(filepath.Dir(master), "_reset-"+stamp)

	// ⚠ **控えの中でも元の名前のまま置きます**（`<控え>/master`・`<控え>/trash`）。
	// 最初は `data/master` をそのまま控えの名前へ改名していましたが、それだと
	// 中身が `<控え>/00/…` と `<控え>/trash` の並びになり、**戻すのに1手では
	// 済みません**（シャードを1つずつ動かし、trash を別に扱うことになる）。
	// いまは `<控え>/master` を `data/master` へ動かすだけで戻せます。
	if err := os.MkdirAll(backup, 0755); err != nil {
		return sum, err
	}
	sum.BackupDir = backup

	if _, err := os.Stat(master); err == nil {
		sum.Pages = countPageDirs(master)
		if err := renameWithRetry(master, filepath.Join(backup, "master")); err != nil {
			// **失敗の跡を残しません**（2026-09-17）。空の控えだけが `data/` に積もると、
			// 「どれが本物の控えか」が読めなくなります（実際に2つ積もりました）。
			// 中身があるときは消えません（`os.Remove` はディレクトリが空のときだけ通る）。
			_ = os.Remove(backup)
			sum.BackupDir = ""
			return sum, fmt.Errorf("控えへ移せません: %w"+
				"（%s の中のファイルを、ほかのアプリが開いている可能性があります。"+
				"エディタで開いたページのファイルを閉じ、少し待ってからもう一度押してください）",
				err, master)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return sum, err
	}
	// ゴミ箱も一緒に控えへ（消したページの唯一の写しなので、捨てません）。
	trash := filepath.Join(filepath.Dir(master), "trash")
	if _, err := os.Stat(trash); err == nil {
		_ = os.Rename(trash, filepath.Join(backup, "trash"))
	}
	// **空の置き場を作り直します**——`data/master` が無いままだと、最初のページ作成で
	// 転びます（ディレクトリを作るのは書き込み側の仕事ではない）。
	if err := os.MkdirAll(master, 0755); err != nil {
		return sum, err
	}

	// 派生索引を作り直す。**正本が空になったので、索引も空にします**——ここを
	// 飛ばすと、消したはずのページが一覧や検索に出続けます（索引はDB、正本はファイル）。
	if err := RebuildDatabase(); err != nil {
		return sum, fmt.Errorf("派生索引を作り直せません: %w", err)
	}
	sum.Rebuilt = true

	auth.Audit(username, "data.reset", fmt.Sprintf("%dページ → %s", sum.Pages, sum.BackupDir))
	return sum, nil
}

// renameForReset は差し替えられる改名です（試験が失敗を注ぎ込むための継ぎ目。
// `judgeOrderPDF` と同じ流儀で、本番では常に `os.Rename`）。
var renameForReset = os.Rename

// resetRenameTries / resetRenameWait はやり直しの回数と間隔です。
const (
	resetRenameTries = 5
	resetRenameWait  = 200 * time.Millisecond
)

// renameWithRetry は**一時的に掴まれている**ことに備えて数回やり直します。
//
// ⚠ **Windows では、書いたばかりのファイルを一瞬ほかのものが掴みます**——ウイルス対策の
// 走査・検索の索引・エディタの見張りが、開いたばかりのハンドルを閉じるまでの数百ミリ秒、
// **そのフォルダの改名を断ります**（`Access is denied.`）。2026-09-17 に実データで
// **2度続けて断られ、その40秒後に同じ改名を手で試すと通りました**——つまり掴んでいたのは
// 一時的なもので、待てば済むものでした。
//
// **待てば済むものを、人に「もう一度押してください」と言わせない**のがこの関数です。
// それでも駄目なら（エディタでそのファイルを開きっぱなしなど）、呼ぶ側が理由を添えて断ります。
func renameWithRetry(from, to string) error {
	var err error
	for i := 0; i < resetRenameTries; i++ {
		if err = renameForReset(from, to); err == nil {
			return nil
		}
		time.Sleep(resetRenameWait)
	}
	return err
}

// countPageDirs は `data/master/<2桁>/<6桁>` の数を数えます（控えに何枚入ったかの案内用）。
func countPageDirs(master string) int {
	n := 0
	shards, err := os.ReadDir(master)
	if err != nil {
		return 0
	}
	for _, sh := range shards {
		if !sh.IsDir() {
			continue
		}
		pages, err := os.ReadDir(filepath.Join(master, sh.Name()))
		if err != nil {
			continue
		}
		for _, p := range pages {
			if p.IsDir() {
				n++
			}
		}
	}
	return n
}
