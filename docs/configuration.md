# 設定

処理ポリシーは各repoのルートに置く `renstiq.yaml` だけで決まる。PRの条件と `instructions` を `rules` の各項目にまとめ、上から最初に一致した有効な1件を選ぶ。共通ポリシー、設定の継承・合成、フィルタIDの参照は使わない。

## 設定ファイルとコマンド

- 各repoの `renstiq.yaml`：参加設定、処理ルール、マージ方法、保留時・後処理、API読み取りの再試行。
- `$XDG_CONFIG_HOME/renstiq/config.yaml`（未設定なら `~/.config/renstiq/config.yaml`）：リポジトリ探索の `discovery.include/exclude` だけ。

`config show` と `pr list` はrepo設定だけを読み、探索設定の有無・内容に依存しない。`--repo` は省略時 `.`。リポジトリルートで実行する。

```sh
renstiq init --repo .
renstiq config show
renstiq pr list
```

`init --repo .` はrepo設定を作成する。`init` は既定の場所に探索設定を作成する。`init --config /path/to/config.yaml` は指定したファイルに探索設定を作成する。どちらも全項目の説明・記入例をコメントで含み、既存ファイルは上書きしない。

```sh
renstiq discover --config /path/to/config.yaml
```

`--config` は `init` と `discover` だけで使用する。探索設定をPR処理に引き継ぐ必要はない。設定例は [config.example.yaml](config.example.yaml) と [renstiq.example.yaml](renstiq.example.yaml)。

## ルールの選択

各repoは `version: 2` と `enabled: true` を指定する。`enabled` がfalseまたは省略なら処理に参加しない。

```yaml
version: 2
enabled: true
merge:
  method: squash

rules:
  - id: major
    authors: [app/renovate, 'renovate[bot]']
    base_branches: [main]
    labels: [major]
    instructions: 調査をせずにスキップする。

  - id: minor-patch
    authors: [app/renovate, 'renovate[bot]']
    base_branches: [main]
    labels: [minor, patch]
    instructions: |
      CIのcheckがOKならマージする。
      Renovate以外の変更履歴、コメント等がある場合はマージしない。

  - id: other-renovate
    authors: [app/renovate, 'renovate[bot]']
    base_branches: [main]
    instructions: 調査をせずにスキップする。
```

majorとpatchの両ラベルが付いたPRは、先にある `major` の指示だけを使う。minor・patchは `minor-patch`、どれもないPRは最後の `other-renovate` になる。広い条件は後ろへ置く。Renovate以外の作者・main以外へのPRは、上の例ではどのルールにも一致せず対象外。

- `id` は `rules` 内で一意。英数字で始め、英数字・ハイフン・アンダースコア・ピリオドを使える。
- 項目の `enabled` は省略時true。falseなら評価しない。
- 有効なルールには、空白だけではない `instructions` が必須。選ばれた本文をそのまま使い、ほかの本文を追記・合成しない。
- どれにも一致しない場合は `excluded`。`rules` の省略・空配列・全件無効の場合も対象外。
- 上のルールが判定不能なら、後ろに一致するルールがあっても飛ばさない。必要な情報を取得して不一致と確定した場合だけ次へ進む。確定できなければ `unknown`。
- 一致したルールより後ろのルールのために、追加情報を取得することはない。

ルール内の条件はすべてAND。各許可リストは省略なら制限なし、明示した空配列 `[]` ならそのルールに一致する対象なし。

| 項目 | 判定 |
| --- | --- |
| `authors` | PR作者が指定した名前のいずれかに完全一致 |
| `labels` | 指定ラベルが1つ以上付いている。大文字・小文字を区別 |
| `base_branches` | マージ先ブランチの完全一致 |
| `head_branches` | PRブランチのglob |
| `commit_authors` | 全コミットの作者のGitHubログイン名が許可対象 |
| `files` | 全変更パスがrepo相対globのいずれかに一致。renameは旧名・新名両方 |
| `dependencies` | 全更新の依存名が許可対象 |
| `update_types` | 指定種別の更新が1件以上含まれる。他種別の混在も可 |

同じルールの別条件が不一致と確定していれば、情報不足の条件があってもそのルールは不一致になる。例えば作者不一致のルールのためにファイルを取得しない。ファイル取得後に不一致となり、次のルールにコミット情報が必要になった場合は、その情報を追加取得する。取得中のPR更新や整合性確認の失敗を、後続ルールで回避しない。

CI、draft、競合はCLIの固定の除外条件にしない。必要な確認はルールの `instructions` に書く。候補への選択はマージの許可を意味せず、AIが指示とGitHubのマージ条件を確認する。

依存名と更新種別は、RenovateのPR本文の Package / Update 表（Markdown・HTML）から読む。作者・タイトル・バージョン番号だけでは推測しない。表や必要な列が不足していれば、それを必要とするルールは判定不能になる。表を必要としない条件のルールは先頭にあれば選択できる。

対応する更新種別は `patch`、`minor`、`major`、`digest`、`pin`、`pinDigest`、`lockFileMaintenance`、`lockfileUpdate`、`replacement`、`rollback`、`bump`。replacementの旧名・新名は両方を依存名条件で確認する。

## マージ方法とAPI読み取り

`merge.method` は `squash` / `merge` / `rebase`。未指定の場合、AIは選ばれたルールの指示を実施したうえで不足を報告し、マージを保留する。

`github_api_read_retry` はこのrepoのAPI読み取りだけの再試行設定。省略・`{}` は最初の1回のみ。

- `max_attempts`：最初の試行を含めた上限（1〜100）。1なら再試行なし。
- `interval_seconds`：0〜86400秒、小数可。`max_attempts > 1` なら明示必須。0は即時再試行。
- `respect_retry_after`：trueの場合だけ、APIのRetry-Afterが設定間隔より長ければ待機を延ばす。

書き込みや後処理の再試行は、必要なら各 `instructions` に明記する。

## 保留時・後処理

`on_blocked`、`after_merge`、`after_repo` は各repoの指示の配列。各項目は `id`、`enabled`、`match`、`exclude`、`instructions` を持つ。各配列内でIDは一意。有効な項目には空白だけでないinstructionsが必要で、省略時のenabledはtrue。

これらは `rules` の先頭一致とは別に、該当するすべての有効項目を設定順に実施する。

- `on_blocked`：マージを見送るとき。
- `after_merge`：各PRのマージ成功を確認した直後。
- `after_repo`：今回の確定マージが1件以上ある場合に、各IDを一度。

`match` / `exclude` の条件は `changed_files_any`（変更パスのいずれかに一致するglob）、`dependencies`（依存名）、`update_types`。空でない条件同士はANDで、依存名と更新種別は同じ更新を評価する。matchした更新からexcludeに合う更新を除き、1つ以上残れば適用する。matchの省略・空条件は制限なし、excludeの省略・空条件は除外なし。

after_repoの条件はマージしたPRごとに評価し、いずれかが該当すれば実施する。別PRのファイルと依存を混ぜて判定しない。指示にはコマンド、実行場所、待機、失敗時の対応を書ける。CLIは実行せず、AIが結果をIDごとに報告する。

## 出力と確認

`pr list` は候補だけを返す。除外・判定不能の理由を調べる場合は `--all` を使う。`review` は選ばれた1ルールの `id` と `instructions` を持つ配列、`review_ids` はそのID1件。excluded・unknownではどちらも空配列になる。

```json
{
  "selection": "candidate",
  "review": [{"id": "minor-patch", "instructions": "CIのcheckがOKならマージする。"}],
  "review_ids": ["minor-patch"]
}
```

取得失敗は `errors` と非0終了で報告する。`complete: false` のときも取得できた候補は保持する。`open_pr_count` は選別前のopen PR総数で、nullは取得不完全を表す。AIは設定を再合成せず、出力の選択済み指示を使用する。

```sh
renstiq config show
renstiq pr list --all
renstiq schema repo
renstiq schema config
```

## 旧設定からの移行

探索ファイルの `defaults` をなくし、再試行・マージ方法・保留時・後処理を各repoの `renstiq.yaml` へ移す。`pull_requests.filters` と `review` は、条件と完成した指示を持つ `rules` に置き換える。優先したいルールを先にし、広いルールを最後に置く。

`inherit`、`filter_ids`、`filter_ids_mode` は削除する。共通・repoのinstructionsを合成していた場合は、必要な本文をrepo側に完成した形で記述する。旧形式は設定エラーとして扱い、暗黙の変換や旧共通設定からの補完は行わない。既存の `version: 2` は維持する。
