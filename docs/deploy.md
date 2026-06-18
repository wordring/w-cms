# w-cms デプロイ・運用マニュアル

このドキュメントでは、**w-cms** をサーバー上で運用・デプロイする際の手順と、ディレクトリ構成の動作仕様について説明します。

---

## 1. サーバー実行時のディレクトリ構成と動作仕様（対策Aの採用）

w-cms は、データ保存用ディレクトリ（`data`）へのパスがソースコード内で**相対パス**で実装されています。
そのため、実行時の挙動は以下のようになります。

*   **動作仕様**: `data` ディレクトリは、**「実行可能ファイルを実行した時のカレントディレクトリ（作業ディレクトリ）」の直下**に生成されます。
*   **本番サーバーでの運用方法**: 実行可能ファイルの配置場所に関わらず、サービス起動時に作業ディレクトリ（WorkingDirectory）を明示的に指定して実行します。

---

## 2. サーバー（Linux / systemd）へのデプロイ手順

本番サーバーで常時実行（デーモン化）させるための手順です。

### ステップ 1: アプリケーションのビルド
開発環境でターゲットサーバーのOS（例: Linux）に合わせてビルドを行い、バイナリを生成します。

```bash
# Linux（AMD64環境）向けにクロスビルドする場合
$Env:GOOS="linux"
$Env:GOARCH="amd64"
go build -o w-cms cmd/w-cms/main.go
```

### ステップ 2: サーバーへの配置
生成された `w-cms` バイナリを、サーバーの任意のパスに転送します。
*   **バイナリ配置先例**: `/usr/local/bin/w-cms`
*   **データ格納先ディレクトリ例**: `/var/www/w-cms` （ここを作業ディレクトリとします）

サーバー上でデータ格納先ディレクトリを作成し、適切な権限を付与しておきます。
```bash
# サーバー側で実行
mkdir -p /var/www/w-cms
chown -R www-data:www-data /var/www/w-cms
```

### ステップ 3: systemd サービスファイルの作成
`/etc/systemd/system/w-cms.service` を作成し、以下のように定義します。

```ini
[Unit]
Description=w-cms Content Management System
After=network.target

[Service]
Type=simple
User=www-data
Group=www-data
# ★重要: dataフォルダを作成・参照する基準ディレクトリを指定します
WorkingDirectory=/var/www/w-cms
# 実行バイナリのパス
ExecStart=/usr/local/bin/w-cms
Restart=always

[Install]
WantedBy=multi-user.target
```

### ステップ 4: サービスの起動と有効化
```bash
# 設定の再読み込み
sudo systemctl daemon-reload

# サービスの起動
sudo systemctl start w-cms

# サーバー起動時の自動起動を有効化
sudo systemctl enable w-cms

# ステータスの確認
sudo systemctl status w-cms
```

この設定により、`data/cms.db` や `data/master/` は常に `/var/www/w-cms/data/` 配下に作成・保存され、安全に運用できます。

---

## 3. 開発環境（会社・自宅）での注意点

複数環境で開発を行う際、`git pull` 後にローカル環境で動かす場合も同様の考え方が適用されます。

*   **ローカル実行時**:
    プロジェクトルート（`C:\Users\kouic\source\repos\w-cms`）で `go run cmd/w-cms/main.go` を実行した場合、データは `C:\Users\kouic\source\repos\w-cms\data` に作成されます。
*   **Gitの除外設定**:
    ローカルで作成されたデータベースやアップロード画像などの実データは、`.gitignore` によってGitの管理対象外に設定されているため、会社と自宅の間で勝手に上書き同期されることはありません（ソースコードのみが同期されます）。
