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
```

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
WCMS_ADDR=:9443
WCMS_TLS_CERT=/var/lib/w-cms/data-test/tls/cert.pem
WCMS_TLS_KEY=/var/lib/w-cms/data-test/tls/key.pem
WCMS_SECURE_COOKIES=1
```

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
WCMS_TLS_PUBLIC_CERT=/var/lib/w-cms/data/tls/public.crt
WCMS_TLS_PUBLIC_KEY=/var/lib/w-cms/data/tls/public.key
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
