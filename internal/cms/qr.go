package cms

// ─────────────────────────────────────────────────────────────────────────
// QRコード——ページのURLを紙とスマホの間に渡す（2026-09-10）
//
// ユーザー:「右レールのページ情報にそのページのURLを表すQRコードを入れられますか？」。
//
// ── なぜ自前で書くのか ──
//
// **外部パッケージを足せません**（[開発方針.md](../../docs/開発方針.md) §1）。
// そして CSP が strict なので、CDN の QR ライブラリも読めません（`script-src 'self'`）。
// 残るのは自前だけです——IMAP を必要な5コマンドだけ書いたのと同じ判断で、
// **要るのは1種類のQRだけ**なので全機能は要りません:
//
//   - **バイトモードだけ**（URLはASCIIだが、数字/英数モードを足しても短くなるのは
//     ほんの数バイト。分岐が増えるほうが高くつく）
//   - **誤り訂正レベルM**（15%）。印刷して現場で読む前提の標準的な選択
//   - **型番1〜10**（レベルMで最大213バイト）。URLは長くても100バイト程度で、
//     11以上が要るなら**それはURLの側がおかしい**
//
// ── 正しさをどう確かめたか ──
//
// **自作のQRは「読めない」と分かるのが現場**です。それでは遅いので、
// **独立した読み取り機（OpenCV）で実際に復号して**確かめます（`tools/qr_verify.py`）。
// 一度読めると確かめた絵は `testdata/qr/*.txt` に凍らせ、`qr_test.go` が回帰を止めます。
//
// **他所のライブラリの絵とは一致しません**——segno・qrcode・w-cms は3つとも違う絵を出し、
// どれも読めます（詰め草とマスク選択が実装ごとに割れる。詳細は `qr_test.go` の
// `TestQRMatchesGolden`）。最初は segno と1ビットずつ突き合わせる形で書き、全件
// 食い違って半日かけて原因を追いました。**絵の一致を正しさの尺度にしないこと。**
//
// 検証の道具は開発機の照合用で、**製品は何にも依存しません**。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"fmt"
	"strings"
)

// QRMatrix は出来上がったQRの白黒です（`true` が黒）。1辺は Size。
type QRMatrix struct {
	Size int
	Dark []bool // Size*Size、行優先
}

// At は (x, y) が黒かを返します。
func (m *QRMatrix) At(x, y int) bool { return m.Dark[y*m.Size+x] }

func (m *QRMatrix) set(x, y int, v bool) { m.Dark[y*m.Size+x] = v }

// qrVersionSpec は型番ごとの誤り訂正の割り付け（レベルM）です。
//
// **数は規格表そのまま**で、`total == ecPerBlock*(blocks1+blocks2) + data...` が
// 成り立つことを `qr_test.go` が確かめます（写し間違いは静かに読めないQRを作るため）。
type qrVersionSpec struct {
	total      int // 総コードワード数
	ecPerBlock int // 1ブロックあたりの誤り訂正コードワード数
	blocks1    int // 第1群のブロック数
	data1      int // 第1群のデータコードワード数／ブロック
	blocks2    int // 第2群（無ければ0）
	data2      int
}

// qrSpecs は型番1〜10（レベルM）。添字が型番（0番は使いません）。
var qrSpecs = [11]qrVersionSpec{
	{},
	{26, 10, 1, 16, 0, 0},
	{44, 16, 1, 28, 0, 0},
	{70, 26, 1, 44, 0, 0},
	{100, 18, 2, 32, 0, 0},
	{134, 24, 2, 43, 0, 0},
	{172, 16, 4, 27, 0, 0},
	{196, 18, 4, 31, 0, 0},
	{242, 22, 2, 38, 2, 39},
	{292, 22, 3, 36, 2, 37},
	{346, 26, 4, 43, 1, 44},
}

// dataCodewords はその型番で入れられるデータの総量です。
func (s qrVersionSpec) dataCodewords() int {
	return s.blocks1*s.data1 + s.blocks2*s.data2
}

// qrAlignCenters は位置合わせパターンの中心座標（型番ごと）です。
var qrAlignCenters = [11][]int{
	{}, {}, {6, 18}, {6, 22}, {6, 26}, {6, 30},
	{6, 34}, {6, 22, 38}, {6, 24, 42}, {6, 26, 46}, {6, 28, 50},
}

// ErrQRTooLong は「このURLは型番10（213バイト）に収まらない」の印です。
var ErrQRTooLong = errors.New("QRに収まりません（URLが長すぎます）")

// NewQR は文字列をQRコードにします（バイトモード・誤り訂正レベルM）。
func NewQR(text string) (*QRMatrix, error) {
	data := []byte(text)

	// ── 1. 収まる最小の型番を選ぶ ──
	version := 0
	for v := 1; v <= 10; v++ {
		// モード指示子4ビット＋文字数（型番1〜9は8ビット、10は16ビット）＋データ。
		bits := 4 + qrCountBits(v) + len(data)*8
		if (bits+7)/8 <= qrSpecs[v].dataCodewords() {
			version = v
			break
		}
	}
	if version == 0 {
		return nil, ErrQRTooLong
	}
	spec := qrSpecs[version]

	// ── 2. ビット列を組む ──
	var bs qrBits
	bs.add(0b0100, 4) // バイトモード
	bs.add(len(data), qrCountBits(version))
	for _, b := range data {
		bs.add(int(b), 8)
	}
	// 終端子は最大4ビット（残りが4未満ならその分だけ）。
	capacity := spec.dataCodewords() * 8
	if rest := capacity - bs.len(); rest < 4 {
		bs.add(0, rest)
	} else {
		bs.add(0, 4)
	}
	// バイト境界へそろえる。
	if pad := bs.len() % 8; pad != 0 {
		bs.add(0, 8-pad)
	}
	// 残りは 0xEC / 0x11 の繰り返しで埋める（規格が定めた埋め草）。
	for i := 0; bs.len() < capacity; i++ {
		if i%2 == 0 {
			bs.add(0xEC, 8)
		} else {
			bs.add(0x11, 8)
		}
	}

	// ── 3. ブロックに分けて誤り訂正を付ける ──
	words := bs.bytes()
	var dataBlocks, ecBlocks [][]byte
	pos := 0
	appendBlock := func(n int) {
		blk := words[pos : pos+n]
		pos += n
		dataBlocks = append(dataBlocks, blk)
		ecBlocks = append(ecBlocks, qrReedSolomon(blk, spec.ecPerBlock))
	}
	for i := 0; i < spec.blocks1; i++ {
		appendBlock(spec.data1)
	}
	for i := 0; i < spec.blocks2; i++ {
		appendBlock(spec.data2)
	}

	// ── 4. 交互に取り出して1本に織る ──
	//
	// **ブロックを順に並べてはいけません**——汚れが1か所に集中したとき、
	// 1ブロックが丸ごと壊れて訂正しきれなくなります。交互に置くことで
	// 傷が全ブロックへ均される、というのが誤り訂正の効き目の前提です。
	var final []byte
	maxData := spec.data1
	if spec.data2 > maxData {
		maxData = spec.data2
	}
	for i := 0; i < maxData; i++ {
		for _, blk := range dataBlocks {
			if i < len(blk) {
				final = append(final, blk[i])
			}
		}
	}
	for i := 0; i < spec.ecPerBlock; i++ {
		for _, blk := range ecBlocks {
			final = append(final, blk[i])
		}
	}

	// ── 5. 絵にする ──
	return qrDraw(version, final), nil
}

// qrCountBits はバイトモードの文字数フィールドのビット数です。
func qrCountBits(version int) int {
	if version <= 9 {
		return 8
	}
	return 16
}

// qrBits はビット列を積む小さな箱です。
type qrBits struct {
	buf []byte
	n   int // 詰んだビット数
}

func (b *qrBits) len() int { return b.n }

func (b *qrBits) add(value, bits int) {
	for i := bits - 1; i >= 0; i-- {
		if b.n%8 == 0 {
			b.buf = append(b.buf, 0)
		}
		if value&(1<<uint(i)) != 0 {
			b.buf[b.n/8] |= 1 << uint(7-b.n%8)
		}
		b.n++
	}
}

func (b *qrBits) bytes() []byte { return b.buf }

// ── GF(256) 上の演算（誤り訂正のため）────────────────────────────────
//
// 原始多項式は規格の 0x11D。対数表を1度だけ作って掛け算を足し算に落とします。

var (
	qrExp [512]byte
	qrLog [256]byte
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		qrExp[i] = byte(x)
		qrLog[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		qrExp[i] = qrExp[i-255]
	}
}

func qrMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return qrExp[int(qrLog[a])+int(qrLog[b])]
}

// qrReedSolomon はデータに対する誤り訂正コードワードを返します。
func qrReedSolomon(data []byte, ecLen int) []byte {
	// 生成多項式 (x - α^0)(x - α^1)…(x - α^(ecLen-1))
	gen := make([]byte, 1, ecLen+1)
	gen[0] = 1
	for i := 0; i < ecLen; i++ {
		gen = append(gen, 0)
		for j := len(gen) - 1; j > 0; j-- {
			gen[j] = gen[j-1] ^ qrMul(gen[j], qrExp[i])
		}
		gen[0] = qrMul(gen[0], qrExp[i])
	}

	// **並びを逆にします**——ここまでは添字0が定数項（低い次数から）ですが、
	// 下の割り算は**添字0が最高次（＝常に1）**を前提にしています。ここを揃えないと
	// データはそのまま正しく、**誤り訂正の数バイトだけが違うQR**ができます
	// ——絵は出るのに読み取り機が黙って諦める、いちばん見つけにくい壊れ方です
	// （2026-09-10 に実際に踏み、参照実装との突き合わせで見つけました）。
	for i, j := 0, len(gen)-1; i < j; i, j = i+1, j-1 {
		gen[i], gen[j] = gen[j], gen[i]
	}

	rem := make([]byte, ecLen)
	for _, d := range data {
		factor := d ^ rem[0]
		copy(rem, rem[1:])
		rem[ecLen-1] = 0
		if factor != 0 {
			for i := 0; i < ecLen; i++ {
				rem[i] ^= qrMul(gen[i+1], factor)
			}
		}
	}
	return rem
}

// ── 絵を描く ──────────────────────────────────────────────────────────

// qrDraw は機能パターンを置き、データを流し込み、いちばん見やすいマスクを選びます。
func qrDraw(version int, words []byte) *QRMatrix {
	size := version*4 + 17
	reserved := make([]bool, size*size) // 機能パターンの居場所（データを置かない）

	base := &QRMatrix{Size: size, Dark: make([]bool, size*size)}
	mark := func(x, y int, dark bool) {
		base.set(x, y, dark)
		reserved[y*size+x] = true
	}

	// 位置検出パターン（3隅）＋分離帯。
	for _, p := range [][2]int{{0, 0}, {size - 7, 0}, {0, size - 7}} {
		for dy := -1; dy <= 7; dy++ {
			for dx := -1; dx <= 7; dx++ {
				x, y := p[0]+dx, p[1]+dy
				if x < 0 || y < 0 || x >= size || y >= size {
					continue
				}
				inner := dx >= 0 && dx <= 6 && dy >= 0 && dy <= 6
				dark := false
				if inner {
					// 外枠（7x7の縁）と中央の3x3が黒。
					edge := dx == 0 || dx == 6 || dy == 0 || dy == 6
					core := dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4
					dark = edge || core
				}
				mark(x, y, dark)
			}
		}
	}

	// 位置合わせパターン（型番2以上）。位置検出と重なる3隅は置きません。
	centers := qrAlignCenters[version]
	for _, cy := range centers {
		for _, cx := range centers {
			if (cx <= 8 && cy <= 8) || (cx <= 8 && cy >= size-9) || (cx >= size-9 && cy <= 8) {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					ring := dx == -2 || dx == 2 || dy == -2 || dy == 2
					mark(cx+dx, cy+dy, ring || (dx == 0 && dy == 0))
				}
			}
		}
	}

	// タイミングパターン（6行目・6列目）。
	for i := 8; i < size-8; i++ {
		mark(6, i, i%2 == 0)
		mark(i, 6, i%2 == 0)
	}

	// 常に黒の1点。
	mark(8, size-8, true)

	// 形式情報の居場所を先に押さえます（値は後でマスクごとに入れる）。
	for i := 0; i < 9; i++ {
		if !reserved[6*size+i] || i != 6 { // タイミングは飛ばす
			if i != 6 {
				reserved[8*size+i] = true
				reserved[i*size+8] = true
			}
		}
	}
	reserved[8*size+8] = true
	for i := 0; i < 8; i++ {
		reserved[8*size+(size-1-i)] = true
		reserved[(size-1-i)*size+8] = true
	}

	// 型番情報（型番7以上）の居場所。
	if version >= 7 {
		for i := 0; i < 18; i++ {
			r, c := i/3, i%3
			reserved[r*size+(size-11+c)] = true
			reserved[(size-11+c)*size+r] = true
		}
	}

	// ── データを蛇行させて置く ──
	//
	// 右下から2列ずつ、上下に折り返しながら。**6列目は飛ばします**
	// （タイミングパターンの列なので、そこを数えると全部ずれます）。
	bitAt := func(i int) bool {
		if i/8 >= len(words) {
			return false // 余りは白（規格の「残余ビット」）
		}
		return words[i/8]&(1<<uint(7-i%8)) != 0
	}
	dataBits := make([]bool, 0, size*size)
	idx := 0
	up := true
	for right := size - 1; right > 0; right -= 2 {
		if right == 6 {
			right--
		}
		for i := 0; i < size; i++ {
			y := i
			if up {
				y = size - 1 - i
			}
			for c := 0; c < 2; c++ {
				x := right - c
				if reserved[y*size+x] {
					continue
				}
				base.set(x, y, bitAt(idx))
				dataBits = append(dataBits, bitAt(idx))
				idx++
			}
		}
		up = !up
	}
	_ = dataBits

	// ── マスクを8通り試し、罰点の低いものを採る ──
	bestMask, bestScore := 0, -1
	var best *QRMatrix
	for mask := 0; mask < 8; mask++ {
		cand := &QRMatrix{Size: size, Dark: append([]bool(nil), base.Dark...)}
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				if reserved[y*size+x] {
					continue
				}
				if qrMaskAt(mask, x, y) {
					cand.set(x, y, !cand.At(x, y))
				}
			}
		}
		qrPlaceFormat(cand, mask)
		if version >= 7 {
			qrPlaceVersion(cand, version)
		}
		if qrForcedMask >= 0 {
			if mask == qrForcedMask { return cand }
			continue
		}
		if s := qrPenalty(cand); bestScore < 0 || s < bestScore {
			bestScore, bestMask, best = s, mask, cand
		}
	}
	_ = bestMask
	return best
}

// qrForcedMask はマスクを固定するための試験用の口です（-1 で通常の選択）。
//
// **本番では触りません。** `qr_test.go` が「選んだマスクが罰点最小か」を確かめる
// ために使います——絵の一致では守れない性質だからです（実装ごとに選ぶマスクが
// 違う。理由は同ファイルの `TestQRMatchesGolden` の説明）。
var qrForcedMask = -1

// qrMaskAt はマスク条件（0〜7）を返します。
func qrMaskAt(mask, x, y int) bool {
	switch mask {
	case 0:
		return (y+x)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (y+x)%3 == 0
	case 4:
		return (y/2+x/3)%2 == 0
	case 5:
		return (y*x)%2+(y*x)%3 == 0
	case 6:
		return ((y*x)%2+(y*x)%3)%2 == 0
	default:
		return ((y+x)%2+(y*x)%3)%2 == 0
	}
}

// qrPlaceFormat は形式情報（誤り訂正レベルMとマスク番号）を2か所へ置きます。
//
// **同じ値を2か所に書くのは規格どおり**です——位置検出の周りは汚れやすいので、
// 読み取り側がどちらかを拾えれば復号を始められます。
func qrPlaceFormat(m *QRMatrix, mask int) {
	const ecM = 0b00
	bits := qrBCH(ecM<<3|mask, 5, 10, 0x537) ^ 0x5412

	size := m.Size
	// **下位ビットから数えます**（`bit(0)` が最下位）。規格の配置表がこの数え方で
	// 書かれているためで、上位から数える書き方と混ぜると**2枚目の写しだけ逆順**に
	// なります（2026-09-10 に実際に踏みました。1枚目は合っているので絵はそれらしく
	// 出て、右上と左下だけが食い違います）。
	bit := func(i int) bool { return bits>>uint(i)&1 != 0 }

	// 1枚目——左上の位置検出のまわり。
	for i := 0; i <= 5; i++ {
		m.set(8, i, bit(i))
		m.set(i, 8, bit(14-i))
	}
	m.set(8, 7, bit(6))
	m.set(8, 8, bit(7))
	m.set(7, 8, bit(8))

	// 2枚目——右上（横）と左下（縦）。
	for i := 0; i < 8; i++ {
		m.set(size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		m.set(8, size-15+i, bit(i))
	}
}

// qrPlaceVersion は型番情報（型番7以上）を左下・右上へ置きます。
func qrPlaceVersion(m *QRMatrix, version int) {
	bits := qrBCH(version, 6, 12, 0x1F25)
	size := m.Size
	for i := 0; i < 18; i++ {
		on := bits&(1<<uint(i)) != 0
		r, c := i/3, i%3
		m.set(size-11+c, r, on)
		m.set(r, size-11+c, on)
	}
}

// qrBCH は BCH 符号の検査ビットを付けた値を返します（形式情報・型番情報の共通処理）。
func qrBCH(value, dataBits, ecBits, generator int) int {
	v := value << uint(ecBits)
	for i := dataBits + ecBits - 1; i >= ecBits; i-- {
		if v&(1<<uint(i)) != 0 {
			v ^= generator << uint(i-ecBits)
		}
	}
	return value<<uint(ecBits) | v
}

// qrPenalty は「読みにくさ」を点数にします（規格の4つの規則）。
func qrPenalty(m *QRMatrix) int {
	size, score := m.Size, 0

	// 規則1: 同じ色が5つ以上並ぶ（5個で3点、以降1個ごとに1点）。
	run := func(get func(i int) bool) {
		count, prev := 1, get(0)
		for i := 1; i < size; i++ {
			cur := get(i)
			if cur == prev {
				count++
			} else {
				if count >= 5 {
					score += count - 2
				}
				count, prev = 1, cur
			}
		}
		if count >= 5 {
			score += count - 2
		}
	}
	for y := 0; y < size; y++ {
		yy := y
		run(func(i int) bool { return m.At(i, yy) })
	}
	for x := 0; x < size; x++ {
		xx := x
		run(func(i int) bool { return m.At(xx, i) })
	}

	// 規則2: 同じ色の 2x2 が1つにつき3点。
	for y := 0; y < size-1; y++ {
		for x := 0; x < size-1; x++ {
			c := m.At(x, y)
			if c == m.At(x+1, y) && c == m.At(x, y+1) && c == m.At(x+1, y+1) {
				score += 3
			}
		}
	}

	// 規則3: 位置検出パターンに似た並び（1:1:3:1:1 の前後に空き）が1つにつき40点。
	pattern := []bool{true, false, true, true, true, false, true, false, false, false, false}
	rev := []bool{false, false, false, false, true, false, true, true, true, false, true}
	match := func(get func(i int) bool, start int, want []bool) bool {
		for i, w := range want {
			if get(start+i) != w {
				return false
			}
		}
		return true
	}
	for y := 0; y < size; y++ {
		yy := y
		g := func(i int) bool { return m.At(i, yy) }
		for x := 0; x+11 <= size; x++ {
			if match(g, x, pattern) || match(g, x, rev) {
				score += 40
			}
		}
	}
	for x := 0; x < size; x++ {
		xx := x
		g := func(i int) bool { return m.At(xx, i) }
		for y := 0; y+11 <= size; y++ {
			if match(g, y, pattern) || match(g, y, rev) {
				score += 40
			}
		}
	}

	// 規則4: 黒の割合が50%から離れるほど点が付く（5%ごとに10点）。
	dark := 0
	for _, d := range m.Dark {
		if d {
			dark++
		}
	}
	percent := dark * 100 / (size * size)
	k := percent - 50
	if k < 0 {
		k = -k
	}
	score += (k / 5) * 10
	return score
}

// ── 出し方 ────────────────────────────────────────────────────────────

// QRSVG はQRコードをSVGにします。
//
// **SVGなのは、印刷しても潰れないため**です（QRは拡大縮小されるほど誤読が増える）。
// 黒は1つの `path` にまとめます——モジュールごとに `rect` を出すと、型番10で
// 3000個近い要素になり、右レールに常時置くには重すぎます。
//
// `quiet` は周囲の余白（モジュール数）。**4未満にしないこと**——規格が求める
// 静穏帯で、詰めると読み取り機が縁を見つけられません。
func QRSVG(m *QRMatrix, quiet int) string {
	if quiet < 4 {
		quiet = 4
	}
	side := m.Size + quiet*2

	var path strings.Builder
	for y := 0; y < m.Size; y++ {
		for x := 0; x < m.Size; x++ {
			if !m.At(x, y) {
				continue
			}
			// 横に続く黒は1本の矩形にまとめます（要素数を減らすため）。
			run := 1
			for x+run < m.Size && m.At(x+run, y) {
				run++
			}
			fmt.Fprintf(&path, "M%d %dh%dv1h-%dz", x+quiet, y+quiet, run, run)
			x += run - 1
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" `+
		`shape-rendering="crispEdges" role="img">`, side, side)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/>`, side, side)
	fmt.Fprintf(&b, `<path d="%s" fill="#000"/>`, path.String())
	b.WriteString(`</svg>`)
	return b.String()
}
