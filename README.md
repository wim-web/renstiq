# renstiq

対象repoの探索、有効設定の解決・検証、open Renovate PRの候補取得を行うCLIです。設定と候補をJSONでAIへ渡し、詳細調査・マージ判断・操作・後処理はAIが担当します。

## インストール

```sh
go install .
renstiq version
```

## GitHub認証

`GH_TOKEN`、`GITHUB_TOKEN`、`gh auth token --hostname github.com` の順で使用します。

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
