package sheetmetal

// ─────────────────────────────────────────────────────────────────────────
// DXF の取り込み係——表題欄をタグにする（2026-09-03）
//
// 通信箱へ置かれた DXF を通信記録ページにし、**表題欄の「ラベル：値」を
// そのまま「名前：値」のタグ**にします（`dxf.go` が読む）。
//
// **なぜ JSON ではなくタグか**（2026-09-03 ユーザーの問い）:
//
//   - JSON は索引に載らない。タグなら `vocab_index` にそのまま入り、
//     `図面番号=X008-135-4` の**逆引きが今日すぐ効く**
//   - 「見える文字がデータの手掛かり」——JSON は人が読めず、
//     「タグは見た目のままDBに入ると信じられること」の原則から外れる
//   - ラベルは会社ごとに違う。自由語のタグはそれをそのまま受ける
//
// **図面番号は識別子だが一意ではない**（2026-09-03 ユーザー:「別の製品の図面番号が
// 一致してしまう場合もあり、その場合には、社内で識別番号を割り当てるしかありません」）。
// だから**図面番号で自動的にページを束ねません**。同一性を担うのは常にページIDで、
// 図面番号は探すための**タグ**です——衝突したときは複数出るので、どれかは人が選ぶ
// （w-cms はページごとに6桁の社内識別番号を必ず持っているので、
// 「社内で識別番号を割り当てる」は構造としては既に満たされている）。
//
// 実運用では DXF は**メールに入って来ることがほとんど**で、直接貼るときは
// 各部品のページへ貼る——どちらも「受信箱への単体ドロップ」ではありません。
// 既に添付されている DXF から表題欄を読む口は次の段（作業引き継ぎ）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"crypto/sha256"
	"encoding/hex"
	"html"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"w-cms/internal/cms"
)

func init() {
	cms.RegisterIntake(dxfIntake{}, ".dxf")
}

type dxfIntake struct{}

func (dxfIntake) Name() string         { return "dxf" }
func (dxfIntake) Extensions() []string { return []string{".dxf"} }

// SourceRef は重複検知の鍵（中身のSHA-256）を返します。図面には Message-ID に
// あたる自然な鍵が無く、**図面番号は一意ではない**ので鍵に使えません。
func (dxfIntake) SourceRef(fileName string, content []byte) (string, string, bool) {
	sum := sha256.Sum256(content)
	return cms.ContentHashTag, hex.EncodeToString(sum[:]), true
}

// OnFile は DXF を通信記録ページにします。
func (dxfIntake) OnFile(ctx *cms.IntakeContext, fileName string, content []byte) (string, string, error) {
	fields := DXFTitleBlock(ParseDXFTexts(content))

	// 題は「図面番号 図面名称」——**図面名称は重複しうる**ので番号を先に置く。
	// どちらも無ければファイル名（表題欄が空欄の構想図など）。
	title := strings.TrimSpace(strings.TrimSpace(fields["図面番号"]) + " " + strings.TrimSpace(fields["図面名称"]))
	if title == "" {
		title = strings.TrimSpace(strings.TrimSuffix(fileName, filepath.Ext(fileName)))
	}
	if title == "" {
		title = "図面"
	}

	pageID, err := ctx.CreatePage("<h1>" + html.EscapeString(title) + "</h1>")
	if err != nil {
		return "", "", err
	}
	id, href, err := ctx.SaveAttachment(pageID, ".dxf", content)
	if err != nil {
		return "", "", err
	}

	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(`<dl data-type="tags">`)
	// 表題欄の項目（**在るものだけ**。空欄の図面から値を捏造しない）。
	// 並びは名前順——DXF の要素順は図面ごとにばらばらで意味が無い。
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		writeIntakeTag(&b, k, fields[k])
	}
	writeIntakeTag(&b, "取り込み日時", time.Now().In(time.Local).Format(time.RFC3339))
	sum := sha256.Sum256(content)
	writeIntakeTag(&b, cms.ContentHashTag, hex.EncodeToString(sum[:]))
	b.WriteString("</dl>")
	b.WriteString(`<p data-id="` + html.EscapeString(id) + `">📎 <a href="` +
		html.EscapeString(href) + `" download="` + html.EscapeString(fileName) + `">` +
		html.EscapeString(fileName) + `</a></p>`)

	if err := ctx.UpdatePage(pageID, b.String()); err != nil {
		return "", "", err
	}
	return pageID, title, nil
}

// writeIntakeTag は値のあるタグだけを書きます（空欄は書かない）。
func writeIntakeTag(b *strings.Builder, name, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	b.WriteString("<dt>" + html.EscapeString(name) + "</dt><dd>" +
		html.EscapeString(strings.TrimSpace(value)) + "</dd>")
}
