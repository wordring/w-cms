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

`/etc/caddy/Caddyfile`。証明書は Caddy が Let's Encrypt から取り、自分で更新します。

⚠ 2026-10-26 の時点では、まだ w-cms へ中継していません（管理者 `a` のパスワードを決めるまで）。「準備中」だけを返します。

```caddyfile
〈公開の名前〉 {
	header {
		Strict-Transport-Security "max-age=31536000"
		X-Content-Type-Options "nosniff"
		-Server
	}
	respond "準備中です（w-cms）" 503
}
```

中継するときは、`respond` の行を次に替えます（考察 §3.2 の、知らせの流れを溜めない・待ち時間を長めにする、を入れた形）。

```caddyfile
	reverse_proxy https://10.8.0.2:8443 {
		flush_interval -1
		transport http {
			tls_server_name 〈公開の名前〉
			read_timeout 300s
			write_timeout 300s
		}
	}
```

- 業務サーバーの w-cms の証明書を〈公開の名前〉のものにするまでは、`tls_server_name` で確かめが通りません（今の業務サーバーの証明書は自己署名）。VPS が取った証明書を業務サーバーへ写す形は、考察 §3.3 の A です。
- 中継を始めるときは、業務サーバーの `w-cms.env` の `WCMS_TRUSTED_PROXIES` に `10.8.0.1` を書きます。書かないと、社外の全員が1つの接続元に見えます（ログインの失敗の数え方・監査の接続元）。

## まだのこと

- 管理者 `a` のパスワードを決める（利用者）。それまで中継しない。
- 業務サーバーの証明書を〈公開の名前〉のものにする（VPS で取って写す）。
- 社内からも〈公開の名前〉で繋ぐ（split-horizon。考察 §3.4）。
- 見張り（VPS から1分ごとにログインの画面を見に行く・考察 §6）。
