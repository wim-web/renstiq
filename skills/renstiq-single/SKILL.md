---
name: renstiq-single
description: renstiqの有効設定とPR候補を読み、単一repoのRenovate PRを調査し、依頼と設定で許可されたコメント・ラベル・マージ・後処理を行う。repoやPRを個別に指定した依頼で使う。
---

# 単一repoのPR処理

対象repo、対象PR、調査のみか操作まで行うかを依頼から確認し、指定された場所・共通設定・許可範囲を引き継ぐ。AGENTS.mdなどの上位・追加指示に従う。設定は判断条件と操作の許可範囲を示すもので、調査依頼を操作依頼に広げない。

`REPO_DIR` はローカルのrepoルート、`OWNER/REPO` は取得結果のGitHub名、`PR_NUMBER` は対象PR番号。`--config FILE` が指定されていれば、開始・再取得・終了にも同じ値を付ける。

## 開始と対象の確定

```sh
renstiq config show --repo REPO_DIR
renstiq pr list --repo REPO_DIR --all
git -C REPO_DIR status --short
git -C REPO_DIR branch --show-current
```

- `config show` の `path`、`repo`、`enabled`、`sources`、`config` を読む。GitHub名を別repoの値で補わない。設定欠落・不正は処理不能、`enabled: false` は無効として報告し、有効化しない。初期設定も依頼された場合だけ `renstiq init --repo REPO_DIR` を使う。雛形の作成を条件の移行完了と扱わない。
- 開始時の `open_renovate_count` と、`candidate`・`excluded`・`unknown` の各件数、PR番号とhead/base SHAを保持する。`--all` は指定repoの全open Renovate PRを設定による除外も含めて表示する。人間・Dependabot・closed PRや全repoへの拡張ではない。PRが限定されていれば、その指定と照合する。
- `candidate` は機械的に除外されなかっただけでマージ許可ではない。`candidate_rule_ids` はファイル条件に関連するルールであり、許可ルールの確定結果ではない。`review_required` は追加確認項目で、確認済みの結果ではない。
- `complete: false`、`errors`、非0終了を読む。部分失敗のJSONにある成功分を活かし、`unknown` は必要情報を補うまで選別未確定とする。`open_renovate_count: null` を0件と扱わない。`complete: true` でもレビュー完了や同時点のsnapshotを保証しない。
- open数が0なら「open Renovate PRなし」、open数が正で候補0なら「候補なし」と除外・不明の内訳を区別する。どちらも終了時確認は行う。
- 既存の `.agents/skills/renovate-automerge/SKILL.md` があれば、許可条件・追加調査・禁止事項・後処理がconfigの項目またはinstructionsへ移行済みか照合する。未移行の条件を無視して完了扱いしない。設定変更まで依頼されていなければ不足を報告し、条件に依存する操作を保留する。
- `post_merge` のコマンドと参照先を読み、必要入力・実行場所・checkout操作が明示されていることを確認する。旧形式のstdin自動注入や自動checkout同期に依存したままなら、移行が必要な箇所と影響を報告する。入力を捏造して実行しない。

## 詳細取得と判断

通常の詳細取得にはghを使う。次は例であり、必要なフィールドを追加できる。

```sh
gh pr view PR_NUMBER --repo OWNER/REPO --json number,title,body,url,author,state,isDraft,headRefName,headRefOid,baseRefName,baseRefOid,mergeable,mergeStateStatus,reviewDecision,reviewRequests,reviews,comments,files,commits,statusCheckRollup
gh pr diff PR_NUMBER --repo OWNER/REPO
gh pr checks PR_NUMBER --repo OWNER/REPO --json name,bucket,state,workflow,link
```

- ghで不足する情報だけ `gh api` 等で補う。ファイル・コミット・コメント・レビューthreadなどの一覧はページングと件数を確認し、切り詰められたデータで判断しない。RESTのPRファイル一覧は最大3000件、PRコミット一覧は最大250件のため、上限を超える場合は別の読取方法で補うか確認不能として報告する。[GitHub REST仕様](https://docs.github.com/en/rest/pulls/pulls)
- 設定の作者・base/head・files・commit_authorsを現在情報と照合する。全変更を実際の依存名・更新種別・更新前後の版へ対応付け、renameは旧名と新名を含める。タイトル・branch・本文だけで更新分類を確定しない。lockfileに含まれる付随更新や依存以外の変更も見落とさない。
- `rules` がある場合、各更新の全関連ファイル、依存名、更新種別に一致するルールを確認する。group PRは更新ごとに異なるルールを使えるが、調査から漏れたファイルを残さない。一つの更新に複数ルールが一致したら全てのinstructionsとchecksを満たす。都合のよい一つだけを選ばない。
- `rules[].checks` は共通checksに対する上書き。各一致ルールについて、省略項目は継承し、`required: []` は必要check名の一覧を空にする。`minimum` と `all_success` はそれぞれ有効値に従う。ルールが空なら追加の更新別ルールなしで共通checksを確認するが、空であることをマージ許可の根拠にしない。
- `review.instructions` と一致ルールの `instructions` に従い、公式release notes・changelog・migration guide、repo内の利用箇所、互換性と影響を調査する。upstreamや利用箇所を確認できなければ、その不足を明示する。PR本文だけで代替しない。
- CI失敗・pending・draft・競合・未解決要求があっても、依頼された影響調査を省略しない。checksは必要数、名前、workflow、app ID、成功状態を有効設定に沿って確認し、GitHubのrequired checksも満たす。`all_success: true` のときskipped/neutralをsuccess扱いしない。
- 人間のコメント、requested changes、レビュー要求、未解決threadを確認する。古いautomationコメントを一律に人間の未解決要求と扱わず、現在も具体的な対応要求が残っているか調べる。
- draft、確認できないmergeability、競合、未解決要求、checks不充足はマージしない。`merge.require_clean: true` ならCLEANが必要。falseでもGitHubのマージ制約を無視しない。待機が必要なら依頼範囲で有限に確認し、残るpendingは保留として報告する。

## 許可された操作

同一repo内は1PRずつ処理する。操作前に現在のstate、作者、head/base SHA、設定、checks、要求を再確認する。調査後にheadやbaseが変わった場合は差分と影響を再調査し、SHAだけ更新して古い判断を流用しない。

- マージ方法は `merge.method` のsquash/merge/rebaseに合わせる。例えばsquashなら次を使う。head照合はbaseやchecksの再確認を代替しない。[gh pr merge](https://cli.github.com/manual/gh_pr_merge)

```sh
gh pr merge PR_NUMBER --repo OWNER/REPO --squash --match-head-commit REVIEWED_HEAD_SHA
```

- `--admin`、checksやbranch protectionの回避、Renovate branchへのcommit/push、PRのclose、Renovate rebase checkboxの操作は禁止する。auto-mergeやmerge queueへの登録を即時マージ成功と報告しない。実際のmerged状態とmerge commitを確認してから後処理へ進む。
- branch削除は `merge.delete_branch` が許可した場合のみ。削除直前にhead repo・branch・SHAを確認し、更新済みbranch、base/default branch、別repoのbranchを削除しない。ローカル変更を破棄する操作を行わない。
- コメントは依頼で許可され、`feedback.comment_on` に理由が含まれ、調査内容を残す意味がある場合に行う。理由、根拠URL、影響、人間への確認点を具体的に書く。CI待ち・通信失敗だけでは投稿しない。同等の既存コメントは重複投稿せずURLを報告し、他人のコメントを書き換えない。
- labelの追加・解除は `feedback.labels` の範囲内で行い、人間確認が必要か、以前の理由が解消したかを現在の調査で判断する。単なる一時的な取得失敗を人間確認の証拠にしない。コメント本文は構造化引数または `--body-file` で渡す。
- 操作が失敗・タイムアウトしたら終了コードだけで未実行と決めず、PRのmerged状態、投稿済みコメント、現在のlabelやbranchを確認する。結果不明の書込みを直ちに再送しない。確認不能なら依存する後続処理を止め、独立した調査は続ける。

## マージ後処理

renstiqは後処理を実行しない。確定したマージと実際の更新情報をもとに、依頼・configで許可された `post_merge` のコマンドをAIが実行する。

- `match.changed_files_any` が非空なら変更パスの少なくとも一つが一致する必要がある。dependencies/update_typesが非空なら同じ更新が両条件を満たす必要がある。ファイル条件と更新条件の両方があれば両方を満たす。空の条件は追加制限なし。`requires_review: true` は設定済みIDごとに必要性と理由を判断する。
- `after_each_merge` は該当マージ後、次のPRへ進む前に実行・結果確認する。`after_repo` は今回の対象PRの判断を終え、今回の確定マージが一つ以上ある場合に、該当コマンドを一度実行する。マージ0件や調査のみなら実行しない。
- `working_dir` はコマンド指定、共通の有効設定、repoルートの順。相対パスはrepoルート、`~/` はホームから解決し、実際の場所と対象repoを確認する。argvは設定に従い、未設定のコマンドや入力を調査結果から作らない。
- 最新checkoutが必要なら、ローカル変更・現在branch・origin・対象commitを確認し、設定の操作指示に沿って明示的に同期する。自動同期を前提にせず、ローカル変更を失うreset/checkoutをしない。
- スクリプトへの入力は移行済み設定の明示的な契約に従う。旧実行記録や判断JSONを復元してCLIに提出する必要はない。必要な入力・手順が未定義なら不足として報告する。
- 後処理の実行結果が不明なら、ログ、生成物、release/deploy先などで効果を確認する。確認できるまで同じコマンドを再実行せず、依存する操作を保留する。旧stateを利用・削除して回避しない。

## 最終確認と報告

```sh
renstiq pr list --repo REPO_DIR --all
git -C REPO_DIR status --short
git -C REPO_DIR branch --show-current
```

開始時の対象と照合し、最後に増えたPRは残件として記載する。依頼範囲を無断で広げて処理を繰り返さない。最終取得が失敗した場合や最終確認を行えない場合は、最終件数を推測せず未完了とする。

報告には次を含める。

- repo名・ローカルパス、開始時と終了時のopen数およびcandidate/excluded/unknown数。不明な数は不明と明記する。
- PRごとの番号・URL、マージまたは保留の理由、checksの実際の結果、changelogの根拠、利用箇所と影響、未解決要求、残る確認事項。
- 実施したマージ・コメント・label・branch操作と確認結果。結果不明や投稿失敗も分けて記載する。
- 後処理のID、必要/不要の理由、実行結果と確認した効果、エラー、未実施・未移行事項。
- 開始時からのローカル変更、最終確認の成否、新規PRを含む残件、ユーザーが確認できる上記コマンドとPR URL。
