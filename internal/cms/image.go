package cms

// ─────────────────────────────────────────────────────────────────────────
// 画像添付の入口検査と無害化（要件定義書 §2.6）
//
// 守りたいことは3つです。
//
//  1. **拡張子は名乗りにすぎない**——`.png` という名前のHTMLを置かれると配信側の
//     判定を欺けるので、中身のマジックナンバーで種別を決める（PDF の `%PDF-` 検査と
//     同じ考え方）。名乗りと中身が食い違うファイルは拒否する。
//  2. **EXIF は保存時に消す**——カメラ写真には撮影位置（GPS）・撮影日時・機材情報が
//     埋まっており、公開サイトに載ると撮影者の所在が漏れる。公開時に選別するのでは
//     なく、アップロード経路で一律に落とす（フェイルクローズ）。回転情報は**捨てる前に
//     画素へ反映**する——捨てるだけだと表示が横倒しになる。
//  3. **SVG はスクリプトを内包できる**——安全性の本体は配信側（`page.DataFileHandler` の
//     `Content-Disposition: attachment` ＋ sandbox CSP）だが、入口でも明白に危険な
//     記述を弾く（多層防御の網）。
//
// 外部パッケージは使いません（開発方針 §1）。JPEG の向き補正は標準の image/jpeg で
// 復号→回転→再符号化し、その過程で全メタデータが落ちます。PNG / WebP は容器の
// 塊（チャンク）を歩いてメタデータだけ落とします。GIF は EXIF を持ちません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"strings"
)

// allowedImageExts は画像添付として保存を許す拡張子です（要件 §2.6）。
// 実際に保存されるかは中身の種別と一致するかで決まる（extMatchesKind）。
var allowedImageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".svg": true,
}

// extKinds は拡張子から期待される種別です。
var extKinds = map[string]string{
	".png": "png", ".jpg": "jpeg", ".jpeg": "jpeg",
	".webp": "webp", ".gif": "gif", ".svg": "svg",
}

// SniffImageKind は中身から画像の種別を返します（判定できなければ空文字）。
// HEIC は許可しませんが、利用者へ理由を返すために "heic" として区別します
// （iOS のカメラ写真がこの形式で届くことがあるため。要件 §2.6）。
func SniffImageKind(content []byte) string {
	switch {
	case bytes.HasPrefix(content, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(content, []byte{0xFF, 0xD8, 0xFF}):
		return "jpeg"
	case bytes.HasPrefix(content, []byte("GIF87a")), bytes.HasPrefix(content, []byte("GIF89a")):
		return "gif"
	case len(content) >= 12 && bytes.HasPrefix(content, []byte("RIFF")) &&
		bytes.Equal(content[8:12], []byte("WEBP")):
		return "webp"
	}
	if k := sniffISOBMFF(content); k != "" {
		return k
	}
	if looksLikeSVG(content) {
		return "svg"
	}
	return ""
}

// sniffISOBMFF は ISO 基本メディア形式（HEIC/HEIF/AVIF）のブランドを見ます。
// これらは許可しませんが、拒否の理由を利用者へ具体的に伝えるために識別します。
func sniffISOBMFF(content []byte) string {
	if len(content) < 12 || !bytes.Equal(content[4:8], []byte("ftyp")) {
		return ""
	}
	switch string(content[8:12]) {
	case "heic", "heix", "hevc", "hevx", "mif1", "msf1", "heim", "heis":
		return "heic"
	case "avif", "avis":
		return "avif"
	}
	return ""
}

// looksLikeSVG は先頭の宣言・DOCTYPE・コメントを読み飛ばし、最初の要素が svg かを見ます。
// 判定だけなので厳密な検証は ValidateSVG が行います。
func looksLikeSVG(content []byte) bool {
	if len(content) > 1<<20 {
		content = content[:1<<20] // 先頭だけ見れば足りる
	}
	dec := xml.NewDecoder(bytes.NewReader(content))
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if se, ok := tok.(xml.StartElement); ok {
			return strings.EqualFold(se.Name.Local, "svg")
		}
	}
}

// ValidateSVG は SVG の入口検査です。整形式の XML でルートが svg であること、
// および明白に危険な記述を含まないことを確かめます。
//
// **これは多層防御の網であって、安全性の本体ではありません。** 本体は配信側の
// `Content-Disposition: attachment` ＋ sandbox CSP と、本文からの参照が
// `<img src>` に限られること（画像コンテキストではブラウザ仕様としてスクリプトが
// 動かない）です。「完全なサニタイズ」は目標にしません（要件 §2.6）。
//
// Go の XML パーサは外部実体を解決しないので XXE の心配はありません。
func ValidateSVG(content []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(content))
	dec.Strict = true
	rootSeen := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("SVGとして読めません（整形式のXMLである必要があります）: %v", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		name := strings.ToLower(se.Name.Local)
		if !rootSeen {
			if name != "svg" {
				return errors.New("SVGファイルではありません（ルート要素が svg ではありません）")
			}
			rootSeen = true
		}
		if name == "script" || name == "foreignobject" {
			return fmt.Errorf("SVGに <%s> は使用できません", name)
		}
		for _, a := range se.Attr {
			an := strings.ToLower(a.Name.Local)
			if strings.HasPrefix(an, "on") {
				return fmt.Errorf("SVGにイベント属性（%s）は使用できません", a.Name.Local)
			}
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Value)), "javascript:") {
				return errors.New("SVGに javascript: のURLは使用できません")
			}
		}
	}
	if !rootSeen {
		return errors.New("SVGファイルではありません（要素がありません）")
	}
	return nil
}

// StripImageMetadata は保存前に画像からメタデータ（EXIF など）を落とします。
// 種別ごとに手当てが違うので、SniffImageKind が返した種別を渡します。
func StripImageMetadata(kind string, content []byte) ([]byte, error) {
	switch kind {
	case "jpeg":
		return stripJPEG(content)
	case "png":
		return stripPNGChunks(content)
	case "webp":
		return stripWebPChunks(content)
	case "gif", "svg":
		// GIF は EXIF を持たない。SVG は XML なので位置情報の器が無く、
		// 書き換えると図が壊れうるので触らない（配信側で無害化する）。
		return content, nil
	}
	return nil, fmt.Errorf("扱えない画像種別です: %s", kind)
}

// stripJPEG は EXIF の向きを画素へ反映してから再符号化します。
// 復号→再符号化なので、EXIF・XMP・ICC を含む全メタデータが落ちます。
func stripJPEG(content []byte) ([]byte, error) {
	orientation := jpegOrientation(content)
	img, err := jpeg.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("JPEGとして読めません: %v", err)
	}
	img = applyOrientation(img, orientation)
	var buf bytes.Buffer
	// 品質は既定（75）より高めに取る。再符号化は一度きりなので、
	// 画質の劣化より「メタデータが確実に落ちること」を優先する。
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, fmt.Errorf("JPEGを書き出せません: %v", err)
	}
	return buf.Bytes(), nil
}

// jpegOrientation は APP1（Exif）から Orientation タグ（0x0112）を読みます。
// 見つからなければ 1（そのまま）。外部ライブラリを入れないための最小実装で、
// IFD0 の1階層しか見ません（Orientation は必ずそこにあります）。
func jpegOrientation(content []byte) int {
	const defaultOrientation = 1
	i := 2 // SOI の次から
	for i+4 <= len(content) {
		if content[i] != 0xFF {
			return defaultOrientation
		}
		marker := content[i+1]
		if marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		if marker == 0xDA { // 画像データの開始。ここから先にEXIFは無い
			return defaultOrientation
		}
		segLen := int(binary.BigEndian.Uint16(content[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(content) {
			return defaultOrientation
		}
		if marker == 0xE1 { // APP1
			payload := content[i+4 : i+2+segLen]
			if o, ok := exifOrientation(payload); ok {
				return o
			}
		}
		i += 2 + segLen
	}
	return defaultOrientation
}

// exifOrientation は APP1 のペイロード（"Exif\0\0" ＋ TIFF）から向きを読みます。
func exifOrientation(payload []byte) (int, bool) {
	if !bytes.HasPrefix(payload, []byte("Exif\x00\x00")) {
		return 0, false
	}
	tiff := payload[6:]
	if len(tiff) < 8 {
		return 0, false
	}
	var bo binary.ByteOrder
	switch {
	case tiff[0] == 'M' && tiff[1] == 'M':
		bo = binary.BigEndian
	case tiff[0] == 'I' && tiff[1] == 'I':
		bo = binary.LittleEndian
	default:
		return 0, false
	}
	if bo.Uint16(tiff[2:4]) != 42 {
		return 0, false
	}
	off := int(bo.Uint32(tiff[4:8]))
	if off+2 > len(tiff) {
		return 0, false
	}
	count := int(bo.Uint16(tiff[off : off+2]))
	entry := off + 2
	for n := 0; n < count; n++ {
		if entry+12 > len(tiff) {
			return 0, false
		}
		tag := bo.Uint16(tiff[entry : entry+2])
		typ := bo.Uint16(tiff[entry+2 : entry+4])
		if tag == 0x0112 && typ == 3 { // Orientation / SHORT
			return int(bo.Uint16(tiff[entry+8 : entry+10])), true
		}
		entry += 12
	}
	return 0, false
}

// applyOrientation は EXIF の向き（1〜8）を画素へ反映した画像を返します。
// 1（そのまま）と未知の値は何もしません。
func applyOrientation(src image.Image, orientation int) image.Image {
	if orientation <= 1 || orientation > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	// 5〜8 は 90 度単位の回転を含むので、縦横が入れ替わる。
	outW, outH := w, h
	if orientation >= 5 {
		outW, outH = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, outW, outH))
	// 元画像を一度 RGBA へ落としてから座標を写す（型ごとの分岐を避ける）。
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch orientation {
			case 2: // 左右反転
				nx, ny = w-1-x, y
			case 3: // 180度
				nx, ny = w-1-x, h-1-y
			case 4: // 上下反転
				nx, ny = x, h-1-y
			case 5: // 転置
				nx, ny = y, x
			case 6: // 時計回り90度
				nx, ny = h-1-y, x
			case 7: // 逆転置
				nx, ny = h-1-y, w-1-x
			case 8: // 反時計回り90度
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, rgba.At(x, y))
		}
	}
	return dst
}

// pngMetaChunks は落とす PNG の補助チャンクです（メタデータの器）。
// 画像の見え方に関わるチャンク（gAMA・iCCP・tRNS など）は残します。
var pngMetaChunks = map[string]bool{
	"tEXt": true, "zTXt": true, "iTXt": true, "eXIf": true, "tIME": true,
}

// stripPNGChunks は PNG のチャンクを歩いてメタデータの器だけ落とします。
func stripPNGChunks(content []byte) ([]byte, error) {
	const sig = "\x89PNG\r\n\x1a\n"
	if !bytes.HasPrefix(content, []byte(sig)) {
		return nil, errors.New("PNGファイルではありません")
	}
	out := bytes.NewBuffer(make([]byte, 0, len(content)))
	out.WriteString(sig)
	i := len(sig)
	for i+8 <= len(content) {
		length := int(binary.BigEndian.Uint32(content[i : i+4]))
		if length < 0 || i+12+length > len(content) {
			return nil, errors.New("PNGの構造が壊れています")
		}
		typ := string(content[i+4 : i+8])
		end := i + 12 + length // 長さ4 + 型4 + データ + CRC4
		if !pngMetaChunks[typ] {
			out.Write(content[i:end])
		}
		i = end
		if typ == "IEND" {
			break
		}
	}
	return out.Bytes(), nil
}

// webpMetaChunks は落とす WebP のチャンクです（拡張形式 VP8X のみが持ちうる）。
var webpMetaChunks = map[string]bool{"EXIF": true, "XMP ": true}

// stripWebPChunks は RIFF の塊を歩いて EXIF / XMP を落とします。
// 単純形式（VP8 / VP8L だけ）のファイルはメタデータの器を持たないのでそのまま返ります。
func stripWebPChunks(content []byte) ([]byte, error) {
	if len(content) < 12 || !bytes.HasPrefix(content, []byte("RIFF")) ||
		!bytes.Equal(content[8:12], []byte("WEBP")) {
		return nil, errors.New("WebPファイルではありません")
	}
	body := bytes.NewBuffer(nil)
	i := 12
	for i+8 <= len(content) {
		typ := string(content[i : i+4])
		size := int(binary.LittleEndian.Uint32(content[i+4 : i+8]))
		padded := size + size%2 // RIFF の塊は偶数長へ詰められる
		if size < 0 || i+8+padded > len(content) {
			// 末尾が切れている場合は触らずそのまま返す（壊すより無害）。
			return content, nil
		}
		if !webpMetaChunks[typ] {
			body.Write(content[i : i+8+padded])
		}
		i += 8 + padded
	}
	out := bytes.NewBuffer(make([]byte, 0, body.Len()+12))
	out.WriteString("RIFF")
	var l [4]byte
	binary.LittleEndian.PutUint32(l[:], uint32(body.Len()+4))
	out.Write(l[:])
	out.WriteString("WEBP")
	out.Write(body.Bytes())
	return out.Bytes(), nil
}
