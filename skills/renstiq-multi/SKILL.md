---
name: renstiq-multi
description: renstiqで複数リポジトリのPR処理を進め、repoごとの結果を集計する。repo間の実行方法は利用側の追加指示に従い、指定がなければ直列で処理する。探索範囲の全repoや複数repoを指定したレビュー・マージ依頼で使う。
---

# 複数repoのPR処理

対象範囲と、調査のみか操作まで行うかを依頼から確認する。指定された共通設定・状態保存先・操作の許可範囲を全コマンドに引き継ぐ。repoごとに有効設定、run、判断JSON、進行状況を分ける。

## 実行時の追加指示

repo間の実行方法は、利用側から渡された追加指示に従う。実行方法の詳細は利用側の指示に委ねる。

追加指示がなければ、1repoの情報取得・レビュー・操作・終了処理を済ませてから次のrepoへ進む。

## 対象の確定

```sh
renstiq discover --all
```

探索結果を対象一覧として保持する。`enabled` は処理候補とし、無効・設定なし・除外・設定不正・取得失敗の理由も最終報告に残す。利用者が対象repoを限定している場合は、その指定と照合する。設定不正のrepoを飛ばしても、残りのrepoは続ける。

共通設定の探索対象が空なら対象は0件になる。探索範囲を推測で広げない。初期設定も依頼されている場合は `renstiq init` と `renstiq init --repo REPO_DIR` を使い、依頼された場所に設定する。

## 情報取得

確定した実行方法に従い、各repoの処理を次のコマンドから開始する。

```sh
renstiq inspect --repo REPO_DIR
```

`--config FILE` や `--state-dir DIR` が指定されている場合は、以後も同じ値を付ける。全repoの情報取得だけをまとめる場合は `renstiq inspect --all` も使える。このCLIコマンド自体は直列で動く。

- 終了コード1でもstdoutのJSONを読み、repoごとの取得結果を確認する。全体を失敗扱いにして終了しない。
- 各 `results[]` の `path`、`repo`、`run`、`config_digest`、`config`、`pull_requests` を対応付ける。取得に失敗したPRを未取得のままマージしない。
- 一括取得後にも別の操作でheadやbaseは変わり得る。各PRの処理時に必要な再取得・再レビューを行う。

## repoごとのレビューと操作

`renstiq schema decision` で判断形式を取得し、以下を各repoで行う。`REPO_DIR` はそのrepoのルート、`RUN_ID` はそのrepoのrun、`DECISION_FILE` はそのPRの判断JSONで置き換える。

1. 有効設定の `review.instructions` とルールの `instructions` に沿って、PR差分・必要なupstream情報・利用箇所・未解決要求を調査する。マージを妨げる条件があっても、要求された影響調査を省略しない。
2. 取得したrepo名・PR番号・head/base SHA・config digestと、判断・理由・根拠をJSONへ記述する。`review` と `updates` はschemaに従って実際の確認結果を入れる。設定や判断を別repoからコピーして補完しない。
3. `requires_review: true` のマージ後コマンドは、設定済みIDごとに実行の必要性と理由を記述する。判断JSONは対象repo外の一時ディレクトリにPRごとに保存する。
4. 調査のみの依頼ならここまでの結果を報告する。操作まで依頼されている場合は、必要なコメント・ラベル操作を `feedback`、マージ可能な判断を `merge` に渡す。

```sh
renstiq feedback --repo REPO_DIR --run RUN_ID --decision DECISION_FILE
renstiq merge --repo REPO_DIR --run RUN_ID --decision DECISION_FILE
```

互換性問題の保留理由と根拠は `feedback` でPRに残す。同等コメントの指定やラベルの解除は、当該PRの実際の内容と設定に基づいて判断する。CI待ち・通信失敗だけではコメントしない。schemaで表現できない依頼は制約として報告し、架空の更新や確認結果で通さない。

同じrepoのPRは1件ずつ処理し、`merge` が実行する `after_each_merge` の完了まで確認する。マージでbaseが進んだ場合や `review_required` が返った場合は、`inspect --repo REPO_DIR --run RUN_ID --pr PR_NUMBER` で再取得し、判断を更新する。別repoの失敗で、処理可能なrepoを打ち切らない。

## repoの終了と全体の集計

当該repoの対象PRを判断・処理し終えた時点で、そのrunを終了する。

```sh
renstiq post-merge --repo REPO_DIR --run RUN_ID --finish
renstiq status --repo REPO_DIR
```

- `--finish` が `after_repo` を実行する。開始後は同じrunに追加マージできない。調査のみの依頼では実行しない。
- 同期失敗・子コマンド失敗・実行結果不明なら、そのrepoの依存する後続処理を止め、他repoを続ける。状態削除・直接再実行・新しいrunの作成で失敗記録を回避しない。run打切りの `abandon` は、手動確認後の明示的な依頼に従う。
- 一括終了の `post-merge --all --finish` は、今回処理していない既存runも選び得る。通常は上記のように今回扱ったrepoとrunを指定して終了する。
- 全repoが依頼対象なら `renstiq status --all` で集計する。対象が限定されていれば、そのrepoの `status` を集計する。
- 最初の対象一覧と結果を照合し、全repoのPR判断、実際のマージ、コメント・ラベル、マージ後処理、対象外・失敗・未完了の理由を報告する。終了コードだけで操作結果を推測しない。PR URLと確認用の `status` コマンドを添える。
