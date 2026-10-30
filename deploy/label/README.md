# ラベルプリンター（QL-800・w-cms-label）

2026-10-30 に作りました。納品書と部材発注書のページの表の下の「🏷 現品票」で、業務サーバーにつないだブラザーの QL-800 から現品票を刷ります。
決めたことと札の中身は [【考察】現品票の印刷.md](../../docs/考察/【考察】現品票の印刷.md) にあります。

- w-cms は守りのため、機械の部品に触れません（`deploy/server/w-cms.service` の `PrivateDevices`）。プリンターの口を持つのは、この道具 `w-cms-label` だけです。
  w-cms は札を PDF で組み、mutool で白黒の絵にして（`ext/label`）、Unix ソケットで道具へ渡します。
- 道具は札の中身を知りません。絵を QL-800 の命令（ラスターコマンド）にして送り、刷り終わるのを待つだけです（`cmd/w-cms-label/raster.go`）。
- 試験用（w-cms-test）は、道具のソケットの組に入らないので、本物の札は刷れません（見本の絵は作れます）。
- ⚠ QL-800 の「Editor Lite」の灯りは消しておきます（点いていると USB メモリに見え、プリンターとして使えない）。
- ⚠ QL-800 の電源が切れると、USB の機器ごと消えます。w-cms からは起こせません。欄に「電源が切れているか、USB が抜けています」と出たら、電源を入れてから欄を開き直します。

## 置き方

1. QL-800 を業務サーバーの USB に挿し、電源を入れます。`ls -l /dev/usb/lp0` が `root lp` で見えることを確かめます（`lsusb` に `04f9:209b Brother … QL-800`）。
2. 利用者を作ります: `sudo useradd --system --no-create-home --shell /usr/sbin/nologin --groups lp wcms-label`。
3. 道具を組み立てて置きます: `cd /opt/w-cms/prod && go build -o /tmp/w-cms-label ./cmd/w-cms-label && sudo install -m 0755 /tmp/w-cms-label /opt/w-cms/w-cms-label`。
4. `w-cms-label.service` を `/etc/systemd/system/` に置き、`sudo systemctl daemon-reload && sudo systemctl enable --now w-cms-label`。`journalctl -u w-cms-label` に「待っています」。
5. 本番の w-cms をソケットの組に入れます: `sudo systemctl edit w-cms` で `[Service]` に `SupplementaryGroups=wcms-label`（電話をかける道具の組もあれば、空白で並べる）。
   `/etc/w-cms/w-cms.env` に `WCMS_LABEL_SOCK=/run/w-cms-label/label.sock`。`sudo systemctl restart w-cms`。
6. 納品書のページで「🏷 現品票」を押し、「✓ QL-800（62mm のロール）で刷れます」と出るかを見ます。見本を見てから、1枚刷ります。

## 道具を入れ替えるとき

`w-cms-update` が入れ替えるのは w-cms だけです。`cmd/w-cms-label` を変えたら、上の 3 で組み直して置き、`sudo systemctl restart w-cms-label` で起こし直します。

## 決まり

- 札は横長です（長さ 100mm × 62mm のロールの幅・`ext/toho/genpin.go` の `genpinLengthMM`）。刷るときに時計回りに 90 度回して送ります（札の左端から出る）。
- いまは長尺のロール（DK-22205 など）だけに刷れます。決まった大きさの札（DK-11209 など）は断ります。
- 1度に刷るのは 50 枚までです。刷ったことは監査に `genpin.print`（ページと枚数）で残ります。
