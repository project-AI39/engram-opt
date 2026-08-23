# engram opt（EngramOpt）設計メモ

- **由来**: 脳内に物理的に刻まれる記憶痕跡「engram」× 最適化「opt」。映像という記憶情報を、知覚画質を損なわずに極小の痕跡へと最適化（圧縮）する。
- **本書の位置づけ**: 設計の唯一の情報源（SSOT）。**動作仕様の固定点を変更するときは必ず本書を先に更新してから着手する**。新機能の追加は原則しない（堅牢化・整理が主目的）。開発経緯の詳細ログはgitログを参照（本書には結論のみ記載）。

# 1. 現在の状態（2026-08-24時点）

Phase 1〜8の機能開発と改善フェーズが**すべて完了**（動作確認済み）。

- 実装済み: 依存セットアップ自動化／シーン検出／CRF二分探索／VMAF評価／パイプライン統合／TUIダッシュボード＋ウィザード／音声4モード／配布Zip化／CI（`.github/workflows/ci.yml`: windows-latest Dev-CI Parity・race検出はCIのみ・実行は手動workflow_dispatch）
- 改善フェーズ実績: バグハント5ラウンド（不具合修正11件）・staticcheck全解消・ファズターゲット5個（約1150万実行で不変条件違反ゼロ）・precheck層新設・コミット規約統一（全92コミット書換）・ドキュメント整理
- 残作業: 本書「8. 将来課題とチェックリスト」のみ

# 2. 背景目的

- 長時間（1日単位など）安定して動作し続ける動画最適化CLIツール。
- ランタイム導入・PATH設定・ビルドを一切要求しない**完全ポータブル配布**（Zip解凍だけで即動作）。
- 「シーン分割 → エンコード → VMAF v1評価 → CRF二分探索によるPer-Shot最適化」を全自動化し、離籍から戻っても進捗・削減状況・ログが一目で分かるTUIダッシュボードを提供する。

# 3. アーキテクチャ

## 3.1 ディレクトリ構成（現行）

将来拡張用の空モジュールは作らない（YAGNI）。

```text
cmd/
  ├── engram-opt/       # ランタイム本体main（薄いラッパのみ・配布対象）
  ├── engram-setup/     # 開発者専用セットアップmain（配布物には含めない）
  └── engram-package/   # 配布Zip化コマンド（品質ゲート兼最終門番）
internal/
  ├── cli/              # cobraコマンド定義（root/run/wizard/precheck/jobdir/check）
  ├── setup/            # FFmpeg DL・cargo build・検証（pin値はここに集約）
  ├── domain/           # 共通型・インターフェース（依存の錨）＋encargs/evalprofile/fuzz
  ├── detector/avscenechange/   # Y4Mパイプ処理・ffprobe総フレーム数取得
  ├── encoder/ffmpeg/   # フレーム完全一致チャンク抽出・codec別preset解決・音声mux
  ├── evaluator/libvmaf/        # 1080pリサイズ・グラフ生成・時間基準正規化
  ├── engine/           # bsearch.go（単一シーン探索）＋orchestrator.go（司令塔）
  ├── ui/               # bubbletea製ダッシュボード＋ウィザード（style.goにテーマ一元）
  ├── toolbin/          # レイアウト検出・外部バイナリ解決・fsutil（パス比較一元）
  ├── packaging/        # Zip化＋THIRD-PARTY-NOTICES全文埋め込み生成
  ├── devcheck/         # gofmt/vetゲート（packageコマンドから呼ばれる）
  └── testutil/         # lavfiによるテスト動画動的生成・バイナリ存在ガード
test/e2e/               # パイプライン全走査
```

## 3.2 データ契約（domain）

正確な定義は各ソースファイルがSSOT（以下は要点）。浮動小数点秒は内部処理から排除し、**int64フレーム番号のみを唯一の真実（SSOT）**とする（23.976fps等の丸め誤差がVMAF崩壊を招く実測のため。秒数表示はTUI/ログでのみ `frame/fps` から算出）。

- `Scene`（scene.go）: Index / StartFrame（0-indexed）/ EndFrame（inclusive）+ FrameCount()
- `QualityMetrics`（metrics.go）: HarmonicMean / Mean / Min ＋ `Score(metric)` 選択器。Scoreは正規化済み値のみ受容し未知/ゼロ値はpanic（防御フォールバック廃止）
- `EncodeParams`（config.go）: 単一試行分 Codec / CRF / Preset / BitDepth / OutWidth,Height / ExtraArgs
- `SearchConfig`（config.go）: 探索全体設定 Codec / MinCRF / MaxCRF / TargetScore / Preset / BitDepth / Metric / Eval(EvalProfile) / OutWidth,Height / ExtraArgs。検証は `Validate()` に一元（ウィザード確定時とCLI構築時の双方で呼ぶ）
- インターフェース（interfaces.go）:
  - `SceneDetector.Detect(ctx, inputPath) ([]Scene, error)` — 総フレーム数はdetector内部でffprobe取得（呼び出し側のffprobe依存なし）
  - `VideoEncoder.EncodeChunk(ctx, input, scene, params, out)` / `ConcatChunks(ctx, paths, out)`
  - `QualityEvaluator.Evaluate(ctx, originalPath, scene, chunk, workDir, profile) (QualityMetrics, error)` — workDir引数により評価成果物の所有権をシーン作業領域に明確化

## 3.3 エンジン設計と起動導線

- orchestrator: 検出 → 各シーン逐次二分探索 → 結合。並列化は明示的にスコープ外。
- 進捗は疎結合のコールバック注入（`ProgressFn`）。engineはuiをimportしない（CLI=ログ、TUI=bubbleteaモデル更新へ変換）。
- 一時ファイルは `tmp/<job-id>/`（PID接尾辞付き・newJobDir一元）に隔離。成功時破棄・失敗時保持（72時間超のstaleジョブは起動時に掃除）。
- 起動導線は全経路単一の `decideLaunch` へ集約（かつて裸起動のみバイパス経路で--headless排他漏れがあったため統合済み）。フラグ値（--codec等）はウィザード初期値へ反映される。

# 4. 固定仕様（動作仕様の固定点）

## 4.1 CRF二分探索

- 変更するのは**整数CRFのみ**（既定15〜36。単調性前提の二分探索で4〜5試行収束）。Preset/Speedは全試行一律固定。
- 合否: シーンごとに `選択指標 >= TargetScore`（既定95.0）を満たす最大CRFを採用。
- 指標は選択可能（`--metric` / ウィザードMetric。libvmaf JSONキーと同一の実名）:
  - `harmonic_mean`（既定）: 低スコア帧のペナルティ最大＝最も保守的な代表値
  - `mean`: 最も緩い。`min`: 最悪1フレーム基準（MinCRFベストエフォート落ちが増え得る仕様を受容）
  - 3統計ともpooled_metricsから取得済みで、単調性前提は各指標で成立
- 未達シーンは警告ログ＋`MetTarget=false`報告のうえMinCRFをベストエフォート採用（全体は続行）。
- 可変フレームレート（VFR）入力は想定外。

## 4.2 エンコード固定点

- **ピクセル形式**: 既定10-bit `yuv420p10le`（入力が8-bitでもバンディング防止とサイズ削減で5〜15%有利）。8-bit `yuv420p` も選択可（encoderは `BitDepth` 参照）。
- **GOP / キーフレーム**: GOP長＝シーン長。チャンク先頭フレームは必ずIDR（copy結合とシークの前提）。
  - 先頭以降の適応的キーフレーム挿入は**エンコーダー判断に委ねる**（scenecut抑止フラグは廃止。除去のみでx264/x265の既定scenecut(40)が有効化されることを実機確認）。
  - 二分探索との両立根拠: キーフレーム配置はlookaheadのソース解析由来でCRF非依存 → 試行間でGOP構造不変。min-keyint自動値（≈GOP/10）が挿入下限、`-g`＝シーン長が周期IDR上限を保証。過剰挿入はサイズ面で二分探索が自然に排除。
  - 役割分離: シーン境界＝av-scenechangeが権威 / チャンク内I配置＝エンコーダーが権威。
- **追加引数**（`--enc-args`）: ffmpeg出力オプションを試行へ追加渡し。管理対象オプション（-crf/-preset/-vf等）は拒否。`-crf=20`/`--crf` 等の変形も捕捉済み。非オプショントークンは試行でfail-fast。

## 4.3 評価プロファイル制と出力解像度

- **評価プロファイル**（アルゴリズム×評価解像度のセット、`domain.EvalProfile`）:
  - `vmaf_v1.0.16_3d0h`=3d0hモデル@1920x1080（既定）／ `vmaf_4k_v0.6.1`=4Kモデル@3840x2160（pin版ffmpeg同梱を実機確認済み）
  - Nameは `<algorithm>-<resolution>` 形式。Algorithmフィールドで評価器が解釈可能なもののみ受付（libvmaf評価器はlibvmaf以外即エラー）。将来のXPSNR/SSIM等はレジストリ1行＋対応評価器実装で拡張。
  - **フォールバック廃止**（2026-08決定）: 評価失敗・未知名は即エラー（フェイルファスト）。「暗黙にスコア基準が変わる」リスクの排除。
  - スコアは同一プロファイル内でのみ比較可能。
- **`--out-res`**: 出力リサイズ（select後にscale）。未指定＝入力実寸をffprobeで自動検出してそのまま出力（ログ表示）。`WxH` 直接指定またはプリセット（sd/hd/fhd/4k）。シーン検出は常に元解像度で行うため検出精度に影響しない。
- 導入動機（定量根拠は7.2節）: 1080p縮小評価では4Kネイティブの劣化を見逃す。

## 4.4 音声処理

外部意見をもとに精査した4モード構成。**既定は `copy`**。

| モード | 処理 | 用途 |
|---|---|---|
| copy（既定） | 元音声を無劣化コピー | 音質最優先・最速 |
| opus | libopusへ再圧縮（VBR） | 最高圧縮率 |
| aac | AAC（nativeエンコーダ）へ再圧縮 | 互換性重視 |
| none | 音声トラック破棄 | 無音動画・サイネージ |

- **ビットレート自動判定**（ffprobeのchannels。指定不要）: ch<6（mono含む）→ opus/aac とも128k ／ ch>=6（5.1/7.1等）→ opus 256k / aac 320k。native AACに真のVBRが無いため `-b:a` はABR（opusはデフォルトVBR）。
- **アーキテクチャ上の重要決定: 音声はシーン分割の対象外**。完成映像への最終ミックス時に1回だけ処理する（元動画から全長ストリームを直接map）。
  - 理由: 音声フレーム境界はシーンカットと無関係で、チャンク境界切断はAACプライミングサンプル等により各境界でノイズ/欠落が発生する。copy/再圧縮とも単一パスで済み、二分探索ループに音声コストが入らない。
  - 実装: `domain.AudioMuxer`（orchestratorのオプション依存 `Muxer`）。concat → 中間映像（jobDir内）→ mux → 出力。音声なし/none時はconcat直接出力。
- その他の確定事項: 入力に音声が無い場合は全モードで映像のみ出力。ただしopus/aac明示指定×音声なしはエラー（意図表明を強制。copyは妥当）。複数音声ストリームは先頭1本のみ（`1:a:0`。欠落分は冒頭noteログで周知）。opus+MP4コンテナはffmpeg 8.1.2で警告なく動作（推奨コンテナは.mkvのまま）。copyモードで元コーデックがコンテナ非対応ならffmpegがエラーで即失敗（fail-through）。

## 4.5 起動モード判定

| 起動方法 | 引数 | stdout | 動作 |
|---|---|---|---|
| ダブルクリック／裸起動（`engram-opt`） | なし | TTY | **ウィザード**起動 |
| `engram-opt in.mp4` | あり | TTY | 即実行（平文ログ。`--tui`併用でダッシュボード） |
| `--headless` 明示 | あり（入力必須） | 任意 | 対話UI禁止。入力未指定はエラー |
| パイプ／CI／リダイレクト | 任意 | 非TTY | 平文ログで即実行。引数なしならヘルプ表示 |

- `--headless` と `--tui` の同時指定はエラー（全起動経路で排他適用）。
- **cobra mousetrap無効化**（`cobra.MousetrapHelpText = ""` をcliパッケージinitで設定）: ダブルクリック（親プロセスexplorer.exe時のみ発動）でもウィザード直行になる。端末/headless/パイプへの影響ゼロ。
- 保険として、裸起動＋TTY時のみ致命的エラーでも `[Enter]キーで終了します...` 待ちを入れる（`cli.ShouldPauseAfterError()`。引数あり/headless/パイプは即終了）。
- `--shot N` は開発デバッグ専用フラグ（単一シーンのみ探索・結合なし・勝利チャンク保持）。ウィザード項目にはしない。
- `--version`: ldflags埋め込み。配布Zip名と同一バージョンを表示（実バイナリ起動フローはcli/integration_test.goで検証）。

## 4.6 ウィザード項目（Phase 8・実値選択）

方針: **内部実名と実値をそのまま選択肢に出し、全項目に最適な既定値を事前入力**。触らなければ従来の固定仕様と同一挙動。

| 項目 | 既定 | 選択肢 |
|---|---|---|
| 入力ファイル | （空） | パス入力（存在検証・D&Dペースト可） |
| Codec | **AV1**※ | h264 → hevc → av1 をcycle |
| Preset | medium（av1時 "6"） | h264/hevc=x264流9段階（ultrafast〜veryslow）／av1=数値文字列 "1"〜"13"。切替時に当該既定へ自動リセット |
| Min CRF / Max CRF | 15 / 36 | 整数（Min<=Max。x264/x265は実質上限51のためav1用ヘッドルームとして63まで許容） |
| Target VMAF | 95.0 | 50〜100（harmonic_mean基準の実数） |
| Bit Depth | 10 | 10 (yuv420p10le) / 8 (yuv420p) |
| Audio | copy | copy → opus → aac → none |
| Eval Profile | vmaf_v1.0.16_3d0h | アルゴリズム→解像度の2段選択 |
| Out Res | 入力実寸 | native / プリセット / WxH直接入力 |
| Extra Args | （空） | ffmpeg追加出力オプション |

※ウィザード単独起動時の既定はAV1（2026-08変更）。CLI/ヘッドレスの `--codec` フラグ既定はh264のまま。

- 意図的に露出しない項目（構造的固定点）: GOP長・select整数フレーム抽出（-ss禁止）・settb/setpts正規化・shortest=eof_action=endall・評価時1920x1080強制スケール・音声自動ビットレート表・av-scenechange閾値・シーン逐次処理。
- **ウィザード設定の永続化は意図的に未実施**: 「既定値が常に妥当」という設計に対する利得が薄く、永続化層と既定値変更時の不整合コストが上回るため。頻用する非既定値はCLIフラグが担う。

## 4.7 ログ方針

- **stdlib `log` で確定**。構造化ロギングは不採用（出力面が小さい。必要になってから）。
- `charmbracelet/log` は依存ごと削除済み。コンソール装飾はTUI（lipgloss）担当。
- `--log-file <path>`: 追記モードでファイルへ二重化（無人実行向け）。TUI表示中は画面破壊防止のためstderrをUIログ欄へ迂回（`ui.logRouter`、defer復元保証）、ファイル二重化のみ継続（LogMirror）。
- `--tui` のstdout非TTY時: 平文ログへ自動フォールバック（パイプ・CI安全）。

# 5. 外部ツール知見（実測）

## 5.1 av-scenechange v0.24.1

- 用途: 動き補償付きシーン境界検出。Rustソースを `cargo build --release` で単一バイナリ化し `bin/` へ配置。
- 入力に `-` を渡すとstdinからY4Mを読む（FFmpeg stdoutをメモリパイプ直結できる）。
- `--json` フラグは存在しない。デフォルトでstdoutへJSON出力: `{"scene_changes":[0,120],"scores":{...},"frame_count":180,"speed":...}`
- `scene_changes` はカット点リスト（先頭必ず0・終端含まず）。区間は半開区間 `[ci, c(i+1))`、最終終端はframe_count。domain inclusive EndFrameへの変換は `次開始-1`。
- `scores` は巨大なためGo側パーサはstreamingデコーダで scene_changes/frame_count のみ抽出し読み飛ばす。
- 合成クリップ（無音フラット色）では一部カットを見逃すことがあるが、実写向けツールとして問題なし。

## 5.2 FFmpeg 8.1.2 full_build（pin運用）

- GyanD/codexffmpegのGitHubリリースzipをpin（gyan.dev直URLは旧パッケージが404になるためイミュータブルなGitHubリリースURLを採用。digestはRelease Assets APIの公式sha256と照合）。
- 2026-08にessentials→fullへ切替。理由: essentialsには `libsvtav1` が無い（libvmafフィルタ自体はessentialsにも同梱）。
- pin値（URL/SHA256）の実体は `internal/setup` に集約。setupは毎回 libvmafフィルタ＋必須エンコーダ3種（libx264/libx265/libsvtav1）の存在を検証するため、pin差し替えによる機能欠落はセットアップ時点で検知される。
- DL/展開はシェルを経由せずGo標準ライブラリ（net/http, os/exec）で実行。アーカイブ展開のみ純Goの mholt/archives（xz/7z対応）。
- av-scenechange v0.24.1 のライセンスはMIT（dav1d BSD／x264 ISC／rav1e BSD-2+AOM特許条項を含む。タグLICENSEで確認済み）。

## 5.3 libvmaf 実装上の注意（ffmpeg 8.1.2実測）

- フィルタ名は `vmaf` ではなく **`libvmaf`**（8系で改名。`vmafmotion` は別フィルタ）。
- `vmaf_v1.0.16_3d0h` はCAMBI特徴量を含むため低解像度入力（例160x120）だと `no feature 'cambi_hrs_1080_...'` で失敗する → **libvmaf投入前に1920x1080へリサイズ必須**。
- JSONログの `pooled_metrics.vmaf` に `{min,max,mean,harmonic_mean}` が揃っており、per-frame集計は不要。
- **時間基準（timebase）落とし穴**: チャンク側(mkv 1/1000)と元動画側(mp4 1/15360)などtimebaseが違うとframesyncペアリングが丸めズレし、別フレーム同士を比較してスコアが壊滅する（実測: PSNR 28dB/VMAF 20点台まで低下）。対策: 両入力とも `settb=1/{fpsNum},setpts={fpsDen}*N` で共通timebase＋整数刻みPTSへ正規化（fpsはffprobeの `r_frame_rate` を有理数のまま使用。任意の有理数fpsで誤差ゼロ）。
- framesyncは `shortest=1:eof_action=endall` 必須（デフォルトrepeatlastは最終フレームを複製しフレーム数検証を壊す）。
- libvmafのログパスにWindows絶対パス（`C:\...`）を使うとオプション区切り `:` と衝突して失敗 → 作業ディレクトリ相対で出力し `cmd.Dir` を設定。
- 評価フレーム数は参照側select区間数とチャンク側フレーム数の一致を必ず検証（ずれは評価崩壊の前兆のためfail-fast）。

# 6. 堅牢化ポリシー

## 6.1 フェイルファスト統一（2026-08決定）

- **原則**: 暗黙の代替・静かな縮退は禁止。失敗・未知値・契約違反は即座にエラー（プログラマバグはpanic）で表面化させる。ユーザー指定と異なる動作への「親切な」切替は行わない。
- 実装済み: 評価プロファイル未知名即エラー／preset未知名エラー（旧medium黙置換は廃止）／metric旧名エイリアス廃止＋Score契約panic／`--tui`非TTY時はErrNoTTY返却（decideLaunchが事前分岐）／tmp掃除・チャンク後始末の失敗も警告ログ必須。
- **「フォールバック」と呼んでよいのは環境適合のみ**: toolbinレイアウト判定（配布⇄開発。最終解決はバイナリ欠落でフェイルファスト）とlipglossカラー自動適応（装飾のみ）。
- **仕様としての例外**（黙ってではなく常に報告付き）: MinCRFベストエフォート採用（警告ログ＋MetTarget=false）。
- **ゼロ値の扱い**: 「未指定→既定値」は**正規化**と呼びフォールバックと区別する（`Effective*`、out-res空欄＝入力解像度 等。文書化された既定値であり失敗隠蔽ではない）。

## 6.2 パス処理の安全性（監査結論・実機検証済み）

- 全外部プロセス呼び出し（ffmpeg/ffprobe/av-scenechange）はexec.Commandの**引数配列渡し**でシェル不介入。空白・Unicode・引用符は原理的に安全。
- concatリストは自前生成チャンク（jobDir配下ASCII名）のみでユーザーパス非介入。エスケープ（`concatEscape`: `'`→`'\''`）も防御として実装。
- filter_complexへユーザーパスを埋め込まない（元動画は-i入力参照、log_path等はjobDir相対固定名）。
- 出力確定はGo API（MkdirAll＋同一ディレクトリ内rename）。
- 実機検証: 日本語＋空白＋括弧＋クォート入りの入力/出力/VMAF収束をすべてexit 0確認。既知の制限: Windows 260文字制限は上位制約として許容。
- 大小文字同一視付きパス比較（SameAbsPath/IsWithin）はtoolbin/fsutilに一元。

## 6.3 一時領域とライフサイクル

- jobDirは `newJobDir` に一元（PID接尾辞＝同一秒衝突防止）。**成功時丸ごと削除、失敗時は調査のため保持**（パスをログ出力・72h sweepで回収）。
- **最終出力の原子的確定**: 同ディレクトリのステージング `.名前.part-PID.拡張子` へ書き出してrename。**拡張子で終わる名前が必須**——ffmpegは出力フォーマットを拡張子から推定し、`.final.mkv.part-N` のような名前ではInvalid argument(-22)で失敗（実測）。断片はdeferで掃除。ロック済み出力はrename失敗の明確なエラー。
- 出力先制限: jobDir配下だけでなくtmpRoot配下全体を拒否（掃除対象かつZipステージング元のため）。出力≡入力はRequireDistinctPathsで拒否（無検証だと-c copyが元動画をexit 0で上書きする）。出力≡log-file・log-file≡inputも起動時検出。既存ディレクトリへの出力は起動時早期拒否。
- シグナル対応: `signal.NotifyContext`（Ctrl+C/SIGTERM→ctxキャンセル→子プロセス停止。孤児ffmpeg対策）。
- 出力拡張子は.mkv（推奨・全組合せ安全）/.mp4/.webm/.movのみ。出力先ディレクトリは無ければ自動作成。

## 6.4 配布物とライセンス（NOTICES fail-fast）

- 配置規約: 本体隣の `bin/` を `os.Executable()` 基準で解決（PATH依存ゼロ）。`toolbin.DetectLayout` が本体隣bin/の存在を自己検証し、無ければリポジトリ `build/` へフォールバック。tmpも同一base直下（配布 `<本体>/tmp`、開発 `<repo>/build/tmp`）。
- `go run ./cmd/engram-package`: 同梱バイナリ確認 → ビルド → NOTICES生成 → LICENSE/README.txt/tmp配置 → **決定論的Zip**（エントリ時刻エポック固定＝同内容なら同一ハッシュ）→ `dist/engram-opt_<version>_<os>-<arch>.zip`。バージョンはgit describeベース（`-version`で上書き可）。ステージング時tmpは空から作り直す（混入事故の再発防止）。
- **NOTICES方式（全文埋め込み）**: GPLv3は本文同梱が配布要件、MIT/Apacheも著作権＋許諾文同梱が条件のためURL参照では不十分。Goモジュールはビルド済み本体から `go version -m` で**実リンク依存のみ**収集しキャッシュ内LICENSEを実読み（`go mod download -json` で補完）。タグzipにLICENSEが無い依存は `third_party/licenses/modules/` に上流原文をベンダリングしてオーバーライド。FFmpeg GPLv3原文・av-scenechange MIT原文も同様に固定同梱し、生成時に分類ラベル照合で破損・取り違えを検知。
- **unknown・埋め込み不能依存が1件でもあればパッケージング全体を失敗**させる（期待オーバーライドファイル名をエラー表示＝修正ループ不要）。GPL §6対応としてFFmpeg節に無改変convey文言を明記。

## 6.5 品質ゲート

- 自動強制: `go run ./cmd/engram-package` がビルド前に `gofmt -l` ＋ `go vet ./...` を実行（devcheck）。違反時はZip化中止。素の `go build` は迂回可能なため**最終門番はこのpackage経由**。
- 手動監査: `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`（依存追加回避のためゲート外の手動監査）。
- ファジング: 5ターゲット（ParseOutRes/ParseExtraArgs/ParseScoreMetricRoundTrip/ParseAudioMode/ConcatEscape）。シードは通常 `go test` でも毎回実行。深掘りは `-fuzz FuzzXxx -fuzztime 20s`（失敗コーパスはtestdata/fuzzへ落下→コミット）。

# 7. 実測サマリー（2026-08検証の結論）

詳細な実行記録はgitログを参照。ここに結論のみ残す。

## 7.1 コーデック×ソースマトリクス（4コンテナ×4ソース、全てexit 0）

| ソース | h264/medium | av1/preset 6 | 所見 |
|---|---|---|---|
| 720p VP9源 | +47% | +19% | 高効率ソースへの追撃にはビットが必要（AV1で半減） |
| 480p H264源 | -7%（達成7/8） | -14%（達成5/8） | 未達はMinCRF15でもVMAF95未満の難素材 |
| 1080p H264源 | +52% | **-22%** | 同一画質で圧縮率が逆転 |
| 4K HEVC源（4Kモデル評価） | -29% | **-57%** | アーカイブ用途で効果大 |

- サイズ増の根本原因分析: 「高効率ソース × 目標フロア95 × MinCRF下限 × モデル癖」の掛け算でありツールの不具合ではない（crf8でharmonic 98.49に到達することを実証＝スコアリング自体は正常）。品質優先設計として正常挙動。
- SVT-AV1 preset 6は80〜224秒/本で実用的速度。AV1は「サイズ増」ゾーンが大幅に縮小。
- 整合性（全本共通）: フレーム数一致/yuv420p10le/音声copy維持/PCM→MKV無劣化格納/先頭IDR/tmp掃除ゼロ残。
- **回帰保証**: リファクタ群（TUI既定AV1化・フェイルファスト統一・デッドコード除去）前後で同一フィクスチャ4本を再実行→試行数まで完全一致・出力はbyteサイズ一致。決定論性を確認。

## 7.2 評価プロファイルの必要性（定量根拠）

- ツール内95合格の出力を4Kネイティブ再評価すると harmonic 87.45 / 最悪フレーム 80.6 —— **1080p縮小評価は4Kネイティブの劣化を見逃す**（これが `vmaf_4k_v0.6.1@2160p` プロファイル導入の直接動機）。
- 同一4K入力でも4Kモデル評価は1080p評価比で+8.8%のビット要求。4K配信の品質保証には4Kモデルを使用（README案内どおり）。

## 7.3 堅牢性確認（実機）

- 並行実行: 同一tmpRootで2プロセス同時走行→両方exit 0・jobDir分離・自己清掃。
- 連続稼働: 30回超の連続実行で成功時破棄・失敗時保持（72h sweep対象）を確認。
- 正常系裏取り: 静止画（単一シーン）・超短尺12フレーム・相対パス・上書き警告・日本語パス・enc-argsガード回避攻撃（形式/二重ダッシュ/非オプショントークン）はすべて想定どおり。
- AV1→.mp4出力は pin中ffmpegで av01 タグ正常格納（旧メモの「mov muxer拒否」はフィクスチャ生成時の話と判明）。

# 8. 将来課題とチェックリスト

## 8.1 既知のスケーラビリティ課題（意図的に未実施）

試行毎の `select='between(n,S,E)'` は動画先頭から全デコードするため、総デコード量 ≈ 試行数 × Σ(前方フレーム数)。2時間映画（S≈300）では数十時間オーダーの純デコードとなり主用途と衝突する。対策候補:

1. キーフレームアンカーへの事前シーク: ffprobe packet走査でS以前の最終キーフレームを取得し `-ss` 前段＋フレーム番号再基準化（CFR前提の番号整合が正確性の要）
2. シーン単位All-Intraメザニン生成: 試行はメザニンから抽出（中間ディスク書き出しがメモリパイプ方針と衝突）

正確性リスク（フレーム完全一致保証の崩壊）が高いため設計検討を先送り。E2Eのフレーム一致テストが安全網。

## 8.2 未消化チェックリスト（実機検証）

- [ ] 長時間・多シーン: 1〜2時間クラスの実動画（シーン数100以上目安）で無人完走
- [ ] 中断耐性: Ctrl+C中断時に子プロセス（ffmpeg等）が残らないこと
- [ ] Bit Depth 8bitの実TUI確認（CLI経由E2E不可=pixFmtFor単体テストとdomain.Validateで担保済み）
- [ ] TUI表示中の--log-file二重化の実機確認（LogMirror実装済み）

完了済み: Codec 3種／Audio 4モード（hevc実機）／連続稼働30回超／ログ二重化（ヘッドレス）

## 8.3 将来課題

- CLIフラグ化: `--min-crf` / `--max-crf` / `--target` / `--bit-depth`（headless運用の同値指定。現スコープはTUIのみ）
- av-scenechange閾値・音声ビットレート上書きの露出（SceneDetectorインターフェース拡張が必要）
- ウィザードのファイル選択ダイアログ（bubblesに標準ピッカーが無い。D&Dペーストで実用カバー済み）
- 複数音声トラックの選択UI

# 9. 開発履歴（概要）

- **Phase 1〜8**（2026-08前半）: cobra再編とsetup分離 → domain/detector → encoder/evaluator＋単一シーン探索 → orchestrator統合 → bubbleteaダッシュボード → 音声4モード＋配布整備 → TUIウィザード化（3ステージ・起動モード自動判定・--headless）→ ウィザード実値選択化。途中で評価プロファイル制・--out-res/--enc-args・mousetrap無効化によるダブルクリック導線・--version・CI構築を含む。
- **改善フェーズ**（2026-08-23開始）: バグハント5ラウンド——主要修正: AV1プリセット名サイレント置換廃止(f252ae9)／Zip tmp混入防止(9357ba4)／音声なし×opus/aacエラー化(9357ba4)／duration不明誤拒否解消(33fff2e)／既存ディレクトリ出力早期拒否(8ec5c9b)／precheck層新設(0277dcf)／fsutil一元化(5e92bba)／registerRun分割(735bc92)／staticcheck 6件全解消・stderr要約徹底・ファズ拡充(b273735)。加えてコミット規約統一（gitmoji＋Conventional Commits・全92件書換）とドキュメント整理（2026-08-24）。
