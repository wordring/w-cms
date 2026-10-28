# 電話の道具（アロハ ND6）の置き方

2026-10-21 夜に作りました。考えたことは [【考察】通信箱の改善.md](../../docs/考察/【考察】通信箱の改善.md) §7.3 にあります。

電話の道具 `w-cms-phone` は、アロハ ND6 が USB で送ってくる電文を読み、電話に出たら w-cms の通信箱に記録を作ります。

- 着信の番号を覚えておき、受話器を上げたら記録を作ります（チャネル電話・向き受信・対応：未処理）。
- 番号から、連絡帳などの `電話番号` のタグで相手を引きます。
- 受話器を置いたら、記録に「通話終了」の時刻を書きます。
- 出る前に鳴りやんだ着信は、記録のページにしません。2026-10-28 から、w-cms の「出ていない着信」の履歴に残します（`{event:"missed"}`・鳴り始めと鳴りやみ）。
  携帯へ転送された電話も、ND6 には鳴りやんだように見えるためです。通信箱の未処理の表の行として並び、人が「記録」を押すとページになります。
- `-open` を付けると、記録をそのパソコンのブラウザで開きます（電話機のそばのパソコンで動かすとき）。

ND6 は、w-cms と同じ Ubuntu のサーバー機につなぎます（2026-10-21 利用者の答え）。下の 2 の形です。電話に出た人は、スマホやパソコンの w-cms の画面の帯で「🔕 通知を受ける」を押しておくと、出た電話の記録の通知を受け、押してその記録を開けます。

## 1. w-cms の側

秘密の設定（`w-cms.env`）に、次の2つ（別の機械から呼ぶなら3つ）を書いて、w-cms を起こし直します。

- `WCMS_MACHINE_TOKEN=<鍵>`: 16文字以上の、推し量れない文字列です。電話の道具にも同じものを渡します。
- `WCMS_MACHINE_USER=<w-cms の利用者>`: 機械が作った記録の書き手です。通信箱に書ける人にします。
  ⚠ 管理者にしないこと（鍵が漏れたときに管理者として書けてしまう）。機械専用の利用者を作り、グループに入れて、通信箱をそのグループが
  書ける mode にします（本番は 2026-10-27 から利用者「機械」・グループ「通信」・通信箱 `332`）。
  ⚠ 記録の持ち主はこの利用者になり、権限は通信箱から受け継ぎます。電話に出た人がメモを書けるよう、通信箱は
  職場の人が書ける権限にしておきます（職場の人を同じ組に入れて、組が書ける mode・例 `330`）。持ち主だけが書ける
  mode（例 `302`）のままだと、出た人がメモを書けません。
- `WCMS_MACHINE_ALLOW=<CIDR>`: 電話の道具を w-cms と別の機械で動かすときだけ。例 `192.168.1.0/24`。

## 2. Ubuntu（w-cms と同じ機械）で動かすとき

1. 組み立てます: `GOOS=linux go build -o w-cms-phone ./cmd/w-cms-phone` → `/opt/w-cms/w-cms-phone` に置く。
2. udev の決まりを置きます: `99-aloha-nd6.rules` → `/etc/udev/rules.d/`。`sudo udevadm control --reload && sudo udevadm trigger`。
   ND6 を差すと `/dev/aloha-nd6` ができ、USB の省電力が切れます。
3. 鍵を置きます: `/etc/w-cms/phone.env` に `WCMS_MACHINE_TOKEN=<鍵>`（`sudo chmod 600`）。
4. systemd に載せます: `w-cms-phone.service` → `/etc/systemd/system/`。`sudo systemctl daemon-reload && sudo systemctl enable --now w-cms-phone`。
5. 確かめます: `journalctl -u w-cms-phone -f` に「/dev/aloha-nd6 を開きました」。電話がかかってくると「着信: 番号」と出ます（記録は受話器を上げたときだけできます）。
   - 2026-10-25 に業務サーバーで、ND6 が「CON」に「ALOHA」と返すこと（USB でのやりとり）は確かめました。
   - ⚠ 以前ここに書いた「PC TEST」のボタンは、実物の ND6 に見当たりませんでした（仕様書の記憶で書いたもの）。
   - ⚠ **ND6 は、電話の線に「ナンバー・ディスプレイ」の知らせが来ていないと、着信に気づかず、液晶にも何も出しません。** 2026-10-25 は、会社の電話は鳴ったのに ND6 は何も出しませんでした（会社のコードレスにも番号が出ない＝ナンバー・ディスプレイの契約かホームゲートウェイの設定が無い）。ひかり電話のナンバー・ディスプレイを契約し、ホームゲートウェイの電話の設定で、ND6 をつないだ口のナンバー・ディスプレイを「使用する」にします。

## 3. Windows（電話機のそばのパソコン）で動かすとき（いまは使わない）

1. 製造元のドライバを入れて、デバイスマネージャーの「ポート」で ND6 の COM 番号（例 COM3）を見ます。
2. 組み立てます: `GOOS=windows go build -o w-cms-phone.exe ./cmd/w-cms-phone`。
3. 鍵を環境変数 `WCMS_MACHINE_TOKEN` に入れて、`w-cms-phone.exe -dev COM3 -base https://<w-cms の名前>:8443 -open` で動かします。
4. ログオンのときに起こすなら、スタートアップに置くか、タスク スケジューラに載せます。

## USB の電源が切れたとき

- 道具は、ポートが無くなったり読めなくなったりしたら閉じて、3秒から30秒の間隔で開き直し続けます。
- Linux では1分ごとに「CON」を送り、「ALOHA」が2分以上返らなければ開き直します。
- 道具そのものが落ちたら、systemd（`Restart=always`）が起こし直します。
- ✅ 2026-10-27 に、ナンバー・ディスプレイの契約のあと、実物の ND6 と本物の線で確かめました（着信の番号を読む・会社の電話機で出ると記録ができる・切ると「通話終了」が入る・出る前に切れた着信は記録しない）。USB の電源が切れたときの開き直しは、まだ確かめていません。電文の形は製造元の機能仕様書（アロハND6 機能仕様書 9章）に合わせて、試験で確かめました（`cmd/w-cms-phone/frame_test.go`）。

## 電話をかける（業務サーバーのモデム・w-cms-dial）

2026-10-28 に作りました（まだ本物の線では試していない）。w-cms の通信箱の「☎ かける」で、かけ方「業務サーバーのモデムでかける」を選んだ端末は、
業務サーバーの USB モデムが番号を回します。会社の電話機の受話器を上げ、画面の「受話器を上げた」を押すと、モデムは線から離れ、そのまま電話機で話します。
押さなくても 30 秒（`-hold`）で離れます（そのとき受話器を上げていなければ、電話は切れます）。

- ⚠ ND6 は電話をかける命令を持っていません（製造元の機能仕様書 第2版）。そのため、会社の電話の線を分配器で分け、モデムと電話機（ND6）の両方につなぎます。
- モデムは FAX と同じ型（Conexant CX93001・0572:1329）にします。製造番号も同じことが多いので、**差してある USB の口**で見分けます（FAX のモデムは口 1-9 に決めてある・`deploy/fax/99-faxmodem.rules`）。
- 番号の確かめ（通信箱に書ける人だけ・料金の高い番号は管理者だけ）は w-cms がします（`ext/comm/phone_dial.go`）。道具は数字6〜15桁だけを受けます。
- 試験用（w-cms-test）は、道具のソケットの組に入らないので、本物の電話をかけられません。

モデムが届いたら:

1. 会社の電話の線（TEL1）に分配器を付け、片方を ND6（と電話機）、もう片方をモデムにつなぎます。モデムを業務サーバーの空いた USB の口に差します（⚠ FAX のモデムは抜かない・口を替えない）。
2. 新しいポートと口を見ます: `ls -l /dev/ttyACM*`（新しくできたもの・例 ttyACM1）→ `udevadm info -q path -n /dev/ttyACM1` の `…/usb1/<口>/…`（例 1-10）。
3. `99-wcms-dialer.rules` の `XXXX` を口に書き換え、行頭の `#` を外して `/etc/udev/rules.d/` に置きます。`sudo udevadm control --reload && sudo udevadm trigger` → `/dev/wcms-dialer` ができたかを見ます。`/dev/faxmodem` が前のまま ttyACM0 を指していること・`faxstat` が「Running and idle」のことも見ます。
4. 利用者を作ります: `sudo useradd --system --no-create-home --shell /usr/sbin/nologin --groups dialout wcms-dial`。
5. 道具を組み立てて置きます: `cd /opt/w-cms/prod && go build -o /tmp/w-cms-dial ./cmd/w-cms-dial && sudo install -m 0755 /tmp/w-cms-dial /opt/w-cms/w-cms-dial`。
6. `w-cms-dial.service` を `/etc/systemd/system/` に置き、`sudo systemctl daemon-reload && sudo systemctl enable --now w-cms-dial`。`journalctl -u w-cms-dial` に「待っています」。
7. 本番の w-cms をソケットの組に入れます: `sudo systemctl edit w-cms` で `[Service]` に `SupplementaryGroups=wcms-dial`。`/etc/w-cms/w-cms.env` に `WCMS_DIAL_SOCK=/run/w-cms-dial/dial.sock`。`sudo systemctl restart w-cms`。
8. 板金部のパソコンで通信箱を開き、「かけ方」→「業務サーバーのモデムでかける」を選び、自分の携帯へかけて確かめます。
   - 受話器を上げる前に相手が出ると、相手には無音が聞こえます（受話器を上げれば話せる）。
   - モデムが「NO DIALTONE」と答えるときは、`ATX3`（発信音を待たない）が効いていないか、線がつながっていません。
