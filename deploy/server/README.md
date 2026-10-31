# 業務サーバー（Ubuntu）で w-cms を動かす

2026-10-25 に、業務サーバーで試験用を動かしたときに書きました。考えたことは [【考察】職場のサーバー機と社外からの接続.md](../../docs/考察/【考察】職場のサーバー機と社外からの接続.md) §2.3・§4・§6 にあります。

## 置き場所

| もの | 場所 | 持ち主 |
|---|---|---|
| リポジトリ（試験用の画面のファイルと設定もここから読む） | `/opt/w-cms/repo` | 人の管理用の利用者（例 toho） |
| 本番の写し（本番の画面のファイルと設定。試験用で確かめた版に止める） | `/opt/w-cms/prod`（git worktree） | 同上 |
| 本番の実行ファイル | `/opt/w-cms/w-cms` | root |
| 試験用の実行ファイル | `/opt/w-cms/w-cms-test` | root |
| 本番のデータ | `/var/lib/w-cms/data` | w-cms（0700） |
| 試験用のデータ | `/var/lib/w-cms/data-test` | w-cms（0700） |
| 本番の設定 | `/etc/w-cms/w-cms.env` | root（0600） |
| 試験用の設定 | `/etc/w-cms/test.env` | root（0600） |
| 夜の控え | `/srv/backup/w-cms/<data か data-test>/`（控え用の SSD） | root |

w-cms を動かすのは、専用の利用者 `w-cms` です（ログインできない利用者）。電話の道具（`deploy/phone`）も同じ利用者で動きます。

## はじめて置くとき

```bash
sudo useradd --system --home-dir /var/lib/w-cms --shell /usr/sbin/nologin w-cms
sudo install -d -o root -g root -m 0755 /opt/w-cms
sudo install -d -o "$USER" -g "$USER" -m 0755 /opt/w-cms/repo
git clone https://github.com/wordring/w-cms.git /opt/w-cms/repo
sudo install -d -o w-cms -g w-cms -m 0700 /var/lib/w-cms /var/lib/w-cms/data-test
sudo install -d -o root -g root -m 0755 /etc/w-cms
# PDF のファイル表示を絵にする道具（2026-10-27・設定 pdf_pages・internal/cms/pdf_pages.go）。無ければ、いままでどおり PDF を埋め込む。
sudo apt install -y mupdf-tools
```

- 描いた PDF の絵は、データの置き場の `cache/pdf-pages/` に貯まります。夜の控えには入りません（消しても、次に開いたときに描き直す）。

## 組み立てて入れ替える（w-cms-update）

入れ替えは、道具 `w-cms-update` で行います（はじめに `sudo install -m 0755 /opt/w-cms/repo/deploy/server/w-cms-update /usr/local/bin/`）。

```bash
w-cms-update test       # GitHub から取ってきて組み立て、試験用を入れ替える
w-cms-update prod       # 試験用で確かめた版で、本番を入れ替える（ひとつ前の版は控える）
w-cms-update rollback   # 本番を、ひとつ前の版に戻す
```

- 入れ替えたら、応えるか（`/login` が 200）と、起動の時刻が実行ファイルより新しいか（古い実行ファイルのまま動いていないか）を、道具が確かめます。
- ⚠ 本番は、試験用のリポジトリとは別の写し `/opt/w-cms/prod`（git worktree）で動きます。画面のファイルと設定も、実行ファイルと同じ版にそろえるためです。`/opt/w-cms/prod` の中を手で書き換えないこと。
- 記録は `journalctl -t w-cms-update`。どの版が動いているかは `/opt/w-cms/w-cms-test.rev`・`/opt/w-cms/w-cms.rev`。

## 試験用の証明書

自己署名の証明書を、試験用のデータの場所に作ります。サーバーの名前と住所（例 192.168.1.100）が入ります。

```bash
cd /opt/w-cms/repo && sudo -u w-cms env WCMS_DATA_DIR=/var/lib/w-cms/data-test /opt/w-cms/w-cms-test -gencert
```

## 試験用の設定（/etc/w-cms/test.env）

```
WCMS_DATA_DIR=/var/lib/w-cms/data-test
WCMS_ADDR=127.0.0.1:9443
WCMS_TLS_CERT=/var/lib/w-cms/data-test/tls/cert.pem
WCMS_TLS_KEY=/var/lib/w-cms/data-test/tls/key.pem
WCMS_SECURE_COOKIES=1
```

- ⚠ 試験用は業務サーバーの中（127.0.0.1）だけで待ち受けます（2026-10-26。それまでは社内の LAN から `a`/`a` で本番の写しが読めた）。受信の壁の 9443 の許しも外しました。職場の PC から使うときは、SSH の中継を張ってから `https://localhost:9443` を開きます: `ssh -f -N -L 9443:127.0.0.1:9443 wcms-server`。E2E も `WCMS_BASE=https://localhost:9443` で流します。
- 最初の起動のときだけ、`WCMS_ADMIN_USER` と `WCMS_ADMIN_PASSWORD` を足して管理者を作ります。作ったら消します。
- メール・Gemini・機械専用の入口の鍵は、使うときに足します（中身はチャットやリポジトリに書かない）。

## systemd

```bash
sudo cp /opt/w-cms/repo/deploy/server/w-cms-test.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now w-cms-test
journalctl -u w-cms-test -f
```

- 落ちたら3秒で起こし直します。書けるのはデータの場所だけです（`ProtectSystem=strict`）。
- 本番の `w-cms.service` は、いまの PC から引っ越すときに起こします（2つの本番を同時に動かさない）。

## 夜の控え（控え用の SSD）

控え用の SSD を `/srv/backup` にマウントし、毎晩 2時30分に `w-cms-backup` が写します。日ごとの写しを14日ぶん、月ごとの写しを12か月ぶん残します。変わらないファイルは前の日の写しと共有するので、大きさは変わった分しか増えません。

### 控え用の SSD を整える（はじめに1回）

⚠ 選んだ SSD の中身は消えます。`lsblk` で、パーティションの無い控え用の SSD（例 `nvme0n1`）であることを確かめてから流します。

```bash
sudo parted -s /dev/nvme0n1 mklabel gpt mkpart backup ext4 0% 100%
sudo mkfs.ext4 -L w-cms-backup /dev/nvme0n1p1
sudo mkdir -p /srv/backup
echo "LABEL=w-cms-backup /srv/backup ext4 defaults,nofail,x-systemd.device-timeout=10 0 2" | sudo tee -a /etc/fstab
sudo systemctl daemon-reload && sudo mount /srv/backup
```

`nofail` なので、控え用の SSD が壊れても、サーバー機は起き上がります（控えは止まり、`w-cms-backup` がそう記録します）。

### 仕掛けを置く

```bash
sudo apt install -y sqlite3 rsync
sudo install -m 0755 /opt/w-cms/repo/deploy/server/w-cms-backup /usr/local/bin/
sudo cp /opt/w-cms/repo/deploy/server/w-cms-backup.{service,timer} /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now w-cms-backup.timer
```

- 本番を起こすまでは、`w-cms-backup.service` の `DATA`（本番のデータ）がまだ無いので、毎晩「master がありません」と記録して終わります。
- 試験用を手で控えるとき: `sudo DATA=/var/lib/w-cms/data-test DEST=/srv/backup/w-cms/data-test /usr/local/bin/w-cms-backup`
- 記録は `journalctl -t w-cms-backup`。

### 控えから戻す

1. `sudo systemctl stop w-cms`（試験用なら `w-cms-test`）。
2. いまのデータを脇へ移す（消さない）: `sudo mv /var/lib/w-cms/data /var/lib/w-cms/data.before-restore`
3. 控えを写す: `sudo cp -a /srv/backup/w-cms/data/daily/<日付> /var/lib/w-cms/data`（`latest` なら前の夜）。
4. 証明書を本番の外に置いているなら、その場所も確かめる。`sudo chown -R w-cms:w-cms /var/lib/w-cms/data`。
5. `sudo systemctl start w-cms`。cms.db・tables.db は無いので、起きるときに本文から作り直します。

## 公開の名前で開く（2026-10-26）

社外からは VPS を通って〈公開の名前〉で開きます（`deploy/vps/README.md`）。社内からも同じ〈公開の名前〉で、VPS を通らずに直に開けるようにしました。いままでの `https://〈業務サーバーの社内の住所〉:8443` も、そのまま開けます。

### 証明書を名前で使い分ける

`/etc/w-cms/w-cms.env` に、公開の名前の証明書の場所を書きます。

```text
WCMS_TLS_PUBLIC_CERT=/etc/w-cms/tls-public/public.crt
WCMS_TLS_PUBLIC_KEY=/etc/w-cms/tls-public/public.key
```

- 接続のあいさつで〈公開の名前〉を求められたらこの証明書、住所で開いた・名前を送らない・`wcms-server`（VPS の中継）ならいままでの証明書（`WCMS_TLS_CERT`）を渡します（`cmd/w-cms/cert_reload.go` の certChooser）。
- ファイルが替わったら、次の接続から読み直します（起こし直さない）。起動のときに無くても止まりません。

### 証明書を VPS から写す（w-cms-cert-pull）

VPS の Caddy が取った証明書を、トンネルの中から毎日写します。

```bash
sudo install -m 0755 deploy/server/w-cms-cert-pull /usr/local/sbin/
sudo install -m 0644 deploy/server/w-cms-cert-pull.service deploy/server/w-cms-cert-pull.timer /etc/systemd/system/
echo "PUBLIC_NAME=〈公開の名前〉" | sudo install -m 0644 /dev/stdin /etc/w-cms/cert-pull.env
sudo ssh-keygen -t ed25519 -N "" -C w-cms-cert-pull -f /root/.ssh/w-cms-cert-pull
sudo sh -c 'ssh-keyscan -t ed25519 10.8.0.1 > /root/.ssh/known_hosts-w-cms-vps'   # 指紋を VPS の画面のものと照らす
sudo systemctl daemon-reload && sudo systemctl enable --now w-cms-cert-pull.timer
```

- 鍵の公開の部分を、VPS の root の `authorized_keys` に、写すだけの命令に絞って書きます（`deploy/vps/README.md` の「証明書を業務サーバーへ写す」）。
- 名前・期限（1日以上）・鍵の組が合わなければ写しません。中身が同じなら何もしません。

### 社内の 443 番

社内（`eno1`）から 443 番に来たら、8443 番（本番）へ回します。`/etc/ufw/before.rules` の頭（`*filter` より前）に足して `sudo ufw reload`。

```text
*nat
:PREROUTING ACCEPT [0:0]
-A PREROUTING -i eno1 -p tcp --dport 443 -j REDIRECT --to-ports 8443
COMMIT
```

- 業務サーバーの中から自分の 443 番へは回りません（自分の出す通信はこの道を通らない）。確かめは社内の別の PC から。

### 社内の名前引き（dnsmasq）

業務サーバーに dnsmasq を立て、〈公開の名前〉だけを社内の住所で答えます（手本は `dnsmasq-w-cms.conf.example`）。

- ルーター（NTT のホームゲートウェイ）の「ローカルドメイン問合せテーブル」に、ドメイン名〈公開の名前〉・問合せ先に業務サーバーの IPv6 の住所を書きました（この欄は IPv6 の住所しか受け付けない）。各 PC はルーターを DNS に使ったまま、〈公開の名前〉を引いたときだけ、ルーターが業務サーバーに聞きます。
- ⚠ IPv6 の住所の前半は NTT から配られた番号です。NTT が替えると、ルーターの欄が古くなり、社内で〈公開の名前〉が引けなくなります。そのときは業務サーバーの新しい IPv6 の住所（`ip -6 addr show dev eno1` の global）に書き替えます。dnsmasq の側は `bind-dynamic` なので付いていきます。ufw の IPv6 の決まり（53 番・その /64 から）も書き替えます。
- ルーターの「DHCP で配る DNS」は替えていません（ローカルドメイン問合せテーブルで足りた。業務サーバーが止まっても、ほかの名前の引き方に響かない）。
- 「DHCP 固定 IP アドレス設定」と「LAN 側静的ルーティングエントリ」は、名前引きには使えません（2026-10-26 に確かめた）。

## 社外の控え（w-cms-offsite・2026-10-26）

夜の控え（2:30・`w-cms-backup`）のあと、その日の写し（`/srv/backup/w-cms/data/latest`）を restic で暗号化して VPS へ送ります（4:30・`w-cms-offsite.timer`）。VPS の側は `deploy/vps/README.md` の「社外の控えの受け口」。

```bash
sudo apt-get install restic
sudo install -m 0755 deploy/server/w-cms-offsite /usr/local/sbin/
sudo install -m 0644 deploy/server/w-cms-offsite.service deploy/server/w-cms-offsite.timer /etc/systemd/system/
echo "SRC=/srv/backup/w-cms/data/latest" | sudo install -m 0600 /dev/stdin /etc/w-cms/offsite.env
sudo sh -c "umask 077; head -c 48 /dev/urandom | base64 > /etc/w-cms/offsite.pass"   # 控えを開くパスワード
sudo ssh-keygen -t ed25519 -N "" -C w-cms-offsite -f /root/.ssh/w-cms-offsite      # 公開の部分を VPS の wcmsbak へ
sudo w-cms-offsite restic init
sudo systemctl daemon-reload && sudo systemctl enable --now w-cms-offsite.timer
```

- ⚠ **控えを開くパスワード（`/etc/w-cms/offsite.pass`）が無いと、控えは誰にも開けません。** 2026-10-27 から、写しは **USB メモリーと印刷した紙**の2つ（別々の場所に置く）。職場の PC のデスクトップ（OneDrive）には置かない——HP405 1台か OneDrive の口座が乗っ取られると、控えの全部が開けたため（【考察】コードの矛盾 §7.2）。USB には、業務サーバーの設定と鍵（`/etc/w-cms`・`/etc/wireguard`・`/root/.ssh`——夜の控えに入っていないもの）と構成メモも入れた（`はじめに読む.txt`）。業務サーバーが壊れたら、新しい機械にこのパスワードと restic を入れて、VPS から戻します。
- 見る: `sudo w-cms-offsite snapshots`。確かめる（20分の1を読む）: `sudo w-cms-offsite check`。
- 戻す（例）: `sudo w-cms-offsite restic restore latest --target /tmp/restore`（その日の写しの形で戻る——`deploy/server/README.md` の「控えから戻す」と同じ置き方）。
- 古い控えは自動では消しません（VPS が書き換えられなくしているため）。整理は VPS の側の注意を読んでから。

## 見張り（w-cms-watch・2026-10-26）

w-cms が応えるかを1分ごとに見て、止まったとき・戻ったときに ntfy でスマホへ知らせます（利用者:「ntfy に替えます」——UptimeRobot は登録の CAPTCHA を越えられなかった）。同じスクリプトを業務サーバーと VPS の両方に置き、互いの道を見ます。

| 置き場 | 見る道 | 気づけること |
|---|---|---|
| 業務サーバー | 社外の道（VPS の住所へ `--resolve` → Caddy → トンネル → w-cms） | VPS・Caddy・トンネルが止まった |
| VPS | Caddy → トンネル → w-cms | w-cms・業務サーバー・職場の回線や電気が止まった |

```bash
sudo install -m 0755 deploy/server/w-cms-watch /usr/local/sbin/
sudo install -m 0644 deploy/server/w-cms-watch.service deploy/server/w-cms-watch.timer /etc/systemd/system/
sudo install -m 0600 /dev/null /etc/w-cms/watch.env   # 中身は下
sudo systemctl daemon-reload && sudo systemctl enable --now w-cms-watch.timer
sudo w-cms-watch test   # 試しの知らせ
```

`/etc/w-cms/watch.env`（値に空白があれば引用符で囲む）:

```text
NTFY_SERVER=https://ntfy.sh
NTFY_TOPIC=〈長いでたらめの話題の名前〉
WHO="業務サーバーから"
CHECK_URL=https://〈公開の名前〉/login
CHECK_RESOLVE=〈公開の名前〉:443:〈VPS の住所〉   # VPS では 〈公開の名前〉:443:127.0.0.1
```

- ⚠ 話題の名前は、知っている人なら誰でも読めて送れます。リポジトリに書かず、デスクトップの構成メモにだけ残します。知らせの中身に業務のことは書きません（「応えません」と住所だけ）。
- 知らせるのは、続けて2回（約2分）失敗したときと、戻ったときだけ。止まっているあいだは6時間ごとに念押しします。
- 2台が同時に止まると、どちらも知らせられません。

## 業務サーバーの中だけで開く入口（2026-10-26）

w-cms は、ふだんの待ち受け（:8443・:9443）に加えて、業務サーバーの中のファイル（Unix ソケット）でも待ち受けられます。
この入口から来た要求は、決めた1人の w-cms の利用者として扱います（パスワードもセッションも使わない）。
Claude が本番のデータを、版と監査の残る保存の道で直すための入口です（`internal/auth/local_socket.go`・`cmd/w-cms/local_socket.go`）。

- 開けるのは、w-cms を動かしている利用者（w-cms）と root だけ（フォルダは `RuntimeDirectoryMode=0700`・ファイルは 0600）。
  つまり、業務サーバーへ SSH の鍵で入って sudo できる人だけが使えます。社外からも社内の LAN からも見えません。
- 監査には、その利用者の名前が残ります。権限は、その利用者の権限のままです。利用者を無効にすれば、入口も閉じます（401）。
- 設定（両方そろったときだけ開く・片方だけなら起動を止める）:
  - 本番 `/etc/w-cms/w-cms.env`: `WCMS_LOCAL_SOCKET=/run/w-cms/local.sock`・`WCMS_LOCAL_SOCKET_USER=Claude`
  - 試験用 `/etc/w-cms/test.env`: `WCMS_LOCAL_SOCKET=/run/w-cms-test/local.sock`・`WCMS_LOCAL_SOCKET_USER=a`
- systemd の設定（`w-cms.service`・`w-cms-test.service`）に `RuntimeDirectory` を足したので、入れ替えたら写して `daemon-reload` する。
- 使い方（業務サーバーで）:

  ```bash
  sudo curl -s --unix-socket /run/w-cms/local.sock http://localhost/api/me
  ```

  POST も同じ形です（Origin の確かめはしない・ブラウザはこのファイルを開けないため）。

## 端末の登録（2026-10-31）

社外からのログインを、登録した端末だけに絞ります（`docs/考察/【考察】端末の登録.md`）。

1. 記録する段階（いま）: `WCMS_DEVICE_REQUIRED` を書きません。登録のない端末も通し、監査に `login.unregistered-device` を残します。
2. 使う端末を全部、画面の「📱 端末」で登録します。社内からは、数字の住所ではなく公開の名前で開きます。
3. 監査に `login.unregistered-device` が出なくなったら、`/etc/w-cms/w-cms.env` に `WCMS_DEVICE_REQUIRED=1` を書いて本番を起こし直します。

⚠ 社外で全部の端末を失うと、社内へ行くまで入れません。困ったら、社内から業務サーバーへ SSH で入り、この1行を外して起こし直します。

## サーバーの監査（w-cms-audit・2026-10-26）

毎朝 6:30 ごろ、業務サーバーと VPS の前の24時間のログを要点に絞り、Claude（API・`claude-opus-5-5`）に危ないことが無いかを読ませます。

- 集めるもの: SSH のログイン・sudo・受信の壁が止めた通信・w-cms の監査の記録（ログイン・利用者・権限・持ち主・削除）・w-cms の警告と誤り・決まった時刻の仕事の結果・見張りの知らせ・止まった仕事・誤りの記録・守りの更新・ディスク。VPS の分は VPS の `wcms-audit-collect` が出します（`deploy/vps/README.md`）。
- WireGuard はふだんほとんどログを書かないので、トンネルの相手ごとの、いまの様子（相手の住所・最後のつながり・やりとりした量）を出します。1日のあいだの変化は、見張り（`w-cms-watch`・1分ごと・両方の機械）が記録します（相手の住所が変わった・5分を過ぎてもつながらない・またつながった。知らせはしない。状態は `/var/lib/w-cms-watch/wg`）。
- 送る前に、鍵や合言葉に見える長い文字の並び（32字以上）を伏せます。本文の中身は送りません。
- 残す所: `/var/lib/w-cms-audit/<日付>.txt`（送った要点）・`.json`（API の答え）・`.answer.json`・`.md`（90日）。w-cms の `WCMS_AUDIT_PARENT` のページの下の「サーバーの監査 <年-月>」へも書き足します（業務サーバーの中だけで開く入口・利用者 Claude）。
- 知らせ: 「見ておく」「危ない」のときと、流せなかったときに、見張りと同じ ntfy でスマホへ。
- 料金の目安: 1回 0.1〜0.2 ドルほど（入力2〜3万トークン）。Max プランの毎月の API クレジットを結んだ Console の組織の鍵なら、そこから引かれます。

置き方:

```bash
sudo install -m 755 deploy/server/w-cms-audit /usr/local/sbin/w-cms-audit
sudo cp deploy/server/w-cms-audit.service deploy/server/w-cms-audit.timer /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now w-cms-audit.timer
```

設定 `/etc/w-cms/audit.env`（持ち主 root・0600）:

```
ANTHROPIC_API_KEY=〈Claude Console で作った鍵——利用者が書く。チャットに貼らない〉
WCMS_AUDIT_PARENT=〈まとめを置くページの番号〉
AUDIT_VPS_CMD=〈VPS の要点を出す命令（任意）〉
```

この環境のこと（ふだんの利用者・社内の住所の当てはめ など、リポジトリに書かないもの）は `/etc/w-cms/audit-context.txt` に書けます（任意）。

試し方:
- `sudo w-cms-audit collect` — 集めた要点を出すだけ（API へは送らない）。
- `sudo env WCMS_AUDIT_SOCK=/run/w-cms-test/local.sock WCMS_AUDIT_PARENT=… w-cms-audit fake` — API を呼ばず、決まった試しの答えで、ファイルと試験用の w-cms へ残す所まで流す（知らせは送らない）。

## SSD の見張り（smartmontools・2026-10-26）

業務サーバーの2本の SSD（`nvme1` はシステムとデータ・`nvme0` は夜の控え `/srv/backup`）の、壊れかけ・誤り・熱すぎに気づくための見張りです。

- `sudo apt install smartmontools`。設定 `/etc/smartd.conf`（もとの設定は `/etc/smartd.conf.dist` に控えた）:

  ```
  DEVICESCAN -a -n standby -W 0,65,75 -s S/../../7/03 -m root -M exec /usr/local/sbin/w-cms-smart-alert
  ```

  全部の SSD を見て（`-a`）、65℃ で記録・75℃ で知らせ、毎週日曜 3時に短い自己診断を流します。
- 異常のときは `deploy/server/w-cms-smart-alert`（`/usr/local/sbin/`・755）が、見張りと同じ ntfy でスマホへ知らせます（重さ5）。
- 毎朝の監査（w-cms-audit）にも、SSD の健康の要点（判定・温度・寿命の使用・誤り・安全でない電源断）が入ります。
- 試し: `sudo env SMARTD_DEVICE=/dev/nvme1 SMARTD_FAILTYPE=試し SMARTD_MESSAGE=… w-cms-smart-alert`。


## 事務所の PC（HP405）への控え（2026-10-26）

夜の控えを、暗号化したまま、毎朝 HP405 へも写します（利用者:「過去のデータが全部なくなるのはマズいです」——サーバー機の外の控えは、事務所の別の PC と社外の両方）。

- 業務サーバー: `deploy/server/w-cms-pcrepo`（`/usr/local/sbin/`・root で毎朝 5:30・`w-cms-pcrepo.timer`）が、その日の写しを restic で暗号化して `/srv/backup/restic-pc` に入れます。パスワードは社外の控えと同じ `/etc/w-cms/offsite.pass`。古い控えは自動では消しません。
- 窓口: 利用者 `wcmspc`（`useradd --system --create-home --shell /bin/sh wcmspc`）の `/home/wcmspc/.ssh/authorized_keys` に、HP405 の鍵を `restrict,command="/usr/local/sbin/w-cms-pcrepo-export",from="192.168.1.0/24"` を付けて置きます。この鍵でできるのは、一覧（`list`）・1つのファイルを読む（`get <道>`）・写し終えたことを残す（`done <数>`）だけです（`deploy/server/w-cms-pcrepo-export`）。
- ⚠ 置き場の持ち主: `/srv/backup` は `root:wcmspc` の 710（wcmspc は通り抜けるだけ）、`/srv/backup/restic-pc` は wcmspc の組から読める（書けない）、暗号化していない夜の写し `/srv/backup/w-cms` は 700（w-cms-backup が毎晩 700 に保つ）。`/srv/backup/w-cms` を 755 にすると、wcmspc から本文や利用者の DB の写しが読めてしまいます。
- HP405: `deploy/office-pc/copy-backup.ps1` を `C:\w-cms-控え\copy-backup.ps1` に置き（BOM 付きの UTF-8）、Windows のタスク「w-cms の控えを写す」（毎日 8:00・電源が切れていたら次に入れたとき）が流します。置き場は `C:\w-cms-控え\restic-pc`（OneDrive の外）、記録は `C:\w-cms-控え\写した記録.log`。取ってきたファイルは、名前（中身の SHA-256）で壊れていないかを確かめます。鍵は `~\.ssh\id_ed25519_wcms_pc`。
- 毎朝の監査（w-cms-audit）が、HP405 が写し終えた記録を読みます（古くなっていれば気づける）。
- 戻すとき: `C:\w-cms-控え\restic-pc` を業務サーバー（または新しい機械）へ写し、`restic -r <置き場> --password-file <パスワード> restore latest --target <戻す先>`。パスワードは、USB メモリーと印刷した紙にもあります（2026-10-27 から・デスクトップには置かない）。

## 控えから戻す練習（2026-10-26）

利用者:「控えから戻す練習やりましょう」。本番には触らず、戻したものを脇の置き場（`/srv/restore-drill`・700）で確かめました。

### HP405 の控えから

1. HP405 の `C:\w-cms-控え\restic-pc` を業務サーバーの `/srv/restore-drill/hp405-repo` へ写す。
2. `restic -r /srv/restore-drill/hp405-repo --password-file /etc/w-cms/offsite.pass check` → 誤りなし。
3. `restic … restore latest --target /srv/restore-drill/hp405` → 13 秒。夜の写し（`/srv/backup/w-cms/data/latest`）と `diff -rq` で比べて、違い 0。
4. 戻したデータで、試しの w-cms を本番の実行ファイルのまま脇で起こす（社内にも出さない・利用者の DB は戻したもの）:

   ```bash
   sudo systemd-run --unit=w-cms-drill --uid=w-cms --gid=w-cms -p WorkingDirectory=/opt/w-cms/prod \
     -p RuntimeDirectory=w-cms-drill -p RuntimeDirectoryMode=0700 \
     -E WCMS_DATA_DIR=<戻した data> -E WCMS_ADDR=127.0.0.1:9444 -E WCMS_SECURE_COOKIES=0 \
     -E WCMS_LOCAL_SOCKET=/run/w-cms-drill/local.sock -E WCMS_LOCAL_SOCKET_USER=Claude /opt/w-cms/w-cms
   ```

   起きるときに索引を本文から作り直した（1,899 ページ・6 秒）。ページ・添付（本番と同じ SHA-256）・版（いちばん古い版も開ける）・表の検索を、中の入口（`curl --unix-socket /run/w-cms-drill/local.sock`）で確かめた。
5. `sudo systemctl stop w-cms-drill`、`sudo rm -rf /srv/restore-drill`。

⚠ 戻した `data/` には利用者の DB（パスワードの控え）とメールのサインインが入っています。練習の置き場は 700 にして、終わったら消します。

### VPS（社外）の控えから

`sudo w-cms-offsite restic restore latest --target /srv/restore-drill/vps`。

- ⚠ はじめは毎秒 0.07 MB しか出ず、6 GB に丸1日以上かかる見込みでした。職場 ⇔ VPS の道で、夜に 5〜6% の取りこぼしがあり（トンネルの外の ping でも同じ）、TCP の既定の加減（cubic）が速さを大きく下げていたためです。VPS と業務サーバーの TCP の加減を BBR にして、同じファイルが毎秒 2.5 MB になりました（下の「TCP の加減」）。
- 送る向き（控えを送る）は、昼に 6 GB を 18 分で送れていました。戻す向きだけが遅く、練習するまで気づけませんでした。
- BBR にしてから戻し直すと、6 GB を 12 分で戻せ、その日の控え（`/srv/backup/w-cms/data/daily/<日付>`）と 35,552 ファイルすべて一致しました（`diff -rq`）。戻る形は、その日の写しの中身がそのまま `--target` の下に並ぶ形です。

## TCP の加減（BBR・2026-10-26）

`deploy/vps/60-wcms-bbr.conf` を `/etc/sysctl.d/` に置き、`echo tcp_bbr > /etc/modules-load.d/wcms-bbr.conf`、`sysctl --system`。業務サーバーと VPS の両方に置いています。確かめる: `sysctl -n net.ipv4.tcp_congestion_control`（`bbr`）。
