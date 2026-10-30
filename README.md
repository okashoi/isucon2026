# isucon-template

## 初期セットアップ

競技開始後、`/home/isucon` 直下にこのリポジトリを clone し、以下のスクリプトを実行します。

```bash
cd /home/isucon
git clone <this-repo> isucon-template
bash isucon-template/mybin/setup.sh
```

スクリプトは対話式で以下を順に処理します。

1. **バックアップ** — `webapp` → `webapp_bk` にコピー
2. **言語ディレクトリ選定** — `go` / `perl` / `ruby` / `python` / `node` / `rust` 等を自動検出し、使用言語を番号で選択。残りは削除確認
3. **その他ファイル整理** — 残りのディレクトリ・ファイルを1件ずつ `[k]eep / [d]elete / [i]gnore` で選択
4. **Makefile 設定** — `APPNAME`（systemd サービス名）とビルドコマンドを入力
5. **ファイル移動** — `webapp` の中身をリポジトリ直下に移動
6. **`.gitignore` 更新** — Step 3 で `[i]` を選んだ項目を追記
7. **Makefile 書き換え** — 入力した `APPNAME` とビルドコマンドを反映
8. **ディレクトリリネーム** — リポジトリを `webapp` にリネーム（元の構造に一致させる）

完了後は `/home/isucon/webapp` がリポジトリになります。

## /etc 設定の git 管理 (etc-sync)

複数台の `/etc` 配下の主要設定を `infra/<host>/etc/...` に収集し、このリポジトリで差分管理します。対象サーバと収集パスは `conf.yaml` に書きます。

```yaml
hosts:
  s1: isucon-s1
  s2: isucon-s2

includes:
  - /etc/sysctl.conf
  - /etc/sysctl.d
  - /etc/security/limits.conf
  - /etc/hosts

excludes:
  - "*.bak"
  - "*.sock"

host_includes:
  s1:
    - /etc/nginx/nginx.conf
    - /etc/mysql/mysql.conf.d/mysqld.cnf
  s2:
    - /etc/systemd/system
    - /etc/nginx/sites-enabled

host_excludes:
  s1:
    - "*.tmp"
```

- `hosts` のキーがリポジトリ内の保存先 `infra/<host>/etc/...` になります。
- `hosts` の値は `ssh` コマンドで接続できるエイリアス、または `user@host` です。
- `make etc-sync/*` は本番サーバーのどれか1台で実行する想定です。実行元サーバーから、自分自身を含む `hosts` の全接続先へ `ssh` できるようにしてください。
- `includes` にはファイルとディレクトリのどちらも指定できます。例: `/etc/nginx/nginx.conf`、`/etc/mysql/mysql.conf.d/mysqld.cnf`、`/etc/systemd/system`。
- `host_includes` / `host_excludes` は指定したホストにだけ追加適用されます。サーバごとに nginx だけ、mysql だけ、systemd だけ、のように管理対象を分けられます。
- `deploy` も対象ホストの `includes` + `host_includes` に含まれるファイル/ディレクトリだけを反映します。
- `conf.yaml` は shell で読むための限定形式です。YAML の入れ子、アンカー、複雑なクォートは扱いません。

よく使う操作:

```
make etc-sync/pull          # 全ホストから infra/<host>/etc/... へ収集
make etc-sync/diff          # 収集前の差分を dry-run で確認
make etc-sync/deploy        # リポジトリから /etc への反映差分を dry-run で確認
make etc-sync/deploy/apply  # 実際に /etc へ反映
```

特定ホストだけ操作する場合:

```
./mybin/etc-sync/etc-sync.sh pull s1
./mybin/etc-sync/etc-sync.sh deploy s1
./mybin/etc-sync/etc-sync.sh deploy --apply s1
```

`deploy` は事故防止のため既定で dry-run です。実反映には `--apply` を明示してください。`rsync` は remote 側で `sudo rsync` を使い、`--no-owner --no-group` を付けてローカルの所有者・グループ情報を `/etc` へ誤反映しないようにしています。

## pprotein セットアップ

pprotein サーバ（UI :9000）は構築済み前提です。

| 担当 | やること |
|------|----------|
| **あなた → AI に指示** | **Go だけ**（pprof :19001 + Initialize webhook）。指示後に AI が書いて PR を開く |
| **CLI** | nginx / mysql / agent / targets / alp / verify（ホスト conf は人がコマンド実行） |
| **あなた** | SG、値の決定、CLI 実行、Go PR のマージとデプロイ |

Go 以外（nginx.conf や mysqld.cnf）は AI に書かせない。`pprotein-setup.sh` に任せる。

### あなたがやること

| # | 作業 | 例・置き場 |
|---|------|------------|
| 1 | **SG** で pprotein → 各競技ホストの TCP **19000 / 19001** を許可 | AWS コンソール等 |
| 2 | pprotein の URL を決める | `export PPROTEIN_URL=http://203.0.113.10:9000` |
| 3 | 各ホストの **label と到達 IP** | `--host s1=203.0.113.11` |
| 4 | alp 用 `matching_groups` | `--group`（AI が提案してよい） |
| 5 | AI に Go 書き換えを指示し、できた **PR をマージしてデプロイ** | 「pprof 入れて」など |
| 6 | 各競技ホストで CLI を実行し、手元から targets / alp | `--app-pprof` は pprof を入れたホスト |

ログパスが既定と違うときだけ環境変数を足す: `PPROTEIN_HTTPLOG` / `PPROTEIN_SLOWLOG`。

### 競技ホスト上（コマンドはそのまま）

```sh
cd /path/to/isucon-template
sudo ./mybin/pprotein/pprotein-setup.sh nginx on
sudo ./mybin/pprotein/pprotein-setup.sh mysql on
sudo ./mybin/pprotein/pprotein-setup.sh agent install
./mybin/pprotein/pprotein-setup.sh verify
```

最終ベンチ前にログを止める例: `sudo ./mybin/pprotein/pprotein-setup.sh nginx off`

### 手元から API 登録（<> を自分の値に置換）

```sh
export PPROTEIN_URL=http://<pproteinのIP>:9000
./mybin/pprotein/pprotein-setup.sh targets \
  --host s1=<競技ホスト1のIP> \
  --host s2=<競技ホスト2のIP> \
  --app-pprof s1
./mybin/pprotein/pprotein-setup.sh alp --group '^/api/items/[^/]+$'
./mybin/pprotein/pprotein-setup.sh verify <競技ホスト1のIP> <競技ホスト2のIP>
```

Webhook（Initialize から GET）: `${PPROTEIN_URL}/api/group/collect`

`targets` の POST はリスト全体を置き換えます。UI で足した行も消えます。

### pprotein HTTP API

計測結果（httplog / slowlog / pprof / memo）を UI を介さずに取得・操作する方法は [docs/pprotein-api.md](docs/pprotein-api.md) にまとめています。
