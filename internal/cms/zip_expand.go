package cms

// ─────────────────────────────────────────────────────────────────────────
// ZIP添付の展開（2026-09-17）
//
// ユーザー:「ZIP本体がEMLの中に存在するのであれば、ZIPを展開しても良いと思います」。
//
// 2026-09-01 の目録（zip_list.go）は「展開はしない」を原則にしました——展開は
// 攻撃面が別枠で増えるので、要望が出たときに設計する、と。その要望が出ました。
// **メール添付の ZIP から作った部品ページで PDF が表示されない**——中のファイルには
// 本文のブロックが無く、参照（`ページID-添付ID`）が ZIP そのものを指すしかなかった
// ためです。ZIP の中のファイルを**取り込みの時点で1つずつ添付にする**と、どれも
// ブロックIDを持ち、ファイル表示・DXF照合・解析が ZIP を特別扱いしなくて済みます。
//
// ── 守り（展開で増える攻撃面と、その蓋）──
//
//   - **ZIP爆弾**: 申告サイズは自己申告。1件ごとに `LimitReader` で実読みを打ち切り、
//     **件数**（zipExpandMaxMembers）と**合計バイト**（zipExpandBudget）にも蓋をする。
//   - **Zip Slip（`../` の名前）**: 効きません。保存名はサーバー採番のID
//     （`SaveAttachment`）で、中の名前は本文のリンク文字にしか使いません。
//   - **中のZIP**: 再帰しません（1段だけ）。中の ZIP は展開せず、名前だけ知らせる。
//   - **シンボリックリンク**（Unix製ZIPにありうる）: 中身がパス文字列の偽ファイルに
//     なるので飛ばす。
//   - **拡張子**: 呼ぶ側が許可の判定（`allow`）を渡す。人が落とす口と同じ許可リストを
//     通し、外れたもの（`.exe`・`.html` 等）は ZIP の中に残す。
//   - **中身の検査**: ここではしません。呼ぶ側が `GuardUploadContent` を通すこと
//     （PDFのマジックナンバー・画像の種別一致とEXIF除去）。
//   - **暗号化・破損**: 読めなければその1件を飛ばす。ZIP全体が開けなければ error。
//
// 配信側の守り（未知の種別は `octet-stream`＋`attachment`＋`nosniff`）は
// 変わりません——展開で新しい配信経路は増えません。
//
// ── 展開しないもの ──
//
// 人が手でページへ落とした ZIP は展開しません。展開してよい根拠は「原本（.eml）が
// ZIP を丸ごと持っている」ことで、手で落とした ZIP にはその原本がありません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"strings"
)

// zipExpandMaxMembers は1つの ZIP から取り出す件数の上限です。
// 実データは最大40件（見積部品リスト＋図面一式）でした。
const zipExpandMaxMembers = 200

// zipExpandBudget は1つの ZIP から読み出してよい合計バイト数です。
// 1件の上限は添付上限（`MaxUploadBytes`）と同じで、合計はその4倍。
func zipExpandBudget() int64 { return MaxUploadBytes() * 4 }

// ZipMember は展開した ZIP の中のファイル1つです。
type ZipMember struct {
	// Name は ZIP の中のパスです（復号済み・フォルダを含む）。
	// **フォルダは落としません**——実データでは
	// `Q055-サンプル装置仕様　図面/R310-….PDF` のように
	// フォルダ名が装置名称を含むので、ファイル名だけにすると手掛かりが消えます。
	Name    string
	Content []byte
}

// ZipSkipped は展開しなかった中身と、その理由です（本文に一言で残す）。
type ZipSkipped struct {
	Name   string
	Reason string
}

// ExpandZip は ZIP の中のファイルを取り出します。
//
// allow は「この名前（拡張子）を受けるか」——nil なら全部受けます。
// 展開しなかったものは skipped に理由つきで返します（黙って落とさない）。
// ZIP として開けなければ err（呼ぶ側は ZIP をそのまま残して続ける）。
func ExpandZip(zipBytes []byte, allow func(name string) bool) (members []ZipMember, skipped []ZipSkipped, err error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, nil, errors.New("ZIPとして読めません")
	}
	perEntry := MaxUploadBytes()
	budget := zipExpandBudget()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue // フォルダはパスに含まれる
		}
		name := DecodeZipName(f.Name, f.NonUTF8)
		skip := func(reason string) { skipped = append(skipped, ZipSkipped{Name: name, Reason: reason}) }
		if f.Mode()&os.ModeSymlink != 0 {
			skip("リンクは取り出しません")
			continue
		}
		if strings.EqualFold(path.Ext(name), ".zip") {
			skip("中のZIPは展開しません")
			continue
		}
		if allow != nil && !allow(name) {
			skip("受け付けない形式")
			continue
		}
		if len(members) >= zipExpandMaxMembers {
			skip("件数の上限")
			continue
		}
		if f.UncompressedSize64 > uint64(perEntry) {
			skip("大きすぎます")
			continue
		}
		limit := perEntry
		if limit > budget {
			limit = budget
		}
		if limit <= 0 {
			skip("合計の上限")
			continue
		}
		rc, err := f.Open()
		if err != nil {
			skip("読めません（暗号化または破損）")
			continue
		}
		content, err := io.ReadAll(io.LimitReader(rc, limit+1))
		rc.Close()
		if err != nil {
			skip("読めません（暗号化または破損）")
			continue
		}
		if int64(len(content)) > limit {
			skip("大きすぎます") // 申告より大きい＝細工の疑い
			continue
		}
		budget -= int64(len(content))
		members = append(members, ZipMember{Name: name, Content: content})
	}
	return members, skipped, nil
}

// AttachmentExtAccepted は、どれかのアップロード口が受ける拡張子かを返します
// ——PDF・画像（`allowedImageExts`）・汎用の許可リスト（`attachment_extensions`）。
//
// 人が落とすときは口が3つに分かれていますが（PDF専用・画像専用・汎用）、
// 機械が受けるとき（メールの添付・ZIP の中身）は「どれかの口が受けるか」の1問で足ります。
func AttachmentExtAccepted(ext string) bool {
	ext = strings.ToLower(ext)
	return ext == ".pdf" || allowedImageExts[ext] || GenericAttachmentExts()[ext]
}
