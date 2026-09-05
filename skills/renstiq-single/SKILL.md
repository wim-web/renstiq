---
name: renstiq-single
description: renstiqで単一リポジトリのPRをレビューし、依頼範囲に応じてコメント・ラベル・マージ・マージ後処理を進める。repoやPRを個別に指定した依頼で使う。
---

# 単一repoのPR処理

対象repoと、調査のみか操作まで行うかを依頼から確認する。既に指定された設定・実行場所・許可範囲を引き継ぐ。調査のみなら情報取得と報告までにする。

以下の `REPO_DIR` は対象repoのルート、`RUN_ID` はCLIが返すrun、`DECISION_FILE` は調査結果のJSONファイルを表す。`--config FILE` や `--state-dir DIR` が指定されている場合は、後続コマンドにも同じ値を付ける。

## 情報取得とレビュー

```sh
renstiq inspect --repo REPO_DIR
renstiq schema decision
```

- PRが限定されていれば `inspect` に `--pr PR_NUMBER` を付ける。
- `results[]` の `repo`、`run`、`config_digest`、有効設定 `config`、`pull_requests` を読む。`repo` はGitHub上の名前、`REPO_DIR` はローカルパスなので区別する。
- `config.review.instructions` と一致するルールの `instructions` に従い、差分・必要なupstream情報・repo内の利用箇所・未解決のレビューやコメントを調査する。`merge_blockers` があるPRも、要求された調査を省略しない。
- 判断JSONは取得したschemaに従う。`repo`、PR番号、`head_sha`、`base_sha`、`config_digest` を取得結果から写し、判断・理由・根拠と実際の確認結果を記述する。現在のschemaが要求する `updates` には関連する全変更ファイルを含め、renameは旧名も扱う。入力形式で表せない変更を通すために架空の更新や確認結果を記入しない。
- `post_merge` の `requires_review: true` のコマンドについては、設定済みIDごとに実行の必要性と理由を記述する。調査結果から新しいコマンドを作らない。
- 判断JSONは対象repo外の一時ディレクトリに保存する。repo内に未追跡ファイルを作ると、マージ後処理の同期を妨げる。

初期設定の作成も依頼された場合は `renstiq init --repo REPO_DIR` を使う。設定不足を補うために、別repoの設定を流用したり参加を勝手に有効化したりしない。

## 操作

必要な操作を、依頼範囲と有効設定に従って実行する。

```sh
renstiq feedback --repo REPO_DIR --run RUN_ID --decision DECISION_FILE
renstiq merge --repo REPO_DIR --run RUN_ID --decision DECISION_FILE
```

- `hold` の判断は `feedback`、`merge` の判断は `merge` に渡す。マージ前に必要なコメント・ラベル操作があれば先に `feedback` を実行する。
- 互換性問題を保留する場合は、理由と根拠を判断JSONに含める。`feedback` がPRコメントへ反映する。CI待ちや状態取得の通信失敗だけを理由にコメントしない。
- 同等の既存コメントは `equivalent_comment_id` で指定できる。既存コメントの更新とラベル操作は、schemaと設定で許可された範囲で指定する。
- `merge` は最新状態を検証し、マージ後に該当する `after_each_merge` コマンドも実行する。その完了を確認してから次のPRへ進む。
- マージでbaseが進んだ場合や `review_required` が返った場合は、`inspect --repo REPO_DIR --run RUN_ID --pr PR_NUMBER` で情報を再取得し、変更された状態を調査して判断JSONを作り直す。SHAだけを書き換えて古い判断を流用しない。

## 終了とエラー

対象PRの判断・操作が完了したら、そのrunを閉じる。

```sh
renstiq post-merge --repo REPO_DIR --run RUN_ID --finish
renstiq status --repo REPO_DIR
```

- `--finish` は該当する `after_repo` コマンドを実行する。開始後は同じrunに追加マージできないため、PR処理の途中では呼ばない。調査のみの依頼では呼ばない。
- CI完了待ちと状態取得のretryはCLIに任せる。操作がエラーになったらJSONと `status` を読み、マージ・コメント・ラベル・マージ後処理それぞれの結果を確認する。終了コードだけで「マージされなかった」と判断しない。
- 同期失敗や子コマンドの失敗・実行結果不明があれば、依存する後続処理を止める。コマンドの直接再実行や状態ファイルの削除で制約を回避しない。`abandon` は手動確認後のrun打切りを明示的に依頼された場合に使う。
- 最後にPRごとの判断と根拠、実際に行われた操作、マージ後処理、エラーと残作業を報告する。利用者が確認できるPR URLと `renstiq status --repo REPO_DIR` を添える。
