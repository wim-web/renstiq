# 実装の境界とテスト

## CLIとユースケース

`main.go` はシグナル処理と `RunCLI` の呼び出しだけを担当する。
`cli.go` の一つのレジストリが、`update` を含む全コマンドのルーティングと
トップレベルのヘルプを定義する。

各 `cli_*.go` は、そのコマンド専用の `flag.FlagSet` を作り、型付きの入力を
返す。全フラグを登録してから許可表で排除する方式は使わない。
`feedback` と `merge` だけは入力構文が同一なので `reviewOptions` を共有し、
それぞれ `FeedbackRequest` / `MergeRequest` に変換する。

引数解析・必須条件・排他条件・明示的な空値の検証が完了するまでは、設定、
decisionファイル、GitHub認証、リポジトリ、状態ファイルにアクセスしない。
`--pr` を明示した場合は正の数が必要。

コマンドは必要なユースケースの関数だけを受け取る。CLIの結果表示と終了コードは
`cli_output.go`、decision入力のファイル操作は `cli_input.go` に置く。
新しいコマンドを追加する際は、そのParser・Request・ハンドラーとレジストリの
一項目を追加する。既存コマンドの許可表や共通実行関数は変更しない。

## Applicationとロックの寿命

`Application` は、型付きRequestから必要な設定と対象を取得し、リポジトリ単位の
セッションを開く。`withRepository` / `visit` が共通化するのはリポジトリ識別と
ロックの寿命だけであり、コマンド名による業務分岐は持たない。
`inspect` の候補選択・PR評価は `Inspector` に置く。

`status --repo` と `abandon` は復旧用の操作なので、共通設定・repo設定・GitHub認証を
必要としない。`status --all` は探索のため共通設定を読む。
曖昧な無視を避けるため、`status --config` は `--all` と組み合わせる。
`abandon` は設定を使わないため `--config` を受け付けない。

ロックは状態の読み込み時に取得し、外部操作と結果保存が終わるまで保持する。
単に `Load` と `Save` を別々にロックする実装への置き換えは禁止する。

## 外部操作の契約

`Engine` は以下のサービスを組み立てた薄い入口に限定する。

- `FeedbackService` は検証・Decision記録・結果集約を行い、コメントとラベルを
  `CommentService` / `LabelService` に委譲する。
- `MergeService` は新規マージと再開の手順を扱う。既存結果の照合は
  `MergeReconciler`、ブランチ削除は `BranchCleaner` が担当する。
- `PostMergeService` は各マージ後・repo完了時の処理順序とRunの終了を扱い、
  個別コマンドの実行を `PostExecutor` に委譲する。

利用側に必要な役割別interfaceを定義する。`remotePorts` はcomposition rootで
実装を配線するためだけの集合で、各サービスはその全体に依存しない。
GitHubのURL・HTTPメソッド・wire JSON・HTTPエラー分類は
`github_operations.go` に閉じ込める。

明確な書き込み拒否は `RejectedWrite`、それ以外の書き込みエラーは結果未確定として
扱う。コメント、ラベル、マージ、ブランチ削除では確認方法と再送条件が異なるため、
これらを汎用的な自動retryに統合しない。

## Runと永続化

`State` / `Run` はRun作成条件、設定digest照合、終了への遷移、操作記録、abandonの
業務ルールを持つ。`RunSession` がそれらの操作と `Journal.Save` を調整する。
時刻とRun ID生成は注入する。`Store` はファイルの読み書き、排他制御、atomic renameと
fsyncだけを担当し、Runの作成・終了ルールを持たない。

重要な順序は次の通り。

1. 外部操作の意図を状態に記録し、永続化に成功してから外部操作する。
2. 結果を保存できなかった場合、再起動時には最後に永続化できた状態から照合する。
3. 未確定の書き込みを無条件に再送しない。後処理の子プロセスは自動再実行しない。
4. 後処理は `pending保存 → checkout同期 → ログ作成 → running保存 → 子プロセス実行
   → ログ同期 → 結果保存` の順序を保つ。

後処理の `CommandRunner`、`LogStore`、checkout同期、時刻は独立した依存。
`ExecRunner` がプロセスグループの停止や終了コードを処理し、`FileLogs` がログを保存する。
`PostExecutor` は `Store.File` や `GitHub.Log` を参照しない。

## テストの分担

Parserテストは外部依存を一切渡さず、引数エラーの種類・内容まで確認する。
ハンドラーテストは型付きRequestへの変換と出力を確認する。
ユースケースのテストは小さなfakeとメモリ上のJournalを使う。
Journalのfakeは実行中の状態とは別に永続化済みsnapshotを保持するため、保存失敗後に
本当に以前の状態へ戻して「二重送信・二重起動しない」を確認できる。

既存のHTTPサーバー、実ファイル、Git checkout、子プロセスを使うテストは
adapterの結合テストとして維持する。HTTPのJSONやファイル形式を変更した際も、
ユースケースの単体テストまでその詳細に依存させない。

```sh
go test -race ./...
go vet ./...
go build -o renstiq .
```
