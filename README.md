# engram opt（EngramOpt）

映像という記憶情報を、知覚画質を損なわずに極小の痕跡へと最適化（圧縮）する動画変換CLI。

```
入力動画 ─┬─ 1. シーン検出      av-scenechange（Y4Mはメモリパイプ直結）
          ├─ 2. Per-Shot最適化  シーンごとにCRF二分探索。
          │                     「VMAF harmonic_mean >= 95.0」を満たす最大CRFを採用
          └─ 3. 無劣化結合＋音声ミックス  concat demuxer + ストリームコピー。
                                          音声は元動画から最後に1回だけ付与
              → <入力名>.opt.mkv
```

- **画質保証つきの圧縮**: 各シーンで実測VMAFが目標95.0以上であることを確認してから採用。「目標品質を満たした上での最小サイズ」だけを出す。
- **10-bit既定出力**（`yuv420p10le`）: 入力が8-bitでもバンディングを防ぎサイズ削減。ウィザードで8-bitも選択可。
- **完全ポータブル**: Zip解凍だけで動作。ランタイム導入・PATH設定・ビルドは不要。
- **無人運転向け**: `--log-file` 二重化、失敗時の一時領域自動保持、非TTY自動フォールバック。

---

## クイックスタート

### 配布Zipを使う場合

1. Zipを解凍する（**フォルダ構造は崩さない**。本体が隣の `bin/` を基準に動くため）
2. 実行方法は2つ:

```console
engram-opt.exe                       ← 設定ウィザードが起動（ダブルクリックOK）
engram-opt.exe 入力動画.mp4  ← そのまま即実行
```

完了すると入力と同じ場所に `<入力名>.opt.mkv` が出力される。

### 起動モード一覧

| 起動方法 | 動作 |
|---|---|
| 引数なしで起動（ダブルクリック含む）＋端末 | **設定ウィザード**（入力パス／Codec／Preset実値／Min・Max CRF／Target VMAF／Bit Depth／Audio／Eval Algorithm／Eval Resolution／Out Res／Output を選んでEnter。全項目に既定値を事前入力済み。`--codec` 等のフラグを併用した場合はその値が初期選択になる） |
| 端末で `engram-opt.exe 入力.mp4` | 即実行（平文ログ） |
| `engram-opt.exe 入力.mp4 --tui` | ダッシュボード表示で実行 |
| パイプ／CI／リダイレクト環境 | 常に平文ログで即実行（`--tui` は無視される）。引数なしの場合はヘルプ表示のみ |
| `--headless` | 対話UIを一切出さないことを明示（`--tui` と排他）。**入力動画が必須**で、無指定時はエラー終了する |

処理完了後、TUIセッションでは**サマリー画面でキー待ち**するため、
ダブルクリック起動でも結果確認前にウィンドウが消えることはない。
TUI開始前にエラー終了する場合も `[Enter]キーで終了します...` の待ちが入るため、
原因の確認が可能。

### ソースから使う場合（開発者）

| 必要物 | 備考 |
|---|---|
| Go 1.27+ | 本体のビルド |
| Rust（cargo） | av-scenechangeのローカルビルドで使用 |
| Git | ソース取得 |

```console
go run ./cmd/engram-setup        # 依存バイナリ導入（冪等・初回のみ数分）
go run ./cmd/engram-opt 入力動画.mp4
```

setupの内容: FFmpeg 8.1.2 full build をGitHubリリースからダウンロード（SHA256照合）→
`build/bin/` へ展開。av-scenechange v0.24.1 をソース取得（SHA256照合）→ cargoでビルド。
最後に ffmpeg/ffprobe の起動、libvmafフィルタ、必須エンコーダ3種
（libx264/libx265/libsvtav1）の存在を検証する。

> `engram-setup` は開発者専用ツールで、配布Zipには含まれません
> （利用者のZipには `bin/` が同梱済みのためセットアップは不要です）。

---

## コマンドリファレンス

### `engram-opt <input>`

| フラグ | 既定 | 説明 |
|---|---|---|
| `-o, --out <path>` | `<input>.opt.mkv` | 出力先。一時領域配下と入力/ログとの同一パスは起動時に拒否。拡張子は `.mkv`（推奨・全組合せ安全）/ `.mp4` / `.webm` / `.mov` のみ（それ以外は起動時エラー）。出力先ディレクトリは無ければ自動作成される |
| `--codec <name>` | `h264` | `h264` / `hevc` / `av1` |
| `--preset <p>` | `medium` | エンコードプリセット。**全試行で一律固定** |
| `--metric <m>` | `harmonic_mean` | 合否基準のVMAF統計: `harmonic_mean`（調和平均・既定）/ `mean`（算術平均）/ `min`（最悪フレーム）。`min` は最悪1帧が基準のため目標未達でMinCRF採用になりやすい |
| `--eval-profile <p>` | `vmaf_v1.0.16_3d0h` | **評価プロファイル**（アルゴリズム×評価解像度のセット）: `vmaf_v1.0.16_3d0h`=3d0h@1080p / `vmaf_4k_v0.6.1`=4K専用モデル@2160p。4K配信の品質保証には `vmaf_4k_v0.6.1` を推奨（1080p縮小評価はネイティブ4Kの劣化を見逃すため）。失敗時の暗黙フォールバックなし |
| `--out-res <res>` | *入力と同じ* | 出力解像度(px): 未指定なら**入力動画の実寸**を自動検出してそのまま出力（ログに表示）。`<偶数>x<偶数>`（例: `1920x1080`）で明示指定も可。シーン検出は元解像度で行う |
| `--audio <m>` | `copy` | 最終音声トラック: `copy` / `opus` / `aac` / `none`（下表） |
| `--shot <n>` | 無効 | デバッグ: シーン番号nだけCRF探索（結合なし・勝利チャンク保持） |
| `--tui` | 無効 | ダッシュボード表示。stdoutが端末以外なら無視され平文ログになる |
| `--headless` | 無効 | 対話UIを禁止（`--tui` と排他。スクリプトでの明示向け） |
| `--log-file <path>` | 無効 | ログをファイルへ**追記**保存（無人実行向け） |
| `--enc-args <args>` | 無効 | エンコード試行への**追加ffmpeg出力オプション**（例: `-tune film`）。空白区切り。`-crf`/`-preset`/`-vf`等の管理対象オプションは指定すると拒否される |
| `--version` | — | ビルドバージョンを1行表示（配布Zip名と同一値。不具合報告時に添付） |

コーデックとプリセットの対応:

| codec | エンコーダ | preset指定 |
|---|---|---|
| h264 | libx264 | `ultrafast`〜`veryslow` の文字列 |
| hevc | libx265 | 同上 |
| av1 | libsvtav1 | 数値のみ（1〜13。小さいほど高品質・低速）。x264流名称は指定不可（エラー） |

音声モードの詳細:

| モード | 処理 | 用途 |
|---|---|---|
| `copy`（既定） | 元音声を無劣化コピー。処理コストほぼゼロ | 音質最優先・最速 |
| `opus` | libopusへ再圧縮（VBR）。ステレオ128k／サラウンド256kを自動判定 | 最高圧縮率 |
| `aac` | AACへ再圧縮。ステレオ128k／サラウンド320kを自動判定 | 互換性重視 |
| `none` | 音声トラック破棄（映像のみ出力） | 無音動画・サイネージ |

音声はシーン分割の対象外で、完成映像への最終ミックス時に1回だけ処理される
（チャンク境界での音声切断によるノイズが構造的に発生しない）。元動画に
音声がない場合は全モードで映像のみ出力する。

### `engram-setup`（開発者専用・配布物外）

依存バイナリの導入・検証。**冪等**で、導入済みなら検証のみ実行する。
壊れた場合は `build/bin/` の該当バイナリを消して再実行すれば再取得される。

---

## 動作仕様の固定点（現行バージョン）

| 項目 | 固定値 |
|---|---|
| CRF探索範囲 | 整数CRF二分探索。既定 15〜36（ウィザードで変更可） |
| 合否判定 | VMAF `harmonic_mean >= 目標` の最大CRFを採用。目標既定 95.0（変更可） |
| 評価プロファイル | `vmaf_v1.0.16_3d0h`（3d0hモデル@1920x1080・既定）/ `vmaf_4k_v0.6.1`（4Kモデル@3840x2160）。評価失敗時のフォールバックなし（即エラー） |
| ウィザード既定Codec | AV1（`--codec`指定時はその値を初期選択。CLI/ヘッドレスの既定はh264のまま） |
| ピクセル形式 | 既定10-bit `yuv420p10le`（8-bit `yuv420p` も選択可） |
| GOP | GOP長＝シーン長。チャンク先頭は必ずIDR（以降のキーフレーム配置はエンコーダー判断に委ねる） |
| 音声 | 最終ミックス方式（上表参照）。既定copyは無劣化 |
| 処理方式 | シーン逐次。各試行は区間をフレーム単位で正確に抽出（高速化より正確性優先） |

目標スコアに到達できないシーンでは警告後、最低CRF(15)をベストエフォート採用し全体は続行する。

> **注意**: 可変フレームレート(VFR)入力は想定外。

---

## ログの読み方

各CRF試行ごとに1行が出る:

```text
[optimize] shot 2 trial crf=25 harmonic_mean=95.41 min=88.02 [HIT]
[optimize] shot 2 trial crf=30 harmonic_mean=92.10 min=85.33 [MISS]
```

`[HIT]`=目標達成（さらに大きいCRFを試す）、`[MISS]`=未達（より小さいCRFを探す）。
`min` は参考記録値で合否判定には使われない。

完了サマリ: 達成シーン数／サイズ削減率／出力先／総試行回数を出力する。

---

## TUIダッシュボード（--tui）

```text
engram optimizer  input.mp4 -> input.opt.mkv
codec=h264 preset=medium target=95.0 audio=copy
[optimizing] ▕██████████░░░░░░▏   scenes 2/5 done · trials 8 · elapsed 01:23
SHOT  FRAMES       STATUS  CRF   VMAF(harm)  LAST
0     0-119         +      31    96.20       5
1     120-299       >      25    94.87       MISS
-- log --（直近のログ）
[q] quit (running pipeline will be cancelled)
```

- `STATUS` 記号: `·`待機 / `>`実行中 / `+`完了 / `x`失敗。`+` は色で意味が分かれる
  （緑=目標達成、黄=未達ベストエフォート採用）
- `q` キーで中断（実行中パイプラインをキャンセルし、失敗時扱いになる）

### 完了サマリー

実行が完了するとダッシュボードはサマリー画面へ切り替わり、キー入力を待つ:

```text
処理が完了しました
出力先: input.opt.mkv
サイズ: 12.34 MB → 3.21 MB (-74.0%)
達成: 5 / 全 5 shot · 試行 14 回 · 所要 03:12
SHOT  FRAMES       CRF   VMAF(harm)  MET
...
[Enter / q] 終了
```

失敗時もFAILED内容を表示したまま待つため、結果を読む前に画面が消えることはない。

---

## 一時ファイルの扱い

試行チャンク等はジョブごとに隔離される:

- 配布版: `<engram-opt.exeと同じ場所>/tmp/<起動日時>/`
- 開発時: `<リポジトリ>/build/tmp/<起動日時>/`

**成功時は自動削除。失敗・中断時のみ保持**され、パスがログに出る
（`[pipeline] failed; temp kept for inspection: ...`）。解析後は手動で削除すること。

### 単一シーンのデバッグ（--shot）

長い動画の全体実行前に、まず1シーンだけで挙動を確認できる:

```console
engram-opt.exe input.mp4 --shot 0
```

結合せず勝利CRFのチャンク（`.mkv`）を保持し、パスをログに出す。チャンクは
tmp配下に残るので再生して品質を目視確認した後、手動で削除する。

---

## 無人実行（1日単位の運用）

```console
engram-opt.exe long_video.mp4 --codec av1 --tui --log-file run.log
```

- `--log-file` は追記モード。TUI表示中も書き込みは継続する（画面には出ない）
- 失敗時は終了コード非ゼロ。エラーはログとstderrに出力される

## トラブルシューティング

| 症状 | 対処 |
|---|---|
| `binary "ffmpeg" not found; run 'go run ./cmd/engram-setup' first` | 開発時: setupを実行。配布版: 本体と `bin/` の位置関係を確認（フォルダ構造維持） |
| `output path ... must be outside the temp dir` | `--out` が tmp 配下。別の場所を指定する |
| `invalid preset "..." for av1/libsvtav1` | AV1のpresetは**数値のみ**（例: 6）。x264流の名称は不可 |
| `total frame count mismatch` / `frame count mismatch in evaluation` | 入力メタデータと実データの不一致（fail-fast設計）。入力ファイルを確認する |
| `input file not found` / `input is a directory` | 入力パスの指定ミス |
| `input video is too short (...)` | 単一フレーム等の極短入力は対応外 |
| `unsupported output extension ".xxx"` | 出力は `.mkv`(推奨)/`.mp4`/`.webm`/`.mov` のみ |
| `output path is an existing directory` | 出力先にはファイル名まで指定する |
| `--out and --log-file must differ` | 出力とログを同一パスにはできない |
| 終了が遅い | 仕様。まず `--shot` で1シーン試すことを推奨 |
| 中断後に tmp が残った | 失敗時保持の仕様。解析後に手動削除 |

---

## 開発者向け

### ビルドと配布物作成

```console
# 配布物一式（build/へ本体配置＋NOTICES生成＋dist/へZip化）
go run ./cmd/engram-package

# 開発時のクイックビルドのみ
go build -o build/engram-opt.exe ./cmd/engram-opt
```

`build/` は配布Zipと同一構造のステージング領域という規約。パッケージャーは
同梱バイナリの存在確認 → 本体ビルド → THIRD-PARTY-NOTICES自動生成
（実リンク依存のみを収集し、**ライセンス全文を逐字埋め込み**。タグzipに
LICENSEが無い依存は上流原文を third_party/licenses/ 配下で補完）→ LICENSE/README同梱 →
決定論的Zip出力までを一括で行う。出力: `dist/engram-opt_<version>_<os>-<arch>.zip`
（バージョンは `git describe` 由来。`-version` で上書き可）。

### テスト

```console
go test -short ./internal/...               # 高速ループ（数秒）
go test ./internal/... ./test/...           # フル検証（約1〜2分。事前にsetupが必要）
```

統合/E2Eは実バイナリを使用し、未セットアップ環境では自動スキップされる。
テスト動画はコミットせず lavfi で動的生成する。

### モジュール構成

設計の唯一の情報源は `memo.md`（モジュール構成・固定仕様・実測知見）。

```text
cmd/engram-opt/           ランタイム本体のmain（薄いラッパのみ・配布対象）
cmd/engram-setup/         開発者向けセットアップmain（配布物には含めない）
internal/cli/             cobraコマンド定義
internal/setup/           依存関係の自動セットアップ（engram-setupから利用）
internal/domain/          共通型・インターフェース（Scene/Metrics/Config）
internal/detector/        シーン検出（av-scenechange）
internal/encoder/         チャンクエンコード＋無劣化結合（ffmpeg）
internal/evaluator/       VMAF評価（libvmaf）
internal/engine/          CRF二分探索＋パイプライン司令塔
internal/ui/              bubbletea製ダッシュボード
internal/toolbin/         レイアウト検出・外部バイナリ解決
test/e2e/                 パイプライン全走査テスト
```

---

## ライセンス

本ソフトウェアおよび同梱OSS（FFmpeg / av-scenechange / Goライブラリ群）の
ライセンス・著作権表示は `LICENSE` および `THIRD-PARTY-NOTICES.txt` を参照
（配布物に同梱）。
