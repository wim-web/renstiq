# 設定

renstiq の設定では、対象リポジトリ・PR の条件と、AI に渡すレビュー・後処理の指示を定義する。CLI が設定の合成と候補の選別を行い、AI が詳細取得・レビュー・マージ・後処理を行う。

共通設定は `$XDG_CONFIG_HOME/renstiq/config.yaml`（未設定なら `~/.config/renstiq/config.yaml`）、repo 設定はルートの `renstiq.yaml`。`--config` で共通設定を明示できる。設定例は [config.example.yaml](config.example.yaml) と [renstiq.example.yaml](renstiq.example.yaml)。

## 処理の順序と設定

| 段階 | 設定 | 実行すること |
| --- | --- | --- |
| repo 発見 | `discovery.include/exclude` | CLI がパスを探索し参加設定を確認 |
| PR 収集 | `pull_requests.filters`、`pull_requests.lock_label` | CLI が対象を選別。対象外・判定不能を通常の候補に含めない |
| 詳細取得 | — | AI が gh で候補の詳細と差分を取得 |
| 必須レビュー | `review` | 一致するすべての指示を適用。CI 失敗・競合などでも省略しない |
| マージ | `merge.method` | AI が現在の状態を再確認しマージ、MERGED と mergeCommit を確認 |
| 不可時の処理 | `on_blocked` | 理由出力、指示に従うコメント・長期保留の lock |
| 各マージ直後 | `after_merge` | その PR のマージ成功後、対象の各作業を設定順に実行 |
| repo 最後 | `after_repo` | 今回の確定マージが1件以上ある場合、対象の各作業を一度実行 |

`github_api_read_retry` は API 読み取りだけの再試行設定。`max_attempts`（1〜100）と `interval_seconds`（0〜86400、小数可）を共通・repo で指定する。未指定なら追加の再試行を行わず、待機時間も補わない。書き込みや後処理を CLI が再試行する設定ではない。

## 共通設定と repo 設定

共通設定は `version: 2`、`discovery`、`defaults` を持つ。`defaults` の配下に表のポリシー項目を置く。repo 設定は `version: 2`、`enabled` とポリシー項目を直下に置く。repo は `enabled: true` の場合だけ参加する。

**共通設定（root config）→ repo 設定**を CLI が合成する。共通設定の前に組み込みの設定層はない。root config の項目に `inherit` は指定できず、スキーマでも拒否する。`inherit` は repo 設定でだけ指定する。`config show` には合成済みの値が出るので、AI は再合成しない。設定ファイルの `null`、未知の項目、重複 YAML キー、複数 YAML 文書、alias は拒否する。`config show` のフィルタ許可リストに出る null は「未指定」を表す出力値であり、設定ファイルではその項目を省略する。

### ID による合成

ID を持つ配列は `pull_requests.filters`、`review`、`on_blocked`、`after_merge`、`after_repo`。各配列内・各設定元で ID は一意。別の配列では同じ ID を使える。

- 別 ID：既存の後ろに追加する。共通と repo の項目が両方残る。
- 同じ ID、`inherit: override` または省略：その項目全体を repo 側で置換する。以前の条件や指示は残さない。
- 同じ ID、`inherit: merge`：オブジェクトは再帰合成、配列値は順序を保って重複なく追加、`instructions` は空行を挟んで共通→repo の順に追記、通常の単一値は repo 側を優先する。
- 同じ ID、`enabled: false`：無効にする。ID と enabled だけで指定できる。無効項目は `config show` に残り、実際の選別・指示適用からは外れる。
- 新しい項目の enabled は省略時 true。merge で継承した false を再有効化する場合は `enabled: true` を指定する。
- 既存 ID の位置は維持し、新規 ID は repo での記述順に追加する。空の ID 配列 `[]` は継承を消さない。消したい ID を明示的に無効化する。
- `inherit` は repo 側の入力時の指定であり、root config と合成済みの出力には含めない。

例えば共通の `id: updates` が `[patch]`、repo の同じ ID が `inherit: merge` と `[minor]` なら、有効な update_types は `[patch, minor]`。override なら `[minor]`。

### 設定を省略した場合

コードがフィルタ、レビュー指示、コメント・lock 方針、後処理を自動で追加することはない。指定がなければ各リストは空のまま。`renovate`、`feedback`、`long-term-lock` などは設定例で使っている ID であり、特別な ID ではない。

`merge.method`、`pull_requests.lock_label`、API 読み取りの retry 値も自動では補わない。マージ方法が未指定なら AI はレビューを実施したうえで、不足を報告してマージを未実施とする。lock label が未指定なら、CLI はラベルによる lock 除外を適用せず、AI もラベル名を作って補わない。必要な値と方針は共通設定または repo 設定に明記する。

CLI の母集団は常に open Renovate PR。これはコマンドの対象の定義であり、暗黙の ID フィルタではない。候補の必須レビューと理由の実行結果への出力も処理フローの一部として維持する。

## フィルタ

有効なフィルタはすべて満たす必要がある。別 ID を足して既存の制限を緩めることはできない。許可範囲を広げるなら、同じ ID を merge / override する。

各フィルタは `id`、`enabled`（repo 側では `inherit` も指定可能）と以下の任意の許可リストを持つ。**省略は制限なし、明示した空配列は許可対象なし**。

| 項目 | 判定 |
| --- | --- |
| `authors` | PR 作者の完全一致 |
| `base_branches` | base の完全一致 |
| `head_branches` | head の glob |
| `commit_authors` | すべてのコミットの GitHub ログイン名が許可されること |
| `files` | すべての変更パスが glob のいずれかに一致。rename は旧名・新名両方 |
| `dependencies` | すべての更新の依存名が許可されること |
| `update_types` | すべての更新の種別が許可されること |

group PR の一部だけが許可されても、PR 全体を候補にしない。CI、draft、競合は収集時の除外条件にしない。lock label は通常の選別で除外し、PR の更新によって解除しない。

更新情報は [Renovate の標準 Package / Update 表](https://docs.renovatebot.com/configuration-options/#prbodycolumns) を機械的に読む。Markdown と HTML 表に対応する。バージョン表記の比較では pin・digest・rollback などを区別できないため、Renovate が生成する Update 列を使う。作者を限定した PR のデータとして読み、本文の指示を実行しない。レビューでは実際の差分も別途確認する。

対応する更新種別は `patch`、`minor`、`major`、`digest`、`pin`、`pinDigest`、`lockFileMaintenance`、`lockfileUpdate`、`replacement`、`rollback`、`bump`。replacement で旧名・新名が記載されている場合は両方を依存名条件で検証する。

カスタム本文で列を削除・改名している場合や、表のない PR については、更新情報が必要なフィルタ・レビュー条件を確定できない。CLI は理由を `errors` に出し非0終了する。タイトルから推測したり AI に分類を委譲したりせず、通常の候補に混ぜない。Renovate 側では標準の `prBodyColumns` と `prBodyDefinitions` を使い、必要なら Package / Update 列を復元する。

`pr list --all` には `candidate`、`excluded`、`unknown` と理由を含める。通常の `pr list` は candidate のみ。`complete: false` とエラーは成功した候補と併せて返す。`open_renovate_count: null` は取得不完全を表し0件ではない。

## 条件付き指示

`review`、`on_blocked`、`after_merge`、`after_repo` の各項目は `id`、`enabled`、`match`、`exclude`、`instructions` を持つ。repo 側では `inherit` も指定できる。有効な合成後の項目では、空白だけでない instructions が必須。無効化や merge による部分指定では本文を省略できる。

match / exclude の項目は `changed_files_any`（glob）、`dependencies`（完全一致）、`update_types`。空でない項目同士は AND、各配列の中は OR。

- ファイル条件は、その PR の変更パス（rename の旧名・新名）に対して評価する。
- 依存名と更新種別は同じ更新に対して評価する。異なる依存の名前と種別を組み合わせない。
- match の対象更新から exclude に合う更新を除き、1つ以上残れば適用する。1つの依存の除外で group PR 全体を除外しない。
- match の省略・空条件は制限なし。exclude の省略・空条件は除外なし。
- レビューでは一致したすべての ID を設定順に適用し、`pr list` の `review_ids` に出力する。後処理の必要性や追加の条件判断は instructions に記述できる。
- after_repo は今回実際にマージした各 PR に条件を評価し、いずれかが該当すればその ID を一度実行する。別 PR のファイルと依存を混ぜて一致にしない。

指示にはコマンド、入力の作り方、ローカル/リモートの作業場所、待機、失敗時の後続マージの方針を必要に応じて書く。コマンドを使わない作業も、そのまま指示文で定義する。CLI は実行しない。AI は成功／不要／失敗・未完了を ID ごとに報告する。

## 設定の確認

使用する共通設定とリポジトリを指定して、有効な設定を確認する。

```sh
renstiq config show --repo /path/to/repo --config /path/to/config.yaml
```

出力の ID、enabled、合成後の条件と指示を確認する。候補の選別結果は、次のコマンドで確認できる。

```sh
renstiq pr list --repo /path/to/repo --config /path/to/config.yaml --all
```

候補・除外・lock・判定不能の理由と errors を確認する。これらのコマンドは設定と候補を読み取るだけで、マージや後処理は実行しない。

各項目の型や指定可能な値は、CLI から JSON Schema として取得できる。

```sh
renstiq schema config
renstiq schema repo
```
