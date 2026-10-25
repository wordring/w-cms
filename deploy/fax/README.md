# FAX の受信（HylaFAX+・プラネックス PL-US56K2（A））の置き方

2026-10-21 夜に作りました。考えたことは [【考察】通信箱の改善.md](../../docs/考察/【考察】通信箱の改善.md) §7.4 にあります。

FAX サーバーは、w-cms と同じ Ubuntu の機械に置きます（利用者の答え）。受けた FAX を PDF にして、w-cms の通信箱へ渡します。

- w-cms は、チャネル FAX・向き受信・対応：未処理の記録を作り、FAX を添付に置いて本文に表示します。
- 送ってきた番号から、連絡帳などの `FAX番号` のタグで相手を引きます。
- 判定（🤖 解析）は、記録の箱の「🤖 解析（添付）」で人が押します。

2026-10-25 に業務サーバー（Ubuntu 26.04）で、モデムが Class 1 の FAX モデムとして動くこと（Conexant CX93001）と、受けた FAX を w-cms へ渡す道（試験用の w-cms で、見本の1枚から FAX の記録ができた）を確かめました。⚠ 本物の電話の線で FAX を受けるのは、まだです。

## 1. モデムが使えるかを確かめる

1. USB でつなぎ、`lsusb` で型が見えるか、`ls /dev/ttyACM*` でポートができるかを見ます。
2. `sudo apt install minicom` → `sudo minicom -D /dev/ttyACM0` で、`AT` に `OK`、`AT+FCLASS=?` に `1` を含む答え（例 `0,1,1.0,2,8`）が返るかを見ます。
3. 返らなければ、このモデムでは FAX を受けられません（そのときは、Linux で使える FAX モデムを考えます）。

## 2. 決まった名前と省電力

1. `99-faxmodem.rules` の `XXXX`・`YYYY` を、`lsusb` で見た番号に書き換え、行頭の `#` を外します。
2. `/etc/udev/rules.d/` に置き、`sudo udevadm control --reload && sudo udevadm trigger`。
3. モデムを差すと `/dev/faxmodem` ができ、USB の省電力が切れます。

## 3. HylaFAX+

1. `sudo apt install hylafax-server libtiff-tools curl`。
2. モデムの設定 `config.faxmodem`（このフォルダ）を `/etc/hylafax/config.faxmodem` に置きます（`sudo install -m 0644`）。
   - 対話の道具（`faxsetup`・`faxaddmodem`）は使わずに、型紙 `config/class1` にこの会社の設定（国番号・応答までの呼び出し回数・FaxRcvdCmd・速さの上限）を足したものです。
   - ⚠ Ubuntu 26.04 の HylaFAX は、設定を `/etc/hylafax` に置き、動くときだけ `/var/spool/hylafax/etc` に重ねます（`BindPaths`）。`/var/spool/hylafax/etc` に直に置いても読まれません。
3. `sudo systemctl enable --now hylafax.service faxq.service hfaxd.service`。
4. `faxgetty-faxmodem.service` を `/etc/systemd/system/` に置き、`sudo systemctl daemon-reload && sudo systemctl enable --now faxgetty-faxmodem`。
   モデムが消えたら止まり、戻ったら起きます。落ちたら起こし直します。
   ⚠ パッケージの `faxgetty@.service` とは二重に起こさないこと（こちらだけを使う）。
5. 確かめる: `faxstat -s` が「Modem faxmodem (): Running and idle」になり、`journalctl -t FaxGetty` に「MODEM CX93001…」が出ます（2026-10-25 に業務サーバーで確かめた）。

## 4. w-cms へ渡す

1. w-cms の秘密の設定 `w-cms.env` に `WCMS_MACHINE_TOKEN`（16文字以上）と `WCMS_MACHINE_USER`（機械の書き手）を書いて、w-cms を起こし直します。
2. `/etc/w-cms/fax.env` に、同じ鍵と行き先を書きます（`sudo chown root:uucp` と `sudo chmod 640`）。

   ```
   WCMS_MACHINE_TOKEN=<w-cms と同じ鍵>
   WCMS_BASE=https://localhost:8443
   ```

3. `faxrcvd-wcms` を `/var/spool/hylafax/bin/` に置き（`chmod 755`）、`/var/spool/hylafax/etc/config.faxmodem` に `FaxRcvdCmd: bin/faxrcvd-wcms` を足します。
4. 渡せなかった FAX の置き場を作ります（HylaFAX の利用者は /var/spool の下にフォルダを作れないため）:
   `sudo install -d -o uucp -g uucp -m 0750 /var/spool/w-cms-fax`。
   w-cms が中身で断った FAX（4xx）は、その下の `failed/` へ移ります（送り直しません）。
5. w-cms が応えなかった FAX を送り直す仕組みを置きます: `w-cms-fax-retry` を `/usr/local/bin/`（`chmod 755`）、`w-cms-fax-retry.service` と `.timer` を `/etc/systemd/system/` に置き、`sudo systemctl enable --now w-cms-fax-retry.timer`。

## 5. 確かめる

- ほかの FAX から送り、`journalctl -t faxrcvd-wcms` に「w-cms へ渡しました」、通信箱に「FAX … 」の記録ができるかを見ます。
- ⚠ 送ってきた番号（相手の FAX が名乗る番号）は相手が好きに書ける文字なので、スクリプトは数字と + と空白だけに絞って渡します。
