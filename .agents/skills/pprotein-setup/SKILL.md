---
name: pprotein-setup
description: >-
  Prepare pprotein profiling for any ISUCON contest: discover hosts and app
  layout, tell the user to run mybin/pprotein/pprotein-setup.sh
  (nginx/mysql/agent/targets/alp/verify), and when the user instructs Go app
  changes, implement pprof (:19001) + Initialize webhook and open a PR.
  Host setup is CLI, not AI-edited configs. AWS SG is always out of scope.
  Use when the user asks for pprotein setup, group/targets, httplog/config,
  alp config, agent install, or Go pprof/webhook instrumentation.
---

# pprotein Setup (any ISUCON)

## Purpose

計測対象サーバと構築済み pprotein をつなぎ Collect できる状態にする。

**役割の切り分け:**

| 対象 | やり方 |
|------|--------|
| nginx / mysql / agent / targets / alp | **CLI**（人が実行。AI はコマンドを渡すだけ） |
| AWS SG 19000/19001 | **人のみ** |
| **Go アプリ**（pprof + Initialize webhook） | **人が指示 → AI が書いて PR を開く** |

AI が設定ファイルやホストを直接いじるのは Go のアプリ差分だけ。ホスト側の nginx.conf 等は触らない。

最終回答では、ユーザーが埋める値を先に表で出す（空欄を残さない）:

| ユーザー作業 | 値 |
|--------------|-----|
| SG 19000/19001（pprotein→競技ホスト） | 手動 |
| `PPROTEIN_URL` | `http://…:9000` |
| `--host label=ip` | 到達可能な IP（既定 Public） |
| `alp --group` | パラメータ付きルートの regex（提案してよい） |
| Go 書き換えの指示 | 「pprof 入れて」「Go 側やって」など。指示後に AI が PR |
| PR のマージとデプロイ | AI が PR を開いたあと |

```sh
# 各競技ホスト上
sudo ./mybin/pprotein/pprotein-setup.sh nginx on
sudo ./mybin/pprotein/pprotein-setup.sh mysql on
sudo ./mybin/pprotein/pprotein-setup.sh agent install
./mybin/pprotein/pprotein-setup.sh verify

# pprotein API に届くマシンから
export PPROTEIN_URL=http://<pprotein-host>:9000
./mybin/pprotein/pprotein-setup.sh targets --host s1=<ip> --host s2=<ip> --app-pprof s1
./mybin/pprotein/pprotein-setup.sh alp --group '^/api/items/[^/]+$'
```

## Ownership

| 担当 | やること |
|------|----------|
| **CLI** | nginx LTSV、mysql slowlog、agent、targets / alp、verify |
| **エージェント** | Discover、CLI 引数の提案。**Go 書き換えはユーザー指示があってから**実装して PR を開く |
| **ユーザー** | SG、SSH、CLI 実行、Go 書き換えの指示、PR マージ、デプロイ |

- Go 差分はチャットに貼って終わりにしない。指示を受けたら **ブランチを切って PR を開く**（このリポジトリでは PR は日本語）。
- ホスト設定のセットアップ依頼だけでは Go PR を勝手に始めない。指示が無ければ「Go 側は指示があれば PR にします」と一文添える。
- **AWS SG は変更しない。**
- サーバ上で **git checkout / switch しない。**
- SSH はユーザー明示時のみ。

## Architecture

| Role | Port | Process |
|------|------|---------|
| agent | 19000 | `pprotein-agent` |
| app pprof | 19001 | アプリ内 `standalone.Integrate` |
| UI | 9000 | pprotein Collect / alp / slp |

Collect webhook は **GET** `/api/group/collect`（`PPROTEIN_WEBHOOK_URL`）。

## Phase 0 — Discover

不明なら確認。SSH しない。

1. ホスト label / Public IP
2. `PPROTEIN_URL`
3. アプリ言語・エントリポイント・Initialize パス（Go 指示に備える）
4. ログパス（既定と違うときだけ）
5. matching_groups 用ルート

## Workflow

```
- [ ] 0. Discover
- [ ] 1. CLI コマンドを渡す（nginx → mysql → agent → verify）
- [ ] 2. SG は注記のみ
- [ ] 3. targets / alp の CLI コマンドを渡す
- [ ] 4. ユーザーが Go 書き換えを指示したら → pprof + webhook の PR を開く
```

### Go 書き換え（ユーザー指示後）

トリガー例: 「pprof 入れて」「Go 側やって」「webhook も PR で」。

[reference.md](reference.md) に従う。古い Echo middleware / POST webhook は使わない。

PR に含めるもの:

1. `go standalone.Integrate(":19001")`
2. Initialize（または同等）先頭の非同期 GET webhook
3. `PPROTEIN_WEBHOOK_URL` の env / systemd 差分（値はプレースホルダ可）
4. 依存 `github.com/kaz/pprotein`

Go 以外で :19001 が出せないときは webhook のみ、またはスキップ理由を書く。既存の Initialize 付随処理は消さない。

### CLI 詳細

`./mybin/pprotein/pprotein-setup.sh help` を正とする。ホストの conf 編集は CLI に任せ、AI は書き換えない。

### matching_groups

[reference.md](reference.md)。`alp --group` または `--groups-file` に渡す。

## Agent Behavior

1. 問題固有パスをハードコードしない。
2. ホスト設定は CLI コマンドを渡す。AI が nginx/mysql を直接編集しない。
3. **Go だけ**ユーザー指示後に実装し、**PR URL を返す**。
4. 最終回答は CLI コマンド列、Webhook URL、前提 IP。Go 指示済みなら PR URL も。
5. SG を変更しない。pprotein 本体は構築しない。サーバ上で branch を変えない。

## References

- CLI: `mybin/pprotein/pprotein-setup.sh`
- Go 雛形: [reference.md](reference.md)
