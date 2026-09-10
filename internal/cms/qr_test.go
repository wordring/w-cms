package cms

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// -update-qr を付けて走らせると、固定データを**いまの出力で書き直します**。
//
//	go test ./internal/cms -run TestQRMatchesGolden -update-qr
//	python tools/qr_verify.py      # ← 書き直したら必ずこれで読めることを確かめる
var updateQR = flag.Bool("update-qr", false, "QRの固定データを現在の出力で更新する")

// TestQRMatchesGolden は、QRの絵が**1モジュールも変わっていない**ことを固定します。
//
// ── なぜ他所のライブラリと突き合わせないのか（2026-09-10 に半日かけて分かったこと）──
//
// 最初は独立実装（Python の segno・qrcode）の絵と一致させる形で書きました。
// **一致しません。3つとも違う絵を出します。** 理由は2つあり、どちらも
// 「どちらかが壊れている」ではありませんでした:
//
//  1. **詰め草がずれる。** segno の `write_padding_bits` は `8 - (length % 8)` を
//     足すので、**ちょうどバイト境界のときゼロを1バイト余計に詰めます**。
//     バイトモードは「モード4＋文字数8＋本文8n＋終端4」で必ず境界に揃うため、
//     segno は毎回ずれます（w-cms と qrcode は一致）。
//  2. **マスクの選び方が割れる。** 罰点の計算そのものは qrcode と**8マスクとも
//     同点**で一致しますが、qrcode は採点のとき**形式情報を空白にして**測るので、
//     選ぶマスクが変わります。segno はさらに別のマスクを選びます。
//
// **どれも読めます**——読み取り機はマスク番号を形式情報から読み、本文の長さは
// 文字数フィールドが決めるので、詰め草もマスクも結果に影響しません。
// つまり**絵の一致は正しさの尺度になりません**。
//
// そこで:
//
//   - **正しさは「実際に読めること」で測ります**——`tools/qr_verify.py` が
//     OpenCV（独立した読み取り機）で固定データを復号し、元の文字列に戻ることを
//     確かめます。**これが本当に守りたい性質**です。
//   - **この試験は回帰だけを見ます**——一度読めると確かめた絵を凍らせ、
//     うっかり変わったら気づけるようにします。
func TestQRMatchesGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "qr", "*.txt"))
	if err != nil || len(files) == 0 {
		t.Fatalf("固定データが見つかりません: %v", err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".txt")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
			if len(lines) < 3 {
				t.Fatalf("固定データの形が違います: %s", f)
			}
			text := lines[0]

			got, err := NewQR(text)
			if err != nil {
				t.Fatalf("NewQR(%q): %v", text, err)
			}
			rows := make([]string, got.Size)
			for y := 0; y < got.Size; y++ {
				var line strings.Builder
				for x := 0; x < got.Size; x++ {
					if got.At(x, y) {
						line.WriteByte('1')
					} else {
						line.WriteByte('0')
					}
				}
				rows[y] = line.String()
			}

			if *updateQR {
				version := strconv.Itoa((got.Size - 17) / 4)
				out := text + "\n" + version + "\n" + strings.Join(rows, "\n") + "\n"
				if err := os.WriteFile(f, []byte(out), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("更新しました（型番%s・%dx%d）。tools/qr_verify.py で読めることを確かめること",
					version, got.Size, got.Size)
				return
			}

			wantVersion, err := strconv.Atoi(strings.TrimSpace(lines[1]))
			if err != nil {
				t.Fatalf("型番が読めません: %v", err)
			}
			if gotVersion := (got.Size - 17) / 4; gotVersion != wantVersion {
				t.Fatalf("型番が違います: got %d, want %d", gotVersion, wantVersion)
			}
			want := lines[2:]
			for len(want) > 0 && want[len(want)-1] == "" {
				want = want[:len(want)-1]
			}
			if got.Size != len(want) {
				t.Fatalf("大きさが違います: got %d, want %d", got.Size, len(want))
			}
			for y := range rows {
				if rows[y] != want[y] {
					t.Fatalf("%d行目が違います\n got: %s\nwant: %s", y, rows[y], want[y])
				}
			}
		})
	}
}

// TestQRSpecTableIsConsistent は規格表の写し間違いを見つけます。
//
// **写し間違いは静かに壊れます**——総数が合わない表でも計算は最後まで走り、
// 出来上がるのは「読めないQR」だけです。足し算が合うことだけでも確かめます。
func TestQRSpecTableIsConsistent(t *testing.T) {
	for v := 1; v <= 10; v++ {
		s := qrSpecs[v]
		blocks := s.blocks1 + s.blocks2
		if blocks == 0 {
			t.Fatalf("型番%d: ブロック数が0です", v)
		}
		if sum := s.dataCodewords() + s.ecPerBlock*blocks; sum != s.total {
			t.Errorf("型番%d: データ%d + 誤り訂正%d×%d = %d ≠ 総数%d",
				v, s.dataCodewords(), s.ecPerBlock, blocks, sum, s.total)
		}
	}
}

// TestQRPickedMaskIsLowestPenalty は、**選んだマスクがいちばん罰点の低いもの**である
// ことを固定します。
//
// 絵の一致では守れない性質なので（実装ごとに選ぶマスクが違う）、こちらで押さえます。
// 罰点の計算そのものは qrcode の `lost_point` と8マスクとも同点で一致しています
// （2026-09-10 に実測）。
func TestQRPickedMaskIsLowestPenalty(t *testing.T) {
	for _, text := range []string{
		"https://a.jp/1",
		"http://localhost:8080/010268",
		"https://wcms.example.co.jp/010268",
	} {
		best, bestScore := -1, 0
		for mask := 0; mask < 8; mask++ {
			qrForcedMask = mask
			m, err := NewQR(text)
			qrForcedMask = -1
			if err != nil {
				t.Fatal(err)
			}
			if s := qrPenalty(m); best < 0 || s < bestScore {
				best, bestScore = mask, s
			}
		}
		picked, err := NewQR(text)
		if err != nil {
			t.Fatal(err)
		}
		qrForcedMask = best
		wantMatrix, _ := NewQR(text)
		qrForcedMask = -1
		for i := range picked.Dark {
			if picked.Dark[i] != wantMatrix.Dark[i] {
				t.Fatalf("%q: 罰点最小のマスク%d（%d点）が選ばれていません", text, best, bestScore)
			}
		}
	}
}

// TestQRTooLong は、収まらないURLが**黙って切り詰められない**ことを固定します。
func TestQRTooLong(t *testing.T) {
	if _, err := NewQR(strings.Repeat("x", 300)); err != ErrQRTooLong {
		t.Fatalf("長すぎる入力を受け取ってしまいました: %v", err)
	}
}

// TestQRSVGKeepsQuietZone は、静穏帯（周囲の余白）が詰められないことを固定します。
//
// **余白を削ると読み取り機が縁を見つけられません**。「見た目が締まるから」で
// 0にされやすい場所なので、下限を試験で押さえます。
func TestQRSVGKeepsQuietZone(t *testing.T) {
	m, err := NewQR("https://example.jp/010268")
	if err != nil {
		t.Fatal(err)
	}
	svg := QRSVG(m, 0) // 0を渡しても4へ引き上げられるはず
	want := "viewBox=\"0 0 " + strconv.Itoa(m.Size+8) + " " + strconv.Itoa(m.Size+8) + "\""
	if !strings.Contains(svg, want) {
		t.Fatalf("静穏帯が確保されていません: %s を含みません\n%.120s", want, svg)
	}
	if !strings.Contains(svg, "<path") || !strings.Contains(svg, "fill=\"#000\"") {
		t.Fatal("黒モジュールが描かれていません")
	}
}
