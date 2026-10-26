# 社外の VPS（中継）を組む

2026-10-26 に、社外から w-cms に入る入口として VPS を組んだときに書きました。考えたことは [【考察】職場のサーバー機と社外からの接続.md](../../docs/考察/【考察】職場のサーバー機と社外からの接続.md) §3 にあります。

実際の名前と住所は、会社を特定できるのでリポジトリに書きません。下では次のように書きます（本物はデスクトップの `w-cms\サーバー機\構成メモ.txt`）。

| 書き方 | 何か |
|---|---|
| 〈公開の名前〉 | 社外から開く名前（サブドメイン） |
| 〈VPS の住所〉 | VPS の IPv4 の住所 |
| 〈VPS の会社〉 | VPS を借りた会社（DNS もここ） |

## 形

```text
スマホ・社外の PC ──▶ VPS（〈公開の名前〉・Caddy・証明書）
                        │  WireGuard のトンネル（10.8.0.0/24）
                        ▼
                    業務サーバー（10.8.0.2・w-cms 本番 :8443）
```

- VPS は中継だけです。w-cms のデータは VPS に置きません。
- トンネルは業務サーバーから VPS へ張ります。職場のインターネットの住所は固定でないので、VPS の側には職場の住所を書きません（業務サーバーの側の `PersistentKeepalive = 25` で、住所が変わっても VPS が覚え直します）。
- 職場のルーターに受信の穴は開けません。
- ⚠ `header_up Host {host}` を外さないこと。HTTPS の中継先には、Caddy は既定で中継先の住所（`10.8.0.2:8443`）を Host にします。w-cms は画面の住所と Host が合わない書き込みを断る（CSRF・`internal/auth/middleware.go` の sameOrigin）ので、社外からはログインまではできても、書ける形にできず、保存もできませんでした（2026-10-26 に利用者が社外のモバイル回線で試して見つけた）。
- 試験用（:9443）は中継しません。

## 受信の壁

VPS の会社の画面の「セキュリティグループ」と、VPS の中の ufw の、2重です。どちらも同じ口だけを開けます。

| 口 | 相手 | 用途 |
|---|---|---|
| TCP 443 | どこからでも | HTTPS |
| TCP 80 | どこからでも | 証明書の取得・HTTPS への案内 |
| UDP 51820 | どこからでも | WireGuard（鍵の合わない相手には何も返さない） |
| TCP 22 | トンネルの中（10.8.0.2）からだけ | SSH。外からは閉じた（2026-10-26）。セキュリティグループには書かない |
| ICMP | どこからでも | ping（確かめ用） |

- 送信は全部通します（更新・証明書・トンネル）。外へ出る 25番は VPS の会社が止めていて（OP25B）、解除しません。
- ⚠ VPS の会社の Ubuntu のイメージは、ufw が最初から動いていて 22番だけを通します。セキュリティグループだけ開けても、80・443 は時間切れになります（2026-10-26 に踏んだ）。

```bash
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 51820/udp
ufw allow in on wg0 from 10.8.0.2 to any port 22 proto tcp comment 'SSH (トンネルの中から)'
ufw delete allow OpenSSH   # 外からの 22番を閉じる（トンネルの中から入れるのを確かめてから）
```

- 外からの 22番は、2026-10-26 に閉じました（利用者:「ではSSHは外します」）。入れるのは、職場の LAN から業務サーバーを踏み台にして、トンネルの中を通る道だけです（下の WireGuard の節）。
- 社外から VPS に入りたくなったら、その PC を WireGuard の相手として足します。締め出されたときは、VPS の会社の画面のコンソール（root のパスワード）から入ります。

## 入れたもの（Ubuntu 26.04 LTS）

- `wireguard-tools`（Ubuntu のもの）
- `caddy`（Caddy の公式の apt の置き場 `dl.cloudsmith.io/public/caddy/stable`）

⚠ 入れ直した直後は、自動の更新（unattended-upgrades）が apt のロックを持っています。終わるのを待ってから入れます。

## SSH は鍵だけ

2026-10-26 から、パスワードでは入れません（利用者:「VPS の SSH を鍵だけにしてください」）。root も鍵でだけ入れます。

VPS の会社の Ubuntu のイメージは `/etc/ssh/sshd_config.d/50-cloud-init.conf` に `PasswordAuthentication yes` を書いています。sshd は最初に読んだ値を使うので、それより先に読まれる名前のファイルに書きます。

```bash
cat > /etc/ssh/sshd_config.d/00-wcms.conf <<"EOF"
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
EOF
sshd -t && systemctl reload ssh
```

- 職場の PC から入る: `ssh -i ~/.ssh/id_ed25519_wcms -J wcms-server root@10.8.0.1`（PowerShell なら `ssh -i $HOME.sshid_ed25519_wcms -J wcms-server root@10.8.0.1`。`wcms-server` は `~/.ssh/config` に書いた業務サーバーの呼び名）。
- ⚠ 鍵を無くしたら SSH では入れません。VPS の会社の画面のコンソール（root のパスワード）から入って、鍵を足し直します。
- ほかの PC から入るときは、その PC で鍵を作り、公開鍵を `/root/.ssh/authorized_keys` に足します。

## WireGuard

秘密の鍵は、それぞれの機械の中で作り、外へ出しません（`/etc/wireguard/wg0.key`・0600）。設定のファイルには公開鍵だけを書きます。

```bash
umask 077; wg genkey > /etc/wireguard/wg0.key; wg pubkey < /etc/wireguard/wg0.key
```

VPS の `/etc/wireguard/wg0.conf`:

```ini
[Interface]
Address = 10.8.0.1/24
ListenPort = 51820
PostUp = wg set %i private-key /etc/wireguard/wg0.key

[Peer]
PublicKey = 〈業務サーバーの公開鍵〉
AllowedIPs = 10.8.0.2/32
```

業務サーバーの `/etc/wireguard/wg0.conf`:

```ini
[Interface]
Address = 10.8.0.2/24
PostUp = wg set %i private-key /etc/wireguard/wg0.key

[Peer]
PublicKey = 〈VPS の公開鍵〉
Endpoint = 〈VPS の住所〉:51820
AllowedIPs = 10.8.0.1/32
PersistentKeepalive = 25
```

- `Endpoint` は名前ではなく住所で書きます（起動のときに名前を引けないと、トンネルが上がらないため）。
- 両方で `systemctl enable --now wg-quick@wg0`。確かめは `wg show wg0`（latest handshake）と `ping 10.8.0.1`。
- 業務サーバーの ufw には、トンネルの VPS の側からだけ本番に入れる決まりを足しました。

```bash
sudo ufw allow in on wg0 from 10.8.0.1 to any port 8443 proto tcp comment "w-cms 本番 (VPS の中継から)"
```

- 職場の PC から、トンネルの中を通って VPS に入る:

```bash
ssh -i ~/.ssh/id_ed25519_wcms -J wcms-server root@10.8.0.1
```

## Caddy

`/etc/caddy/Caddyfile`。証明書は Caddy が Let's Encrypt から取り、自分で更新します。2026-10-26 午後から、w-cms へ中継しています（それまでは「準備中」だけ・設定の控えは `/etc/caddy/Caddyfile.bak-placeholder`）。

```caddyfile
〈公開の名前〉 {
	header {
		Strict-Transport-Security "max-age=31536000"
		-Server
	}
	reverse_proxy https://10.8.0.2:8443 {
		# 元の名前（Host）をそのまま渡す。
		header_up Host {host}
		# 知らせの流れ（SSE）を溜めずにすぐ流す。
		flush_interval -1
		transport http {
			tls_server_name wcms-server
			tls_trust_pool file /etc/caddy/wcms-upstream.pem
			dial_timeout 10s
			response_header_timeout 300s
		}
	}
}
```

- 中継の先（業務サーバー）は、名前 `wcms-server` で来た接続に、いままでの自己署名の証明書を渡します（業務サーバーの w-cms は名前で証明書を使い分ける——`cmd/w-cms/cert_reload.go` の certChooser）。Caddy はその証明書そのもの（`/etc/caddy/wcms-upstream.pem`・公開の部分だけ）を信じて確かめます。⚠ 業務サーバーの `data/tls/cert.pem` を作り直したら、ここへ写し直します（写さないと中継が止まる）。
- 業務サーバーの `w-cms.env` に `WCMS_TRUSTED_PROXIES=10.8.0.1` を書いてあります。書かないと、社外の全員が1つの接続元に見えます（ログインの失敗の数え方・監査の接続元）。
- 試験用（:9443）は中継しません。

## 証明書を業務サーバーへ写す

社内からも〈公開の名前〉で開くので、業務サーバーにも〈公開の名前〉の証明書が要ります。Caddy が取った証明書を、業務サーバーがトンネルの中から毎日取りに来ます（`deploy/server/w-cms-cert-pull`・タイマー `w-cms-cert-pull.timer`）。

- VPS の側は `/usr/local/sbin/wcms-cert-export`（`deploy/vps/wcms-cert-export`）。root の `authorized_keys` に、業務サーバーの写し用の鍵を、この命令だけを許して書きます。

```text
command="/usr/local/sbin/wcms-cert-export 〈公開の名前〉",from="10.8.0.2",restrict ssh-ed25519 AAAA… w-cms-cert-pull
```

- ほかの命令を頼んでも、この命令しか動きません（2026-10-26 に `id; cat /etc/shadow` を頼んで、証明書だけが返るのを確かめた）。
- VPS に業務サーバーへの鍵は置きません（VPS が乗っ取られても、業務サーバーへは入れない）。

## 社外の控えの受け口（2026-10-26）

業務サーバーの夜の控えを、restic で暗号化してここへも送ります（`deploy/server/w-cms-offsite`）。オブジェクトストレージではなく VPS に置きます（利用者:「オブジェクトストレージとＶＰＳの価格がだいたい同じです」）。

- 受け口は SFTP だけの利用者 `wcmsbak`。トンネルの中（10.8.0.2）から、鍵でだけ。自分のフォルダ（`/srv/restic-chroot`）の外は見えません（`/etc/ssh/sshd_config.d/20-wcmsbak.conf`・鍵は `/etc/ssh/authorized_keys.d/wcmsbak` に `from="10.8.0.2",restrict` を付けて）。
- 控えは業務サーバーで暗号化されて届きます。VPS が乗っ取られても中身は読めません。
- 1時間たったファイルに `chattr +i` を付けます（`wcms-restic-seal` とタイマー）。業務サーバーの鍵では、送った控えを消せも書き換えもできません。⚠ 古い控えを整理するときは、VPS の root で `chattr -R -i /srv/restic-chroot/repo` を外してから、業務サーバーで `w-cms-offsite restic forget --prune …`、終わったら封じ直します。
- 追記専用の受け口（restic の rest-server）は Ubuntu の部品に無く、インターネットからソースを取る作業がこの会話の決まりで止められたので、OpenSSH と chattr で同じ守りを作りました。

## 見張り

業務サーバーと同じ `w-cms-watch` を置いています（`deploy/server/README.md` の「見張り」）。VPS の `/etc/w-cms/watch.env` の `CHECK_RESOLVE` は `〈公開の名前〉:443:127.0.0.1`（Caddy から先を見る）。

## まだのこと

- 社外の控え（オブジェクトストレージと restic）。
