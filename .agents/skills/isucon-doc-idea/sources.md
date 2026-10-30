# ISUCON 過去ドキュメント 収集リスト (ISUCON5〜14)

`reference.md` の対応表を作るために読んだ一次資料の一覧。各回の資料は次の4種類に分けてある。

- **当日マニュアル**: スコア計算式、タイムアウト、反映猶予、失格条件、追試手順。施策のヒントが一番多い。
- **アプリケーションマニュアル / 仕様書**: ドメイン仕様、外部 API、キャッシュしてよい根拠になる業務ルール。ISUCON8 本選以降は独立した文書になっていることが多い。
- **レギュレーション**: ほぼ定型文。ISUCON9 以降は ISUCON2026 版とほとんど差分がないので、読むのは差分だけでよい。
- **講評・解説 / FAQ**: 文言と実際に効いた施策の答え合わせに使う。

> 調べた範囲の補足
> - ISUCON13 と ISUCON14 は本選だけの開催。
> - ISUCON15 は開催されていない。次回は名称を変えた **ISUCON2026 (2026-10-31 開催)**。
> - ISUCON7 本選の当日マニュアルは見つからなかったため、講評だけを使った。

## レギュレーションの共通部分（毎回ほぼ同じ文言）

- 変更してはいけないもの: URI（ただしサーバー側で生成する ID は文字種を変えなければ自由）、レスポンスの構造（HTML の DOM、JSON。ISUCON10 以降は JSON も明記）、JS/CSS、メディアファイル。
- 許可されている作業: サーバーの役割変更、スキーマ変更とインデックス、DB ミドルウェアの変更、**キャッシュ機構の追加、ジョブキュー機構の追加による遅延書き込み**、他言語での再実装、AI の利用（ISUCON13 以降）。
- 守る必要があるもの: 再起動後も正常に動くこと、ベンチマーク中に書き込んだデータが再起動後も取得できること、メンテナンスコマンドとの互換性。
- 文書の優先順位: **当日マニュアル > レギュレーション**（ISUCON10 以降は明記）。
- 最新版: [ISUCON2026 レギュレーション](https://isucon.net/archives/59966826.html)

## 回ごとの資料

| 回 | 題材 | 当日マニュアル | アプリ仕様・外部API仕様 | レギュレーション | 講評・FAQ・補助 |
|---|---|---|---|---|---|
| 5 予選 | ISUxi (SNS) | [gist tagomoris/1a2df5ab](https://gist.github.com/tagomoris/1a2df5ab0999f5e64cff) | – | [45347574](https://isucon.net/archives/45347574.html) | [講評](https://isucon.net/archives/45627361.html) / [FAQ gist](https://gist.github.com/941/f11e4c85ebca0cb3d307) |
| 5 本選 | AirISU (外部API集約) | [gist tagomoris/2be8751d](https://gist.github.com/tagomoris/2be8751d8c13f5e78e61)（本選レギュレーション兼用） | [CONCEPT.md](https://github.com/isucon/isucon5-final/blob/master/CONCEPT.md) | 同左 | [講評](https://isucon.net/archives/45905117.html) |
| 6 予選 | isuda/isutar/isupam | [Regulation.md](https://github.com/isucon/isucon6-qualify/blob/master/Regulation.md)（中身は予選マニュアル） | – | [48396740](https://isucon.net/archives/48396740.html) | [講評](https://isucon.net/archives/48697611.html) / [FAQ gist](https://gist.github.com/941/1ba9b7f3e62f6cc8980e7a17db85f02d) |
| 6 本選 | ISUketch (SSE) | [regulation.md](https://github.com/isucon/isucon6-final/blob/master/regulation.md) | – | 同左 | [講評](https://isucon.net/archives/48778066.html) |
| 7 予選 | isubata (チャット) | [gist 941/8c64842b](https://gist.github.com/941/8c64842b71995a2d448315e2594f62c2) | – | [50927022](https://isucon.net/archives/50927022.html) | [講評](https://isucon.net/archives/50961706.html) / [FAQ gist](https://gist.github.com/941/300fb47c4016d32fa4d6291c1a61364d) |
| 7 本選 | Chair Constructor Online | 未入手 | – | – | [講評](https://isucon.net/archives/51187977.html) |
| 8 予選 | Torb (チケット予約) | [doc/MANUAL.md](https://github.com/isucon/isucon8-qualify/blob/master/doc/MANUAL.md) | – | [52445389](https://isucon.net/archives/52445389.html) | [講評](https://isucon.net/archives/52520045.html) / [FAQ gist](https://gist.github.com/941/9897ba542465368d941f956fc0be4590) |
| 8 本選 | ISUCOIN (取引所) | [docs/MANUAL.md](https://github.com/isucon/isucon8-final/blob/master/docs/MANUAL.md) | [WEBAPP_SPEC](https://github.com/isucon/isucon8-final/blob/master/docs/WEBAPP_SPEC.md) / [ISUBANK_SPEC](https://github.com/isucon/isucon8-final/blob/master/docs/ISUBANK_SPEC.md) / [ISULOGGER_SPEC](https://github.com/isucon/isucon8-final/blob/master/docs/ISULOGGER_SPEC.md) | – | [講評](https://isucon.net/archives/52598691.html) |
| 9 予選 | ISUCARI (フリマ) | [docs/manual.md](https://github.com/isucon/isucon9-qualify/blob/master/docs/manual.md) | [APPLICATION_SPEC](https://github.com/isucon/isucon9-qualify/blob/master/webapp/docs/APPLICATION_SPEC.md) / [EXTERNAL_SERVICE_SPEC](https://github.com/isucon/isucon9-qualify/blob/master/webapp/docs/EXTERNAL_SERVICE_SPEC.md) | [53555007](https://isucon.net/archives/53555007.html) | [講評](https://isucon.net/archives/53789931.html) / [FAQ gist](https://gist.github.com/catatsuy/8112c54ad9895d6ed7bb63057ef5f4dd) / [ベンチ解説 gist](https://gist.github.com/catatsuy/74cd66e9ff69d7da0ff3311e9dcd81fa) / [外部サービス解説](https://medium.com/@catatsuy/isucon9-qualify-969c3abdf011) |
| 9 本選 | ISUXPRESS (鉄道予約) | [docs/MANUAL.md](https://github.com/isucon/isucon9-final/blob/master/docs/MANUAL.md) | [webapp_spec](https://github.com/isucon/isucon9-final/blob/master/docs/webapp_spec.md) / [payment_spec](https://github.com/isucon/isucon9-final/blob/master/docs/payment_spec.md) | – | [講評](https://isucon.net/archives/53916974.html) |
| 10 予選 | ISUUMO (物件検索) | [gist progfay/25edb2a9](https://gist.github.com/progfay/25edb2a9ede4ca478cb3e2422f1f12f6) | – | [54753430](https://isucon.net/archives/54753430.html) | [講評](https://isucon.net/archives/55025156.html) |
| 10 本選 | XSUCON (ポータル) | [docs/manual.md](https://github.com/isucon/isucon10-final/blob/master/docs/manual.md)（本選レギュレーションも含む） | [docs/xsucon.md](https://github.com/isucon/isucon10-final/blob/master/docs/xsucon.md) | 同左 | [講評](https://isucon.net/archives/55140539.html) |
| 11 予選 | ISUCONDITION (IoT) | [docs/manual.md](https://github.com/isucon/isucon11-qualify/blob/main/docs/manual.md) | [docs/isucondition.md](https://github.com/isucon/isucon11-qualify/blob/main/docs/isucondition.md) | [55854734](https://isucon.net/archives/55854734.html) | [講評](https://isucon.net/archives/56044867.html) / [実践攻略法](https://isucon.net/archives/56082639.html) |
| 11 本選 | ISUCHOLAR (学内システム) | [docs/manual.md](https://github.com/isucon/isucon11-final/blob/main/docs/manual.md)（本選レギュレーションも含む） | [docs/isucholar.md](https://github.com/isucon/isucon11-final/blob/main/docs/isucholar.md) | 同左 | [講評](https://isucon.net/archives/56163308.html) |
| 12 予選 | ISUPORTS (マルチテナントSaaS) | [gist mackee/4320c189](https://gist.github.com/mackee/4320c18919c8f6f1867849378a17e651) | [webapp/specification.md](https://github.com/isucon/isucon12-qualify/blob/main/webapp/specification.md) | [56671734](https://isucon.net/archives/56671734.html) | [講評](https://isucon.net/archives/56850281.html) / [SQLiteのまま10万点](https://isucon.net/archives/56842718.html) |
| 12 本選 | ISU CONQUEST (放置ゲーム) | [gist shirai-suguru/770d30d1](https://gist.github.com/shirai-suguru/770d30d16688a07ba78e0a188cd99f9f) | [gist shirai-suguru/accb96c5](https://gist.github.com/shirai-suguru/accb96c5f86200b5c16e1d2a8b533cc1) | – | [講評](https://isucon.net/archives/56959385.html) |
| 13 | ISUPipe (ライブ配信) | [gist kazeburo/bccc2d2b](https://gist.github.com/kazeburo/bccc2d2b2b9dc307b5640ae855f3e0bf)（= repo docs/cautionary_note.md） | [gist kazeburo/70b352e6](https://gist.github.com/kazeburo/70b352e6d51969b214f919bcf0794ba6)（= repo docs/isupipe.md） | [57768216](https://isucon.net/archives/57768216.html) | [講評](https://isucon.net/archives/58001272.html) |
| 14 | ISURIDE (ライドチェア) | [docs/manual.md](https://github.com/isucon/isucon14/blob/main/docs/manual.md) | [docs/ISURIDE.md](https://github.com/isucon/isucon14/blob/main/docs/ISURIDE.md)（+ webapp/openapi.yaml） | [58657116](https://isucon.net/archives/58657116.html) | [講評](https://isucon.net/archives/58869617.html) |

各回のまとめページ（関連リンクの起点）: `https://isucon.net/` のサイドバーにある「ISUCONn まとめ」。

## 資料の取得方法（再取得用）

```sh
# 公式リポジトリ（docs/ 配下に当日マニュアルとアプリ仕様がある回が多い）
git clone --depth 1 --filter=blob:limit=2m https://github.com/isucon/isucon14.git

# gist で公開されているマニュアル
gh api gists/<gist_id> -q '.files[].content'

# isucon.net のブログ記事（本文は article-body クラス内）
curl -sL https://isucon.net/archives/<id>.html
```
