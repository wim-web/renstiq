# renstiq

GitHub PRの確認・マージ・マージ後処理を、リポジトリごとのルールに沿って実行・記録するCLIです。PR情報をJSONで取得し、レビュー結果をJSONで渡してコメント・ラベル・マージを操作できます。

## インストール

Go 1.24以降とGitが必要です。macOS・Linuxに対応しています。ソースを取得したディレクトリで実行します。

```sh
go install .
renstiq version
```

Goのインストール先（`GOBIN`、未指定時は通常 `~/go/bin`）をPATHに追加してください。GitHub認証には `GH_TOKEN`、`GITHUB_TOKEN`、`gh auth token --hostname github.com` の順で使用します。

リリース版を利用している場合は、GitHub Releasesの最新バイナリへ自己更新できます。

```sh
renstiq update
```

`update` は現在のOS/architectureに対応するarchiveと `checksums.txt` を取得し、SHA-256を検証してから実行中の `renstiq` バイナリを置換します。対応対象は macOS/Linux の amd64/arm64 です。

## シェル補完

fish では、現在のセッションに補完を読み込めます。

```fish
renstiq completion fish | source
```

以降のセッションでも有効にするには、補完ファイルを保存します。

```fish
mkdir -p ~/.config/fish/completions
renstiq completion fish > ~/.config/fish/completions/renstiq.fish
```

`renstiq ` や `renstiq inspect --` の後で Tab を押すと候補が表示されます。`--repo`・`--state-dir`・`--config`・`--decision` はパス、`schema` はスキーマの種類を補完します。補完時に GitHub 認証や設定の読み込みは行いません。

bash・zsh・PowerShell 用も出力できます。読み込み方法は `renstiq completion <shell> --help` で確認してください。

## 設定

```sh
renstiq init           # 共通設定を作成
renstiq init --repo .  # 対象repoのルートで実行
```

表示された共通設定ファイルの `discovery.include` に探索対象を指定し、repoの設定ファイルに必要なルールを追記してください。

## 使い方

`discover` はデフォルトで `enabled` のリポジトリだけを表示します。無効・設定なし・除外・エラーも含めて確認するには `--all` を付けます。

```sh
renstiq discover
renstiq discover --all
```

利用するエージェントに、対象に合うskillを読み込ませてください。

- [単一repoの処理](skills/renstiq-single/SKILL.md)
- [複数repoの処理・集計](skills/renstiq-multi/SKILL.md)

コマンド一覧は `renstiq --help`、設定・判断の形式は `renstiq schema config`、`renstiq schema repo`、`renstiq schema decision` で確認できます。
