# pprotein HTTP API

計測結果は pprotein の HTTP API から読む。[kaz/pprotein v1.2.4](https://github.com/kaz/pprotein/tree/v1.2.4) のルートと、2026-10-25 に `http://3.112.51.112:9000` へ当てた応答で確認した。ベース URL が変わったときは GitHub issue #1 を正とする。

認証はない。`/api/*` は `Cache-Control: no-store`。グループ一覧の API はない。

## 結果の読み方

1. 次の 4 つを GET する。
   - `/api/pprof`
   - `/api/httplog`
   - `/api/slowlog`
   - `/api/memo`
2. `Snapshot.GroupId` で束ねる。ID はサーバのローカル時刻 `2006-01-02_15-04-05.999999` なので、文字列の降順が新しい順になる。今のサーバ時計は UTC。
3. `Status` が `ok` のエントリだけ詳細を取る。

| 種類 | 詳細 | 本体 |
|---|---|---|
| httplog | `GET /api/httplog/{id}` | alp の TSV |
| slowlog | `GET /api/slowlog/{id}` | slp の TSV |
| pprof | `GET /api/pprof/data/{id}` | gzip した pprof protobuf |
| memo | 一覧の `Message`、または `GET /api/memo/{id}` | メモ本文 |

`{id}` は一覧の `Snapshot.ID`（例: `dmdmr9svj1gl-httplog.log`）。

生ログの `GET /api/httplog/data/{id}` と `GET /api/slowlog/data/{id}` は数十 MB になる。集計 TSV で足りるときは取らない。

`GET /api/group/collect` は計測を開始する。結果を読むだけでは叩かない。

## 一覧の要素

```json
{
  "Status": "ok",
  "Message": "Ready",
  "Snapshot": {
    "Type": "httplog",
    "ID": "dmdmr9svj1gl-httplog.log",
    "Datetime": "2026-10-25T04:21:56.654992677Z",
    "Repository": {
      "Ref": "refs/heads/main",
      "Hash": "a2b67f27e819...",
      "Author": "...",
      "Message": "...",
      "Remote": "..."
    },
    "GroupId": "2026-10-25_04-21-56.653634",
    "Label": "s1",
    "URL": "http://13.230.95.68:19000/debug/log/httplog",
    "Duration": 60
  }
}
```

`Status` は `pending`、`ok`、`fail`。`Message` は `Collecting`、`Processing`、`Ready`、または失敗理由。Git 情報を取れなかった収集は `Repository` が空オブジェクト、メモは `null`。

存在しない ID の詳細は 404 ではなく 500 で、`{"message":"failed to get entry: no such entry: <nil>"}`。未定義パスは 404 で `message` と `error` の両方がある。削除 API はない。

## 設定

| メソッド | パス | 本体 |
|---|---|---|
| GET / POST | `/api/group/targets` | JSON 配列。GET の `Content-Type` は `application/json` |
| GET / POST | `/api/httplog/config` | YAML。GET の `Content-Type` は `application/x-yaml` |
| GET / POST | `/api/slowlog/config` | YAML。GET の `Content-Type` は `application/x-yaml` |

POST はファイル全体の置換で、成功は空の 200。パースやバリデーションの失敗は 400。targets の各要素は `Type`、`Label`、`URL`（URL 形式）、`Duration`（1 以上）が必須。`Type` は `pprof`、`httplog`、`slowlog` のいずれか。collect は `POST /api/{Type}` に転送する。

2026-10-25 時点の targets は、pprof と httplog が `13.230.95.68`、slowlog が `35.78.64.25`、いずれも `Duration` は 60。最新値は GET で取る。

## 収集

`GET /api/group/collect` は targets の全件を非同期で開始し、受付が終わると空の 200 を返す。データが揃うのはその後で、待ち時間は `Duration` 秒に処理時間を足したもの。完了は一覧を再取得するか、`GET /api/event`（`Content-Type: text/event-stream`。`data:` に一覧と同じ JSON）で `Status` が `ok` か `fail` になるのを見る。

1 件だけ始めるときは `POST /api/pprof`、`/api/httplog`、`/api/slowlog`。フィールド名に JSON タグはなく、次の名前のまま送る。

```json
{"GroupId":"任意","Label":"s1","URL":"http://host:19001/debug/pprof/profile","Duration":60}
```

成功は空の 200。収集自体はバックグラウンドで、`URL` か `Duration` が空だと後から `fail` になる。CPU profile は同時に 1 本だけで、競合すると `Message` が `cpu profiling already in use` になる。

pprotein は対象 URL に `?seconds={Duration}` を付けて GET する。ログは待ったあいだに増えた分、pprof は CPU profile を返す。

## httplog / slowlog

| メソッド | パス | 本体 |
|---|---|---|
| GET | `/api/httplog`、`/api/slowlog` | 一覧 |
| GET | `/api/httplog/{id}`、`/api/slowlog/{id}` | 集計 TSV |
| GET | `/api/httplog/data/{id}`、`/api/slowlog/data/{id}` | 生ログ |
| GET | `/api/httplog/data/latest`、`/api/slowlog/data/latest` | 最新の `ok` の生ログ。`?label=` でラベルを指定できる |

集計の `Content-Type` は `application/json` だが、本体は TSV。JSON としてパースしない。

httplog の列は `Count`、`1xx`、`2xx`、`3xx`、`4xx`、`5xx`、`Method`、`Uri`、`Min`、`Max`、`Sum`、`Avg`、`P90`、`P95`、`P99`、`Stddev`、`Min(Body)`、`Max(Body)`、`Sum(Body)`、`Avg(Body)`。

slowlog の列は `Count`、`Query`、`Min(QueryTime)`、`Max(QueryTime)`、`Sum(QueryTime)`、`Avg(QueryTime)`、`Min(LockTime)`、`Max(LockTime)`、`Sum(LockTime)`、`Avg(LockTime)`、`Min(RowsSent)`、`Max(RowsSent)`、`Sum(RowsSent)`、`Avg(RowsSent)`、`Min(RowsExamined)`、`Max(RowsExamined)`、`Sum(RowsExamined)`、`Avg(RowsExamined)`。

## pprof

`GET /api/pprof/{id}` という集計 API はない。生データは `GET /api/pprof/data/{id}` と `GET /api/pprof/data/latest`（`Content-Type: application/gzip`）。`Status` が `ok` になったスナップショットには、次の UI 用パスが追加される。

| パス | 応答 |
|---|---|
| `/api/pprof/{id}/top` | HTML の表。列は Flat、Flat%、Sum%、Cum、Cum%、Name |
| `/api/pprof/{id}/flamegraph` | HTML（1 MB 超） |
| `/api/pprof/{id}/download` | protobuf を gzip したもの |
| `/api/pprof/{id}/` | 501。`Could not execute dot; may need to install graphviz.` |

## memo

`POST /api/memo` の本体は次の JSON。成功は空の 202。

```json
{"GroupId":"2026-10-25_04-21-56.653634","Label":"measure","Text":"..."}
```

`GET /api/memo/{id}` は `{"Text":"..."}`。一覧の `Message` は `Text` と同じ文字列。

この練習の計測メモは、`Text` に次の JSON を文字列として入れている。

```json
{"schema":"pprotein-measure/1","comment":"...","score":38731,"pass":true,"tags":["notification"]}
```

`comment` は日本語の計測メモ、`score` と `pass` はベンチ結果、`tags` は施策のラベル。古いメモはこの形でないものもある。
