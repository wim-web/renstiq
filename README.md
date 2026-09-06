# renstiq

対象repoの探索、有効設定の解決・検証、open Renovate PRの候補取得を行うCLIです。設定と候補をJSONでAIへ渡し、詳細調査・マージ判断・操作・後処理はAIが担当します。

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

`renstiq ` や `renstiq pr list --repo . --` の後で Tab を押すと候補が表示されます。`--repo`・`--config` はパス、`schema` はスキーマの種類を補完します。補完時に GitHub 認証や設定の読み込みは行いません。

bash・zsh・PowerShell 用も出力できます。読み込み方法は `renstiq completion <shell> --help` で確認してください。

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

```sh
renstiq discover --all
renstiq config show --repo .
renstiq pr list --repo . --all
```

`config show` と `discover` はGitHub認証・通信なしで動きます。`pr list` はrepo設定の有効化が必要です。`--all` は指定repo内の全open Renovate PRを設定による除外も含めて表示します。通常表示にも取得不足の `unknown` は残ります。`candidate` はマージ許可ではありません。

取得失敗は `complete: false` とエラーを返し、成功部分をJSONに残します。open数を確定できなければ `open_renovate_count` はnullです。終了コードは成功0、取得・入出力失敗1、引数・設定不正2です。

コマンド一覧は `renstiq --help`、設定の全項目は `renstiq schema config`・`renstiq schema repo`、出力形式は `renstiq schema config-show`・`renstiq schema pr-list`・`renstiq schema discover`・`renstiq schema result`（init）で確認できます。設定は組み込み値→共通defaults→repoの順にオブジェクトを再帰上書きし、配列を置換します。nullは不正です。空配列の意味は各項目のschemaに記載しています。

CLIと同梱skillsは同時に切り替えてください。旧stateは利用・削除しません。旧stdin自動注入やcheckout自動同期に依存する後処理は、入力と操作を明示する設定へ移行する必要があります。repo固有skillの条件もconfigへ移し、未移行のまま運用完了と扱わないでください。
