# Changelog

## [0.6.0](https://github.com/wim-web/renstiq/compare/v0.5.0...v0.6.0) (2026-09-09)


### Features

* repoごとの先頭一致ルールにポリシーを一本化する ([#35](https://github.com/wim-web/renstiq/issues/35)) ([39d916f](https://github.com/wim-web/renstiq/commit/39d916f2ba4f28605aa06800e24fec5a525a8380))

## [0.5.0](https://github.com/wim-web/renstiq/compare/v0.4.0...v0.5.0) (2026-09-09)


### Features

* filter_idsに完全一致と含む比較モードを追加する ([#34](https://github.com/wim-web/renstiq/issues/34)) ([c17854d](https://github.com/wim-web/renstiq/commit/c17854d650e70b106f579c4a053617c533c04024))


### Bug Fixes

* update_typesを更新種別のいずれかに一致させる ([#32](https://github.com/wim-web/renstiq/issues/32)) ([b3f7eb9](https://github.com/wim-web/renstiq/commit/b3f7eb9a548588f0042d7faec862837e561a8b80))

## [0.4.0](https://github.com/wim-web/renstiq/compare/v0.3.0...v0.4.0) (2026-09-09)


### Features

* initの生成設定に全項目のコメント例を追加する ([#31](https://github.com/wim-web/renstiq/issues/31)) ([06309c7](https://github.com/wim-web/renstiq/commit/06309c70d967d7b2152ba980ee800070ca8e92b7))
* lock labelを廃止し--repoを任意にする ([#29](https://github.com/wim-web/renstiq/issues/29)) ([123d5a8](https://github.com/wim-web/renstiq/commit/123d5a85a6572c274a687f4f1accbbb40ee87c5d))

## [0.3.0](https://github.com/wim-web/renstiq/compare/v0.2.0...v0.3.0) (2026-09-09)


### ⚠ BREAKING CHANGES

* API再試行の回数と間隔を明示設定にする ([#27](https://github.com/wim-web/renstiq/issues/27))
* 作者とレビューを設定に一本化し通常出力を候補に絞る ([#26](https://github.com/wim-web/renstiq/issues/26))
* フィルタ同士をORで評価し判定不能を分離する ([#23](https://github.com/wim-web/renstiq/issues/23))

### Features

* PRのラベルをフィルタ条件に追加する ([#25](https://github.com/wim-web/renstiq/issues/25)) ([09ead63](https://github.com/wim-web/renstiq/commit/09ead632905fd401f2f7c46ba21f7a8303d6624d))
* フィルタ参照でレビューを選びPR一覧に本文を返す ([#24](https://github.com/wim-web/renstiq/issues/24)) ([00ae5f0](https://github.com/wim-web/renstiq/commit/00ae5f05c00d11a914d0ced9474548ff7438f5f1))


### Bug Fixes

* API再試行の回数と間隔を明示設定にする ([#27](https://github.com/wim-web/renstiq/issues/27)) ([59ad644](https://github.com/wim-web/renstiq/commit/59ad6443a0d3db49aeaf95855dd67e30d3d135d8))
* フィルタ同士をORで評価し判定不能を分離する ([#23](https://github.com/wim-web/renstiq/issues/23)) ([1cf3c9c](https://github.com/wim-web/renstiq/commit/1cf3c9cd41503d606707fcb76444a193f51560dd))
* 作者とレビューを設定に一本化し通常出力を候補に絞る ([#26](https://github.com/wim-web/renstiq/issues/26)) ([d6b763d](https://github.com/wim-web/renstiq/commit/d6b763d360f02bbb69d26025f72e6fa5c5e21559))

## [0.2.0](https://github.com/wim-web/renstiq/compare/v0.1.0...v0.2.0) (2026-09-07)


### ⚠ BREAKING CHANGES

* GitHub API再試行設定をdefaults配下へ移動 ([#20](https://github.com/wim-web/renstiq/issues/20))

### Features

* GitHub API再試行設定をdefaults配下へ移動 ([#20](https://github.com/wim-web/renstiq/issues/20)) ([195d381](https://github.com/wim-web/renstiq/commit/195d38167c42f37a2d1213e5fbc3cb362004ea75))
* 設定v2とPR選別・AI指示を再設計 ([#22](https://github.com/wim-web/renstiq/issues/22)) ([dc3fb1c](https://github.com/wim-web/renstiq/commit/dc3fb1cd728ca64e1150067595c07f024d1831bd))

## [0.1.0](https://github.com/wim-web/renstiq/compare/v0.0.0...v0.1.0) (2026-09-07)


### Features

* 設定項目を整理し、ルール優先順位と後処理の除外条件を追加 ([#18](https://github.com/wim-web/renstiq/issues/18)) ([9d4b282](https://github.com/wim-web/renstiq/commit/9d4b2828a761d72b2a1229c03198fcb5b473b64a))

## 0.0.0 (2026-09-06)


### ⚠ BREAKING CHANGES

* 設定とPR候補をAIへ渡すCLIへ切り替え ([#16](https://github.com/wim-web/renstiq/issues/16))

### Features

* 設定とPR候補をAIへ渡すCLIへ切り替え ([#16](https://github.com/wim-web/renstiq/issues/16)) ([063b333](https://github.com/wim-web/renstiq/commit/063b333bae36eb42df50c7fc41a1b8aabc31cdb9))

## [1.3.0](https://github.com/wim-web/renstiq/compare/v1.2.0...v1.3.0) (2026-09-06)


### Features

* discover のデフォルト表示を enabled のみに変更 ([#14](https://github.com/wim-web/renstiq/issues/14)) ([d7ca68f](https://github.com/wim-web/renstiq/commit/d7ca68f5ed5637f049861a32b20377a2e34c30dc))

## [1.2.0](https://github.com/wim-web/renstiq/compare/v1.1.2...v1.2.0) (2026-09-06)


### Features

* Cobra に移行して fish 補完を追加 ([#12](https://github.com/wim-web/renstiq/issues/12)) ([938762d](https://github.com/wim-web/renstiq/commit/938762d59af442d156cee8503574f9df2b640135))

## [1.1.2](https://github.com/wim-web/renstiq/compare/v1.1.1...v1.1.2) (2026-09-06)


### Bug Fixes

* GoのVCS情報からバージョンのハッシュを表示 ([#10](https://github.com/wim-web/renstiq/issues/10)) ([f9d4d3c](https://github.com/wim-web/renstiq/commit/f9d4d3c60de243be1168548c8e70f07f8d99b1d5))

## [1.1.1](https://github.com/wim-web/renstiq/compare/v1.1.0...v1.1.1) (2026-09-05)


### Bug Fixes

* make feedback retries and post-merge recovery safe ([#7](https://github.com/wim-web/renstiq/issues/7)) ([ed5187b](https://github.com/wim-web/renstiq/commit/ed5187b1d2a75673cc9f33ba7aab82f9427ded3f))

## [1.1.0](https://github.com/wim-web/renstiq/compare/v1.0.0...v1.1.0) (2026-09-05)


### Features

* add self-update command ([#5](https://github.com/wim-web/renstiq/issues/5)) ([e13c215](https://github.com/wim-web/renstiq/commit/e13c215a994114caf92fb1e6072b0a66b69ddb97))

## 1.0.0 (2026-09-05)


### Features

* PR操作とマージ後処理を管理するrenstiq CLIを追加 ([#1](https://github.com/wim-web/renstiq/issues/1)) ([4f324d5](https://github.com/wim-web/renstiq/commit/4f324d56746e7fa77280ae8ac128b42b9fa5ce1a))
* release-pleaseによるリリースとmatrixビルドを追加 ([#3](https://github.com/wim-web/renstiq/issues/3)) ([2a48acc](https://github.com/wim-web/renstiq/commit/2a48acc05d168f238e0262d16b91dc0f28e346b6))
