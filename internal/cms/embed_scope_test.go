package cms

import (
	"strings"
	"testing"
)

// TestEmbedURLsLimitedToDataDir は、埋め込み（img/video/audio の src 等）の宛先を
// **`/data/` 配下に限る**ことを検証します（2026-08-21 ユーザー決定）。
//
// 背景: 相対URLというだけで通していたため、本文へ `<img src="/api/logout">` を1つ
// 保存すると、そのページを開いた全員が無音でログアウトさせられました（保存型CSRF）。
// メソッドを絞って個々の口は塞ぎましたが、**本文からアプリのアドレスを叩けること自体**は
// 残っていました。宛先を添付置き場（`/data/`）に限れば、その経路が構造的に消えます。
//
// ユーザーへの確認: 「本文に貼る画像は、この w-cms へアップロードしたものだけで足りますか」
// → 「限ってよいです」（2026-08-21）。
func TestEmbedURLsLimitedToDataDir(t *testing.T) {
	keep := []struct{ name, html, want string }{
		{"添付の絶対パス", `<img src="/data/master/00/000001/a.png">`, `/data/master/00/000001/a.png`},
		{"同じページの相対パス", `<img src="a.png">`, `src="a.png"`},
	}
	for _, tc := range keep {
		t.Run("通す/"+tc.name, func(t *testing.T) {
			if got := Sanitize(tc.html); !strings.Contains(got, tc.want) {
				t.Errorf("落としてはいけない埋め込みが落ちました:\ngot  %s\nwant %s を含む", got, tc.want)
			}
		})
	}

	// **アプリのアドレスを指す埋め込みは落とす。** どれも実在する正規の口なので
	// 「存在するか」の検査では止まりません——止まるのは宛先を限ったからです。
	drop := []struct{ name, html string }{
		{"ログアウト（保存型CSRFの実例）", `<img src="/api/logout">`},
		{"ページ作成", `<img src="/api/new-page?parent=000001">`},
		{"ページ本体", `<img src="/000001">`},
		{"殻のスクリプト", `<img src="/assets/app.js">`},
		{"ルート", `<img src="/">`},
		{"data/ に見せかけた別パス", `<img src="/database/x.png">`},
		{"親をたどって外へ出る", `<img src="/data/../api/logout">`},
		{"video の src", `<video src="/api/logout"></video>`},
		{"poster", `<video poster="/api/logout"></video>`},
		// 相対の名前もサイトのルートから解かれるので、「/」を含む相対は /api/… などに届く（2026-10-26 から落とす）。
		{"相対でアプリの口", `<img src="api/logout">`},
		{"相対で殻", `<img src="assets/app.js">`},
	}
	for _, tc := range drop {
		t.Run("落とす/"+tc.name, func(t *testing.T) {
			got := Sanitize(tc.html)
			if strings.Contains(got, "/api/") || strings.Contains(got, "/assets/") ||
				strings.Contains(got, "/database/") || strings.Contains(got, `src="/"`) ||
				strings.Contains(got, `src="/000001"`) {
				t.Errorf("アプリのアドレスを指す埋め込みが残りました: %s", got)
			}
		})
	}

	// data-src（ファイル容器の配線）も同じ扱い。エンハンサがプレビューのURLに使う。
	t.Run("data-src も /data/ 配下に限る", func(t *testing.T) {
		if got := Sanitize(`<section data-type="file" data-src="/api/logout"></section>`); strings.Contains(got, "/api/") {
			t.Errorf("data-src からアプリのアドレスを指せました: %s", got)
		}
		// ページ配下のファイル名という従来の使い方は通す
		if got := Sanitize(`<section data-type="file" data-src="po.pdf"></section>`); !strings.Contains(got, `data-src="po.pdf"`) {
			t.Errorf("従来の data-src が落ちました: %s", got)
		}
	})

	// リンク（a[href]）は**別扱い**。ページ内リンクや他ページへのリンクは本文の役目なので
	// 制限しない——リンクは押さないと何も起きず、埋め込みのように自動取得されない。
	t.Run("リンクは制限しない", func(t *testing.T) {
		if got := Sanitize(`<a href="/000123">別のページ</a>`); !strings.Contains(got, `href="/000123"`) {
			t.Errorf("ページ間リンクが落ちました: %s", got)
		}
	})
}

// srcset は候補を1つずつ確かめ、逆スラッシュの宛先は通さない（2026-10-22 の見直し）。
func TestEmbedSrcsetAndBackslash(t *testing.T) {
	for _, src := range []string{
		`<video><source srcset="/data/master/00/000001/a.png 1x, /api/logout 2x"></video>`,
		`<img src="\evil.example/x.png">`,
		`<img src="\api\logout">`,
	} {
		out := Sanitize(src)
		if strings.Contains(out, "logout") || strings.Contains(out, "evil") {
			t.Errorf("⚠ 宛先が残りました: %s → %s", src, out)
		}
	}
	ok := `<video><source srcset="/data/master/00/000001/a.png 1x, /data/master/00/000001/b.png 2x"></video>`
	if out := Sanitize(ok); !strings.Contains(out, "b.png") {
		t.Errorf("添付だけの srcset が落ちました: %s", out)
	}
}

// TestRelativeEmbedNamesOnly は、相対の埋め込みは名前1つ（「/」も「?」も無い）だけを通すことを固定します（2026-10-26）。
// 相対はサイトのルートから解かれるので、api/logout は /api/logout になる。
func TestRelativeEmbedNamesOnly(t *testing.T) {
	for _, bad := range []string{"api/logout", "assets/app.js", "img/a.png", "a.png?x=1"} {
		if got := Sanitize(`<img src="` + bad + `">`); strings.Contains(got, "src=") {
			t.Errorf("⚠ 相対の %q を通しました: %s", bad, got)
		}
	}
	if got := Sanitize(`<img src="a.png">`); !strings.Contains(got, `src="a.png"`) {
		t.Errorf("名前1つの相対を落としました: %s", got)
	}
}
