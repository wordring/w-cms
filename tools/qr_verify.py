"""QRの固定データが**本当に読めるか**を、独立した読み取り機で確かめる。

    python tools/qr_verify.py

`internal/cms/testdata/qr/*.txt` は w-cms 自身が出した絵です。回帰は Go の
`TestQRMatchesGolden` が止めますが、**それは「前と同じ」しか言いません**——
最初から間違っていたら、間違ったまま凍ります。

そこでここでは **OpenCV の読み取り機で復号し、元の文字列に戻ること**を確かめます。
自作のQRで本当に守りたいのはこれだけです（絵が他所のライブラリと一致することでは
ありません。理由は internal/cms/qr_test.go の説明）。

必要なもの: `pip install opencv-python-headless numpy`
固定データを更新したら（`go test ./internal/cms -run TestQRMatchesGolden -update-qr`）、
**必ずこれを走らせること**。
"""
import io
import os
import sys

import cv2
import numpy as np

HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FIXTURES = os.path.join(HERE, 'internal', 'cms', 'testdata', 'qr')
SCALE = 8   # 1モジュールあたりの画素数（小さすぎると読み取り機が諦める）
QUIET = 4   # 静穏帯（規格の下限）


def to_image(rows):
    """0/1 の行から、静穏帯つきの白黒画像を作る。"""
    size = len(rows)
    side = (size + QUIET * 2) * SCALE
    img = np.full((side, side), 255, dtype=np.uint8)
    for y, row in enumerate(rows):
        for x, c in enumerate(row):
            if c == '1':
                y0 = (y + QUIET) * SCALE
                x0 = (x + QUIET) * SCALE
                img[y0:y0 + SCALE, x0:x0 + SCALE] = 0
    return img


def main():
    files = sorted(f for f in os.listdir(FIXTURES) if f.endswith('.txt'))
    if not files:
        print('固定データがありません:', FIXTURES)
        return 1
    detector = cv2.QRCodeDetector()
    bad = 0
    for name in files:
        with io.open(os.path.join(FIXTURES, name), encoding='utf-8') as f:
            lines = f.read().split('\n')
        want = lines[0]
        version = lines[1].strip()
        rows = [r for r in lines[2:] if r]
        decoded, _, _ = detector.detectAndDecode(to_image(rows))
        ok = decoded == want
        if not ok:
            bad += 1
        print('%s %-16s 型番%-3s %s' % (
            'OK ' if ok else 'NG ', name, version,
            '読めました' if ok else '読めた文字列=%r / 期待=%r' % (decoded, want)))
    print()
    print('全部読めました' if bad == 0 else '%d 件読めません' % bad)
    return 1 if bad else 0


if __name__ == '__main__':
    sys.exit(main())
