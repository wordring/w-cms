# FAX の受信（HylaFAX+・プラネックス PL-US56K2（A））の置き方

2026-10-21 夜に作りました。考えたことは [【考察】通信箱の改善.md](../../docs/考察/【考察】通信箱の改善.md) §7.4 にあります。

FAX サーバーは、w-cms と同じ Ubuntu の機械に置きます（利用者の答え）。受けた FAX を PDF にして、w-cms の通信箱へ渡します。

- w-cms は、チャネル FAX・向き受信・対応：未処理の記録を作り、FAX を添付に置いて本文に表示します。
- 送ってきた番号から、連絡帳などの `FAX番号` のタグで相手を引きます。
- 判定（🤖 解析）は、記録の箱の「🤖 解析（添付）」で人が押します。

2026-10-25 に業務サーバー（Ubuntu 26.04）で、モデムが Class 1 の FAX モデムとして動くこと（Conexant CX93001）と、受けた FAX を w-cms へ渡す道（試験用の w-cms で、見本の1枚から FAX の記録ができた）を確かめました。同じ夜、本物のひかり電話の線で、複合機から1枚を受けて、本番の w-cms の通信箱に記録ができました（9600bps）。⚠ はじめは ECM（誤り訂正）の受け取りでページが空になったので、`config.faxmodem` で `Class1ECMSupport: no` にしました。

送る向きも、同じ夜に確かめました。サーバー機で `sendfax -n -d <番号> <FAX の形の TIFF>` と打つと、モデムが TEL2 からかけ、社内の複合機に1枚届きました（相手の受け取りの返事 MCF まで）。w-cms から送る画面は、まだありません。
- ⚠ 相手の電話が転送の設定だと、出る前に転送されて「話し中」と受け取り、HylaFAX は少しあとでかけ直します（かけ直しで転送先の携帯を鳴らした）。試しで送るときは、失敗したら `faxrm <番号>` で取り消します。
- ⚠ Ubuntu 26.04 では、Ghostscript（gs）に AppArmor の決まりが掛かっていて、HylaFAX の作業場所のファイルを読めないことがあります（業務サーバーで、/tmp や /var/lib のファイルを読めなかった）。PDF を送るとき HylaFAX は gs で FAX の形に直すので、w-cms から送る機能を作るときは、ここを先に確かめます。試しは、FAX の形の TIFF を直に渡して避けました。
- → 2026-10-25 夜に、`apparmor-local-gs`（このフォルダ）を `/etc/apparmor.d/local/gs` に置き、`sudo apparmor_parser -r /etc/apparmor.d/gs` で、gs が HylaFAX の作業場所を読み書きできるようにしました。FAX の利用者で PDF を FAX の形に直せることを確かめました。
- w-cms から送るのは、PDF のファイル表示の「⋯」の「📠 FAX で送る」です（`ext/comm/fax_send.go`・`/api/fax/send`・`/api/fax/status`）。w-cms（利用者 w-cms）が同じ機械の `sendfax` を呼びます。

## 1. モデムが使えるかを確かめる

1. USB でつなぎ、`lsusb` で型が見えるか、`ls /dev/ttyACM*` でポートができるかを見ます。
2. `sudo apt install minicom` → `sudo minicom -D /dev/ttyACM0` で、`AT` に `OK`、`AT+FCLASS=?` に `1` を含む答え（例 `0,1,1.0,2,8`）が返るかを見ます。
3. 返らなければ、このモデムでは FAX を受けられません（そのときは、Linux で使える FAX モデムを考えます）。

## 2. 決まった名前と省電力

1. `99-faxmodem.rules` の `XXXX`・`YYYY` を、`lsusb` で見た番号に書き換え、行頭の `#` を外します。
2. `/etc/udev/rules.d/` に置き、`sudo udevadm control --reload && sudo udevadm trigger`。
3. モデムを差すと `/dev/faxmodem` ができ、USB の省電力が切れます。

USB が省電力で眠ると、モデムと ND6 が着信を取りこぼします（利用者:「USBが省電力設定に代わるとFAXモデムが休止すると聞いています」「ND6もUSB接続なので対処が必要です」）。業務サーバーでは、次の3つを重ねて切っています（2026-10-26 に確かめた）。

- 1台ずつ: FAX モデム（`99-faxmodem.rules`）と ND6（`deploy/phone/99-aloha-nd6.rules` を `/etc/udev/rules.d/` に）の udev の決まりが `power/control=on` にします。差し直しても、再起動しても切れたまま。
- 全体: `/etc/default/grub` の `GRUB_CMDLINE_LINUX_DEFAULT` に `usbcore.autosuspend=-1`（`sudo update-grub` 済み・次の起動から効く）。
- 機械が眠らない: `sleep.target`・`suspend.target`・`hibernate.target`・`hybrid-sleep.target` は `masked`。

毎朝の監査（w-cms-audit）の「電話と FAX の USB」の節が、ND6 とモデムの省電力が `on`・いまの様子が `active` かを見ます。

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

1. w-cms の秘密の設定 `w-cms.env` に `WCMS_MACHINE_TOKEN`（16文字以上）と `WCMS_MACHINE_USER`（機械の書き手・管理者でない機械専用の利用者。deploy/phone/README.md の 1）を書いて、w-cms を起こし直します。
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

## 6. FAX の側のナンバー・ディスプレイ（2026-10-26 にモデムにたずねた）

> 2026-10-26 利用者:「ナンバーディスプレーの契約があってもND6が無いとFAX相手の番号は読めませんか？」「いまやって大丈夫です。結果が分かったら文書にも載せてください」

送ってきた所は、いまは**送り手の FAX 機が名乗る番号（TSI）**で分かります（`faxrcvd-wcms`。ナンバー・ディスプレイの番号があればそちらを先に使う）。これまでに届いた FAX は、どれも名乗りの番号が取れていました。名乗りは送る側が自由に書けるので、空のこと・まちがっていること・なりすましはあり得ます。

モデム（プラネックス PL-US56K2（A））にたずねた結果（FAX の受け口を30秒止めて、AT の命令で）:

| たずねたこと | 答え |
|---|---|
| チップ（`ATI3`・`AT+GMR`） | `CX93001-EIS_V0.2013-V92` |
| 国の設定（`AT+GCI?`） | `00`（日本） |
| FAX の種類（`AT+FCLASS=?`） | `0,1,1.0,2` |
| 発信者番号を知らせる機能（`AT+VCID=?`・`AT+VCID?`） | `(0-2)` がある・いまは `0`（切っている） |

- つまり、**このモデムは、ナンバー・ディスプレイの番号を受け取れる見込みがあります**。ただ、日本の送り方（ベルの前の合図に答えてから番号を受け取る）に本当に答えられるかは、本物の線で試すまで分かりません。売り場の仕様にも、ナンバー・ディスプレイの対応は書かれていません。
- 試すには、①ナンバー・ディスプレイの契約、②ホームゲートウェイで TEL2 のナンバー・ディスプレイを「使用する」、③HylaFAX がモデムに番号を知らせるよう頼む（`ModemResetCmds` に `AT+VCID=1`）と、その番号を読む設定（`CallIDPattern`）、が要ります。③は 2026-10-26 夜に入れました（6.1）。
- ⚠ 対応していない機械に「使用する」で番号を送ると、ベルが正しく鳴らない・受けられないことがあります。試して FAX が受けられなくなったら、②を「使用しない」に戻します。それまで TEL2 は「使用しない」のまま。
- 契約ができたら、電話（TEL1・ND6）を試すときに一緒に試します。
- ⚠ faxgetty を `systemctl stop` で止めると、記録に `Failed with result 'exit-code'`（終わりの番号 255）と出ます。止めたときにいつも出るもので、`start` で戻れば問題ありません。

### 6.1 番号を読む設定を入れた（2026-10-26 夜）

> 2026-10-26 利用者:「では通知番号を読むように出来ますか？ナンバーディスプレーの申し込みは明日やります」

- `config.faxmodem` に `ModemResetCmds: AT+VCID=1` と `CallIDPattern: "NMBR = "`・`CallIDPattern: "NAME = "` を足し、業務サーバーに入れて faxgetty を読み直しました。モデムはこの設定で待ち受けに戻りました。
- `faxrcvd-wcms` は、知らせの番号を先に数字に絞ってから、空なら FAX 機の名乗り（TSI）へ切り替えます。非通知（P）・表示圏外（O）・公衆電話（C）のように文字だけが来たときも、名乗りの番号を使います。
- 契約が無いあいだ・TEL2 が「使用しない」のあいだは、知らせが来ないだけで、これまでどおり名乗りの番号を使います。

**試し方**（ナンバー・ディスプレイの契約ができてから）:
1. ホームゲートウェイの設定の画面で、TEL2 のナンバー・ディスプレイを「使用する」にします。
2. 番号を通知する電話（携帯など）から、会社の FAX の番号へかけます。モデムが出たら、すぐ切ってかまいません。
3. 業務サーバーで `sudo journalctl -u faxgetty-faxmodem --since "-5 min"` を見ます。`CallID`・`NMBR` の行に、かけた番号が出れば読めています。
4. 次に FAX を1枚送ってもらい、通信箱の FAX の記録の番号が、ナンバー・ディスプレイの番号になっているかを見ます。
5. ⚠ 2 で呼び出しが鳴らない・モデムが出ないなら、1 を「使用しない」に戻します（FAX が受けられなくなるので）。そのときは、このモデムは日本のナンバー・ディスプレイの合図に答えられない、と分かったことになります。
### 6.2 試した結果（2026-10-27 夕）

> 2026-10-27 利用者:「ナンバーディスプレーが入りました」

- TEL2 のナンバー・ディスプレイを「使用する」にして、携帯から FAX の番号へ2回かけました。
- どちらもモデムは出ました（FAX の受信には障りがありません）。
- ただ、モデムから届いたのは呼び出し（`RING`）だけで、番号の知らせ（`NMBR`）は来ませんでした（2回目は `faxconfig -m faxmodem ServerTracing 9` で、モデムとのやりとりを全部記録に残して確かめました）。
- このモデムは、日本のナンバー・ディスプレイの合図（呼び出しの前の合図に答えてから番号を受け取る）に答えられない、と分かりました。FAX の送り元は、これからも FAX 機が名乗る番号（TSI）で見ます。
- TEL2 は「使用しない」に戻すよう勧めました（答えない機械がつながっていると、ホームゲートウェイが合図を待つあいだ呼び出しが遅れることがあるため）。番号を読む設定（6.1）は、害が無いので入れたままです。
- 戻すときの写し: `/etc/hylafax/config.faxmodem.bak-20261026-callid`・`/var/spool/hylafax/bin/faxrcvd-wcms.bak-20261026-callid`。
