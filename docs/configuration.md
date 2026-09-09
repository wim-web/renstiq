# 設定

renstiq の設定では、対象リポジトリ・PR の条件と、AI に渡すレビュー・後処理の指示を定義する。CLI が設定の合成と候補の選別を行い、AI が詳細取得・レビュー・マージ・後処理を行う。

共通設定は `$XDG_CONFIG_HOME/renstiq/config.yaml`（未設定なら `~/.config/renstiq/config.yaml`）、repo 設定はルートの `renstiq.yaml`。`--config` で共通設定を明示できる。設定例は [config.example.yaml](config.example.yaml) と [renstiq.example.yaml](renstiq.example.yaml)。

`renstiq init` で共通設定、`renstiq init --repo .` で repo 設定を作成できる。どちらも全設定項目の説明・記入例をコメントで含むので、必要なブロックを親の行からコメント解除して編集する。例は生成時には無効で、既存のファイルは上書きしない。

## 処理の順序と設定

| 段階 | 設定 | 実行すること |
| --- | --- | --- |
| repo 発見 | `discovery.include/exclude` | CLI がパスを探索し参加設定を確認 |
| PR 収集 | `pull_requests.filters` | CLI が対象を選別。対象外・判定不能を通常の候補に含めない |
| 詳細取得 | — | AI が gh で候補の詳細と差分を取得 |
| レビュー | `review` | 設定に一致するすべての指示を適用。CI 失敗・競合などでも省略しない |
| マージ | `merge.method` | AI が現在の状態を再確認しマージ、MERGED と mergeCommit を確認 |
| 不可時の処理 | `on_blocked` | 理由出力、指示に従うコメントなど |
| 各マージ直後 | `after_merge` | その PR のマージ成功後、対象の各作業を設定順に実行 |
| repo 最後 | `after_repo` | 今回の確定マージが1件以上ある場合、対象の各作業を一度実行 |

`github_api_read_retry` は API 読み取りだけの再試行設定。全体を省略、または合成後に `{}` なら、各 API 読み取りは最初の1回だけ実行し、失敗しても追加の再試行や待機を行わない。

- `max_attempts`（1〜100）は最初の呼び出しを含む試行上限。1なら再試行なし。
- `max_attempts` が2以上の場合、`interval_seconds`（0〜86400、小数可）の明示が必須。0秒で直ちに再試行したい場合も `0` を指定する。間隔だけの指定は設定エラー。
- `respect_retry_after: true` を明示した場合だけ、GitHub の `Retry-After` ヘッダ（秒数）が設定間隔より長ければ待機を延ばす。省略・falseなら指定した間隔をそのまま使う。
- 共通・repo の部分指定は合成後に必須値を検証する。明示した `interval_seconds: 0` は `config show` にも0として残し、省略と区別する。

書き込みや後処理を CLI が再試行する設定ではない。

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

コードがフィルタ、レビュー指示、コメント方針、後処理を自動で追加することはない。指定がなければ各リストは空のまま。`renovate`、`feedback` などは設定例で使っている ID であり、特別な ID ではない。

`merge.method`、API 読み取りの retry 値も自動では補わない。マージ方法が未指定なら AI は設定されたレビューを実施したうえで、不足を報告してマージを未実施とする。必要な値と方針は共通設定または repo 設定に明記する。

`pull_requests.lock_label` は廃止した。共通・repo 設定に残っていれば設定エラーになるため削除する。旧 lock ラベルによる自動除外は行わず、ラベル条件は `filters.labels` で指定する。既存の `on_blocked` に lock ラベルを付ける指示があれば、あわせて削除する。

CLI は指定repoのopen PRを取得する。作者の制限は `filters.authors` だけで決まり、Renovate専用の固定判定はない。作者条件を省略すれば、人間や他のbotが作ったPRも候補になり得る。Renovateに限定したい場合は、設定例のように各経路へ `authors: [app/renovate, 'renovate[bot]']` を指定する。

レビュー指示も設定からだけ決まり、`review` が空ならCLI・skillが互換性調査や独自のCI基準を追加しない。マージ時には、設定された指示に加えてGitHub側のマージ条件を満たす必要がある。

## フィルタ

**同じフィルタ内の条件は AND、別 ID の有効なフィルタ同士は OR**。1つのフィルタを満たせば候補になる。共通設定から継承したフィルタと repo 側で追加したフィルタも OR で扱う。ID による merge / override / 無効化は、この判定とは独立した設定の合成方法である。

別 ID を追加すると、候補になる経路が増える。作者や base などを全経路で制限したい場合は、その条件を各フィルタに指定する。共通設定に作者・base だけのフィルタを残したまま更新種別のフィルタを追加すると、共通のフィルタだけで候補になれる。継承した経路の条件を変更する場合は同じ ID を merge / override し、使わない経路はその ID を無効化する。

各フィルタは `id`、`enabled`（repo 側では `inherit` も指定可能）と以下の任意の許可リストを持つ。**省略は制限なし、明示した空配列は許可対象なし**。

空の許可リストを持つフィルタは一致しないが、別のフィルタが一致すれば候補になる。有効なフィルタが0件の場合は、追加の制限を設けない。

| 項目 | 判定 |
| --- | --- |
| `authors` | PR 作者の完全一致 |
| `labels` | 指定名のラベルが PR に1つ以上付いていること。大文字・小文字を区別する完全一致 |
| `base_branches` | base の完全一致 |
| `head_branches` | head の glob |
| `commit_authors` | すべてのコミットの GitHub ログイン名が許可されること |
| `files` | すべての変更パスが glob のいずれかに一致。rename は旧名・新名両方 |
| `dependencies` | すべての更新の依存名が許可されること |
| `update_types` | 指定した種別の更新が PR に1件以上含まれること |

`labels: [dependencies, security]` は、どちらかのラベルが付いた PR に一致する。PR に他のラベルが付いていてもよい。省略はラベルによる制限なし、`labels: []` は不一致。ラベル条件も同じフィルタの他の条件とは AND で評価する。

```yaml
pull_requests:
  filters:
    - id: labeled-updates
      labels: [dependencies, security]
      base_branches: [main]
```

ラベルを取得できない場合はその条件を判定不能にし、同じフィルタの別条件が不一致ならフィルタ全体は不一致にする。ラベル条件を持たない別のフィルタが一致すれば候補にできるが、`review.match.filter_ids` がラベル条件のフィルタを参照していてレビュー適用を確定できなければ unknown にする。

`update_types: [minor]` は minor 更新を含む PR に一致し、patch や major が混在していても一致する。`update_types: [minor, patch]` はどちらかの更新が1件以上あれば一致する。省略は更新種別による制限なし、`update_types: []` は不一致。更新情報が不完全なら判定不能とする。

group PR でも、1つのフィルタのすべての条件を満たす必要がある。`files` は全変更パス、`commit_authors` は全コミット作者、`dependencies` は全更新の依存名を確認する。別のフィルタから一部の条件だけを組み合わせることはできない。CI、draft、競合は収集時の除外条件にしない。open PRであることは全フィルタに共通で要求する。

例えば、次の2つのフィルタは「patch/minor の更新」または「go.mod/go.sum だけの更新」を候補にする。後者は更新種別を制限しないため、`Update` 列がなくてもファイル条件を確認できれば候補になる。

```yaml
pull_requests:
  filters:
    - id: versioned
      authors: [app/renovate, 'renovate[bot]']
      base_branches: [main]
      update_types: [patch, minor]
    - id: go-modules
      authors: [app/renovate, 'renovate[bot]']
      base_branches: [main]
      files: [go.mod, go.sum]
```

各フィルタは一致・不一致・判定不能を返す。いずれかが一致すれば候補、すべて不一致なら除外、一致がなく判定不能が残る場合は unknown とする。同じフィルタ内では、ある条件の不一致が確定していれば、他の条件が判定不能でもそのフィルタは不一致になる。理由はフィルタの ID とともに出力する。

依存名・更新種別の条件は [Renovate の標準 Package / Update 表](https://docs.renovatebot.com/configuration-options/#prbodycolumns) を機械的に読む。Markdown と HTML 表に対応する。バージョン表記の比較では pin・digest・rollback などを区別できないため、表の Update 列を使う。作者の制限は独立したフィルタ条件であり、表があることから作者を推測しない。本文はデータとして読み、指示を実行しない。表のない人間・他botのPRも、依存名・更新種別を必要としない条件で選別できる。

対応する更新種別は `patch`、`minor`、`major`、`digest`、`pin`、`pinDigest`、`lockFileMaintenance`、`lockfileUpdate`、`replacement`、`rollback`、`bump`。replacement で旧名・新名が記載されている場合は両方を依存名条件で検証する。

カスタム本文で列を削除・改名している場合や、表のない PR については、更新情報が必要なフィルタを判定不能とする。更新情報を必要としない別のフィルタが一致すれば候補にできる。それ以外は理由を `errors` に出し非0終了する。CLI はタイトルやバージョン番号から更新種別を推測しない。必要な列を復元する場合は Renovate 側の `prBodyColumns` と `prBodyDefinitions` を確認する。

フィルタの一致後も、適用するすべての `review` を確定するために必要なファイル・更新情報は確認する。`review.match.filter_ids` が別のフィルタを参照していれば、その判定に必要なファイル・コミット情報も取得する。レビュー適用の確定に必要な情報が不足していれば unknown とする。PR の識別情報不足、取得中の PR 更新や整合性確認の失敗も、別のフィルタの一致では解消しない。実際に行った API 読み取りの失敗は `errors` と非0終了で報告しつつ、別の取得成功データで確定できた候補は保持する。

通常の `pr list` は candidate のみ。調査用の `pr list --all` は、作者条件を含むフィルタ不一致・判定不能も含め、取得したopen PRすべてを返す。`selection` は `candidate`、`excluded`、`unknown` で、対象外には理由が付く。skillも通常は候補だけを取得し、除外理由の調査が必要な場合だけ `--all` を使う。

`complete: false` と `errors` は通常出力でも成功した候補と併せて返す。`open_pr_count` は作者・フィルタによる選別前のopen PR総数。`null` は取得不完全を表し0件ではない。従来の `open_renovate_count` は `open_pr_count` に置き換えた。

## 条件付き指示

`review`、`on_blocked`、`after_merge`、`after_repo` の各項目は `id`、`enabled`、`match`、`exclude`、`instructions` を持つ。repo 側では `inherit` も指定できる。有効な合成後の項目では、空白だけでない instructions が必須。無効化や merge による部分指定では本文を省略できる。

match / exclude の項目は `changed_files_any`（glob）、`dependencies`（完全一致）、`update_types`。空でない項目同士は AND、各配列の中は OR。

`review.match` では追加で `filter_ids` を指定できる。参照先は合成後の `pull_requests.filters` の ID で、存在しない ID は設定エラーになる。有効な参照先のいずれかに一致すれば、この条件を満たす。候補選別と同じ判定を使い、`update_types` は指定種別の更新が1件以上あれば一致する。無効なフィルタは一致しない。省略・空配列はフィルタ ID による制限なし。他の match 条件とは AND で評価する。`exclude` や保留時・後処理の条件には `filter_ids` を指定しない。

例えば `minor-update` に `update_types: [minor]`、`patch-update` に `update_types: [patch]` を指定すると、minor・patch が混在する PR は両方のフィルタに一致する。`filter_ids: [minor-update, patch-update]` を持つレビューは、その PR に一度だけ適用される。major を含む PR も major フィルタに一致するため、major 用と minor/patch 用のレビューがそれぞれあれば両方適用される。

参照先に一致がなく判定不能が残る場合は、レビューの適用を確定できなければ PR を unknown にする。別の参照先の一致で適用が確定する場合や、無効・不一致の参照先だけを持つレビューのためには追加情報を要求しない。

```yaml
pull_requests:
  filters:
    - id: go-modules
      files: [go.mod, go.sum]

review:
  - id: common
    instructions: 変更履歴とCI、人間からの未解決の指摘を確認する。
  - id: go-compatibility
    match:
      filter_ids: [go-modules]
    instructions: Goの対応バージョンと利用箇所への影響を確認する。
  - id: go-major
    match:
      filter_ids: [go-modules]
      update_types: [major]
    instructions: 破壊的変更と移行手順を確認する。
```

`common` は全候補に、`go-compatibility` は go-modules に一致する候補に、`go-major` はそのうち major 更新を含む候補に適用する。複数のフィルタに一致しても同じレビュー ID は1回だけ適用する。

- ファイル条件は、その PR の変更パス（rename の旧名・新名）に対して評価する。
- 依存名と更新種別は同じ更新に対して評価する。異なる依存の名前と種別を組み合わせない。
- match の対象更新から exclude に合う更新を除き、1つ以上残れば適用する。1つの依存の除外で group PR 全体を除外しない。
- match の省略・空条件は制限なし。exclude の省略・空条件は除外なし。
- レビューでは一致したすべての指示を設定順に適用し、`pr list` の各 PR の `review` に合成済みの `id` と `instructions` を出力する。互換性のため `review_ids` も同じ順序で残す。後処理の必要性や追加の条件判断は instructions に記述できる。
- after_repo は今回実際にマージした各 PR に条件を評価し、いずれかが該当すればその ID を一度実行する。別 PR のファイルと依存を混ぜて一致にしない。

指示にはコマンド、入力の作り方、ローカル/リモートの作業場所、待機、失敗時の後続マージの方針を必要に応じて書く。コマンドを使わない作業も、そのまま指示文で定義する。CLI は実行しない。AI は成功／不要／失敗・未完了を ID ごとに報告する。

`pr list` の `pull_requests` の各要素は、例えば次のレビュー情報を持つ（他の PR フィールドは省略）。AI は `review` をそのまま使い、条件の再判定や `config show` からの本文の引き直しは行わない。

```json
{
  "number": 111,
  "url": "https://github.com/owner/repo/pull/111",
  "selection": "candidate",
  "review": [
    {"id": "common", "instructions": "変更履歴とCI、人間からの未解決の指摘を確認する。"},
    {"id": "go-compatibility", "instructions": "Goの対応バージョンと利用箇所への影響を確認する。"}
  ],
  "review_ids": ["common", "go-compatibility"]
}
```

指示がない候補、excluded、unknown の `review` は `[]`。unknown では適用が未確定なので、一部の指示だけを実行可能なレビューとして返さない。固定のレビューを追加していた `review_required` は廃止し、実行するレビューは `review` に集約した。出力全体の `complete`、`errors`、件数などは引き続き確認する。`config show` は設定の調査とマージ方法・保留時・後処理の設定取得に使える。

## 設定の確認

`config show` と `pr list` の `--repo` は省略でき、省略時は `--repo .` と同じくカレントディレクトリをリポジトリルートとして使う。リポジトリルートで次のコマンドを実行する。

```sh
renstiq config show
renstiq pr list
```

別のリポジトリや共通設定を使う場合は、パスを指定する。

```sh
renstiq config show --repo /path/to/repo --config /path/to/config.yaml
```

出力の ID、enabled、合成後の条件と指示を確認する。候補の選別結果は、次のコマンドで確認できる。

```sh
renstiq pr list --repo /path/to/repo --config /path/to/config.yaml
```

候補と errors を確認する。除外・判定不能のPRと理由を調査するときは `--all` を付ける。これらのコマンドは設定と候補を読み取るだけで、マージや後処理は実行しない。

各項目の型や指定可能な値は、CLI から JSON Schema として取得できる。

```sh
renstiq schema config
renstiq schema repo
```
