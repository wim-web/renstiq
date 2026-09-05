# renstiq

GitHub PRの確認・マージ・マージ後処理を、リポジトリごとのルールに沿って実行・記録するCLIです。PR情報をJSONで取得し、レビュー結果をJSONで渡してコメント・ラベル・マージを操作できます。

## インストール

Go 1.24以降とGitが必要です。macOS・Linuxに対応しています。ソースを取得したディレクトリで実行します。

```sh
go install .
renstiq version
```

Goのインストール先（`GOBIN`、未指定時は通常 `~/go/bin`）をPATHに追加してください。GitHub認証には `GH_TOKEN`、`GITHUB_TOKEN`、`gh auth token --hostname github.com` の順で使用します。

## 設定

```sh
renstiq init           # 共通設定を作成
renstiq init --repo .  # 対象repoのルートで実行
```

表示された共通設定ファイルの `discovery.include` に探索対象を指定し、repoの設定ファイルに必要なルールを追記してください。

## 使い方

利用するエージェントに、対象に合うskillを読み込ませてください。

- [単一repoの処理](skills/renstiq-single/SKILL.md)
- [複数repoの処理・集計](skills/renstiq-multi/SKILL.md)

コマンド一覧は `renstiq --help`、設定・判断の形式は `renstiq schema config`、`renstiq schema repo`、`renstiq schema decision` で確認できます。
