# 電話の道具（アロハ ND6）の置き方

2026-10-21 夜に作りました。考えたことは [【考察】通信箱の改善.md](../../docs/考察/【考察】通信箱の改善.md) §7.3 にあります。

電話の道具 `w-cms-phone` は、アロハ ND6 が USB で送ってくる電文を読み、電話に出たら w-cms の通信箱に記録を作ります。

- 着信の番号を覚えておき、受話器を上げたら記録を作ります（チャネル電話・向き受信・対応：未処理）。
- 番号から、連絡帳などの `電話番号` のタグで相手を引きます。
- 受話器を置いたら、記録に「通話終了」の時刻を書きます。
- 出る前に切れた着信は、記録にしません。
- `-open` を付けると、記録をそのパソコンのブラウザで開きます（電話機のそばのパソコンで動かすとき）。

## 1. w-cms の側

秘密の設定（`w-cms.env`）に、次の2つ（別の機械から呼ぶなら3つ）を書いて、w-cms を起こし直します。

- `WCMS_MACHINE_TOKEN=<鍵>`: 16文字以上の、推し量れない文字列です。電話の道具にも同じものを渡します。
- `WCMS_MACHINE_USER=<w-cms の利用者>`: 機械が作った記録の書き手です。通信箱に書ける人にします。
- `WCMS_MACHINE_ALLOW=<CIDR>`: 電話の道具を w-cms と別の機械で動かすときだけ。例 `192.168.1.0/24`。

## 2. Ubuntu（w-cms と同じ機械）で動かすとき

1. 組み立てます: `GOOS=linux go build -o w-cms-phone ./cmd/w-cms-phone` → `/opt/w-cms/w-cms-phone` に置く。
2. udev の決まりを置きます: `99-aloha-nd6.rules` → `/etc/udev/rules.d/`。`sudo udevadm control --reload && sudo udevadm trigger`。
   ND6 を差すと `/dev/aloha-nd6` ができ、USB の省電力が切れます。
3. 鍵を置きます: `/etc/w-cms/phone.env` に `WCMS_MACHINE_TOKEN=<鍵>`（`sudo chmod 600`）。
4. systemd に載せます: `w-cms-phone.service` → `/etc/systemd/system/`。`sudo systemctl daemon-reload && sudo systemctl enable --now w-cms-phone`。
5. 確かめます: `journalctl -u w-cms-phone -f` に「/dev/aloha-nd6 を開きました」。ND6 の「PC TEST」（機能ボタン）で、番号 `012345678900` の電文が届きます（記録は受話器を上げたときだけできるので、テストの電文では記録はできません）。

## 3. Windows（電話機のそばのパソコン）で動かすとき

1. 製造元のドライバを入れて、デバイスマネージャーの「ポート」で ND6 の COM 番号（例 COM3）を見ます。
2. 組み立てます: `GOOS=windows go build -o w-cms-phone.exe ./cmd/w-cms-phone`。
3. 鍵を環境変数 `WCMS_MACHINE_TOKEN` に入れて、`w-cms-phone.exe -dev COM3 -base https://<w-cms の名前>:8443 -open` で動かします。
4. ログオンのときに起こすなら、スタートアップに置くか、タスク スケジューラに載せます。

## USB の電源が切れたとき

- 道具は、ポートが無くなったり読めなくなったりしたら閉じて、3秒から30秒の間隔で開き直し続けます。
- Linux では1分ごとに「CON」を送り、「ALOHA」が2分以上返らなければ開き直します。
- 道具そのものが落ちたら、systemd（`Restart=always`）が起こし直します。
- ⚠ 実機では、まだ確かめていません。電文の形は製造元の機能仕様書（アロハND6 機能仕様書 9章）に合わせて、試験で確かめました（`cmd/w-cms-phone/frame_test.go`）。
