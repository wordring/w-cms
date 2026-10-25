# 業務サーバー（Ubuntu）で w-cms を動かす

2026-10-25 に、業務サーバーで試験用を動かしたときに書きました。考えたことは [【考察】職場のサーバー機と社外からの接続.md](../../docs/考察/【考察】職場のサーバー機と社外からの接続.md) §2.3・§4・§6 にあります。

## 置き場所

| もの | 場所 | 持ち主 |
|---|---|---|
| リポジトリ（画面のファイルと設定もここから読む） | `/opt/w-cms/repo` | 人の管理用の利用者（例 toho） |
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

## 組み立てて入れ替える

```bash
cd /opt/w-cms/repo && git pull
go build -o /tmp/w-cms-test.new ./cmd/w-cms
sudo install -m 0755 /tmp/w-cms-test.new /opt/w-cms/w-cms-test
sudo systemctl restart w-cms-test
```

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
