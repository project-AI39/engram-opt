# ツール名

- **`engram opt`**（EngramOpt）
  - 由来: 脳内に物理的に刻まれる記憶痕跡「engram」× 最適化「opt」。
  - コンセプト: 映像という記憶情報を、知覚画質を損なわずに極小の痕跡へと最適化（圧縮）する。


# 背景目的

- 長時間（1日単位など）安定して動作し続ける動画最適化CLIツールを構築する。
- ユーザーにランタイム（Python, Node.js等）の導入やPATH設定、個別ビルドを一切要求せず、Zipを解凍するだけで即座に動作する「完全ポータブル配布」を実現する。
- 「シーン分割」「エンコード」「最新画質評価（VMAF v1）」「CRF二分探索によるPer-Shot最適化」を全自動で実行し、離籍から戻った際にも進捗・削減状況・ログが一目で把握できるTUIダッシュボードを提供する。

# 自動環境構築

## 仕組み
- Go言語製のセットアップコマンド（本体CLI `cmd/engram` の `setup` サブコマンド、実体は `internal/setup`）を使用し、実行環境のOS（Windows / macOS / Linux）およびアーキテクチャ（amd64 / arm64）を自動判別。
- シェルを経由しないクロスプラットフォーム構築を実現。ダウンロード・外部コマンド実行はGo標準ライブラリ（`net/http`, `os/exec`）で直接実行し、アーカイブ展開のみ純Goライブラリ [`mholt/archives`](https://github.com/mholt/archives) を使用（旧archiverのメンテされる後継。標準ライブラリ非対応の xz / 7z も展開可能）：
  1. 各OS向けFFmpeg静的ビルドバイナリの自動ダウンロード＆展開
  2. Rust製ツール（`av-scenechange`）のローカルコンパイル（`cargo build --release`）
  3. パーミッション設定（Unix系: `0755`）および拡張子制御（Windows: `.exe`）
- ローカル開発環境とCI/CD（GitHub Actions）で同一の `go run ./cmd/engram setup` を実行し、環境差異（Dev-CI Parity）を完全に排除。

## バージョンpin（実装済み）

- **FFmpeg 8.1.2 full_build**: GyanD/codexffmpeg のGitHubリリースzipをpin。gyan.dev 直URLは旧パッケージが404になるため、イミュータブルなGitHubリリースURLを採用し、digestはRelease Assets APIの公式sha256と照合した。
  - 2026-08 に essentials → full へ切替。理由: essentials には `libsvtav1` が含まれない（※libvmafフィルタ自体はessentialsにも同梱されており、切替以前からVMAF評価は動作していた）。
- **av-scenechange v0.24.1**: codeload.github.com のタグtarballをSHA256検証後に `cargo build --release` でローカルビルド。
- pin値（URL / SHA256）の実体は `internal/setup` に集約。更新時はここだけ直せばよい。
- setupの `verifyTools` は libvmaf フィルタ＋必須エンコーダ（`libx264` / `libx265` / `libsvtav1`）の存在を毎回検証するため、pin差し替えによる機能欠落はセットアップ時点で検知される。

## 配置
- `build/` ディレクトリ内に、配布用Zipと100%同一のステージング構造を自動生成。
- 配布時は `build/` フォルダをそのまま圧縮（Zip / Tar.gz）するだけで完結。

```text
build/
  ├── optimizer (.exe)         # GoでコンパイルされたCLI本体
  ├── README.txt               # 使い方・初回セットアップ案内
  ├── LICENSE                  # 自作ツールのライセンス (MIT等)
  ├── THIRD-PARTY-NOTICES.txt  # 同梱OSSのライセンス＆ソースコードURL一覧
  ├── bin/                     # 同梱する外部実行バイナリ群
  │    ├── ffmpeg (.exe)
  │    ├── ffprobe (.exe)
  │    └── av-scenechange (.exe)
  └── tmp/                     # 実行時の一時分割チャンク・ログ出力先（実行時自動生成/終了時破棄）

```

* ツール本体は `os.Executable()` からの相対パスで同梱 `bin/{tool}` を呼び出すため、システム環境変数（`PATH`）への依存がゼロ。レイアウト解決は `internal/toolbin` の `DetectLayout` に集約: 「本体の隣に bin/ がある」ことを直接確認する自己検証方式で、bin/ が無ければ開発環境（リポジトリの `build/`）へフォールバックする。旧設計の「`../bin` 相対」（本体が bin/ より1階層深い前提）は配置図と矛盾していたため廃止。

## パッケージング（実装済み）

- コマンド: **`go run ./cmd/engram-package`**（実体は `internal/packaging`）。Dev-CI Parityのためsetup同様Goコマンド一発。
- 手順（冪等・毎回上書き）: 同梱バイナリ存在確認 → `go build -o build/<optimizer>.exe ./cmd/engram` → `THIRD-PARTY-NOTICES.txt` 自動生成 → LICENSE/README.txt/tmp プレースホルダ配置 → `dist/engram-opt_<version>_<os>-<arch>.zip` 作成。
- バージョン名: 既定は `git describe --tags --always --dirty`、未整備なら `snapshot`。`-version` / `-out` / `-no-zip` フラグあり。
- Zipは決定論的（エントリ時刻をエポック固定）→ 同内容なら同一ハッシュ。
- **NOTICES方式**: 「ライセンス種別＋ソースURLの一覧」方式（配置図の定義どおり）。Goモジュールは `go list -m all` の実依存グラフ＋モジュールキャッシュ内LICENSEファイルの実読み分類（推測なし）。FFmpegはGPLv3ビルドである旨と原文URLを明記（本ツールは別プロセス実行のみでリンクなし＝mere aggregation、本体はMIT維持）。ライセンス全文の埋め込みは将来課題。
- av-scenechange v0.24.1 のライセンスは MIT（asm由来のdav1d BSD／x264 ISC／rav1e BSD-2+AOM特許条項を含む。v0.24.1タグのLICENSEで確認済み）。

## 依存ツール

* **`av-scenechange`**
* 用途: 高精度・高速なシーン境界検出（Per-Shot分割）。
* 形式: Rustソースから単一バイナリとしてビルドし `build/bin/` に配置。
* 特徴: 動き補償（Motion Compensation）付き検出。
* 実測（v0.24.1）:
  * 入力に `-` を渡すと stdin から Y4M を読む（FFmpeg stdout をパイプ直結できる）。
  * `--json` フラグは存在しない。デフォルトで stdout へ JSON 出力: `{"scene_changes":[0,120],"scores":{...},"frame_count":180,"speed":...}`
  * `scene_changes` はカット点リスト（先頭必ず0、終端は含まない）。シーン区間は半開区間 `[ci, c(i+1))`、最終終端は `frame_count`。domain の inclusive EndFrame への変換は `EndFrame = 次開始-1`。
  * `scores` は全フレーム分の内訳で巨大になるため、Go側パーサは streaming デコーダで `scene_changes` / `frame_count` のみ抽出し `scores` は読み飛ばす（実装: `internal/detector/avscenechange`）。
  * 合成クリップ（無音フラット色）では一部カットを見逃すことがあるが、実写・実動画コンテンツ向けのツールのため問題なし（2026-08 実測）。


* **`FFmpeg` (AV1 / HEVC / H.264)**
* 用途: 各シーンのエンコード、メディア情報解析（ffprobe）、最終結合（無劣化Concat）。
* 形式: 公式GPL静的ビルドバイナリをダウンロードして `build/bin/` に配置。
* 対応エンコーダ: `libsvtav1` (AV1), `libx265` (HEVC), `libx264` (H.264)。
  * 2026-08: AV1(SVT-AV1) 要件のため FFmpeg を gyan.dev essentials → **full_build**（GitHubリリースpin）へ切替。setupは必須エンコーダ3種の存在検証を行い、統合テストでも全コーデックの実エンコード（10-bit出力・フレーム完全一致）を検証する。


* **FFmpeg内蔵 `vmaf_v1.0.16_3d0h.json**`
* 用途: 各シーンの知覚画質評価（最新VMAF v1モデル、フォールバック: `vmaf_v0.6.1neg`）。
* 形式: FFmpegバイナリ内にビルトイン（外部モデルファイルの管理不要）。
* 判定指標: **`harmonic_mean`（調和平均）**
* 単純平均（`mean`）の弱点である「局所的なブロックノイズ（破綻）の見落とし」を防ぎつつ、最小値（`min`）のような外れ値への過敏な反応を抑えた最適な代表値として採用。
* 実装時の注意（ffmpeg 8.1.2 で実測）:
  * フィルタ名は `vmaf` ではなく **`libvmaf`**（8系で改名済み。`vmafmotion` という別フィルタもあるので混同しないこと）。
  * `vmaf_v1.0.16_3d0h` はCAMBI特徴量を含むため、入力が小さい解像度（例: 160x120）だと `no feature 'cambi_hrs_1080_...'` エラーで失敗する。1920x1080入力では動作確認済み → 評価パイプラインでは libvmaf への投入前にリサイズ（または解像度ガード）が必要。
  * フォールバックの `vmaf_v0.6.1neg` は低解像度でも正常動作。
  * JSONログの `pooled_metrics.vmaf` に `{min, max, mean, harmonic_mean}` が揃っている。合否判定に必要な代表値はすべてここから取れ、per-frame の `frames[]` を集計する必要はない。
  * **時間基準（timebase）落とし穴**: チャンク側(mkv 1/1000)と元動画側(mp4 1/15360)など時間基準が違うと framesync のペアリングが丸めずれし、全く別フレーム同士を比較してスコアが壊滅する（実測: PSNR 28dB / VMAF 20点台まで低下）。対策として両入力とも `settb=1/{fpsNum},setpts={fpsDen}*N` で共通時間基準＋整数刻みPTSへ正規化する（fps は ffprobe の `r_frame_rate` から有理数のまま取得）。任意の有理数fpsで frame duration がちょうど fpsDen tick になり誤差ゼロ。
  * framesync は `shortest=1:eof_action=endall` を指定する（デフォルトの repeatlast は最終フレームを複製しフレーム数検証を壊す）。
  * libvmaf へ渡すログパスに Windows 絶対パス（`C:\...`）を使うとフィルタオプション区切りの `:` と衝突して失敗する → 作業ディレクトリへ相対パスで出力し `cmd.Dir` を設定する。
  * 評価フレーム数は参照側 select 区間数とチャンク側フレーム数の一致を必ず検証する（ずれは評価崩壊の前兆のため fail-fast）。





# 音声処理（Phase 6で実装）

外部意見をもとに精査の上、3モード構成を採用。**既定は `copy`**（旧挙動の暗黙音声削除を解消）。

### モード一覧

| モード | 指定 | 処理 | 用途 |
|---|---|---|---|
| copy（既定） | `--audio copy` | 元音声を無劣化コピー | 音質最優先・最速 |
| opus | `--audio opus` | libopusへ再圧縮（VBR） | 最高圧縮率 |
| aac | `--audio aac` | AAC（nativeエンコーダ）へ再圧縮 | 互換性重視 |
| none | `--audio none` | 音声トラック破棄 | 無音動画・サイネージ |

### ビットレート自動判定（opus/aac時）

ffprobe の `channels` で判定し、数値指定は不要:

- ステレオ扱い（ch < 6、mono含む）: opus 128k / aac 128k
- サラウンド扱い（ch >= 6。5.1/7.1等）: opus 256k / aac 320k

※ native AACエンコーダには真のVBRモードが無いため `-b:a` はABR指定（opusはデフォルトでVBR）。この点を除き外部意見どおり。

### アーキテクチャ上の重要決定: 音声はシーン分割の対象外

音声は **完成映像への最終ミックス時に1回だけ処理する**（元動画から全長ストリームを直接map）。チャンク毎に音声を切って結合する方式は採らない:

- シーン分割はエンコード効率のための **映像概念**。音声フレーム境界はシーンカット位置と無関係であり、チャンク境界で切断するとAACプライミングサンプル等により各境界でノイズ/欠落が発生する
- copy/再圧縮とも単一パスで済み、二分探索の試行ループに音声コストが入らない
- 実装: `domain.AudioMuxer` インターフェース（orchestratorのオプション依存 `Muxer`）。concat → 中間映像ファイル(jobDir内) → mux → 出力。音声なし/none時は従来どおりconcat直接出力

### その他の確定事項

- 入力に音声ストリームが無い場合: 全モードで正常動作（映像のみ出力）
- 複数音声ストリーム: 先頭の1本のみ対応（`1:a:0`）。複数対応は将来拡張
- opus+MP4コンテナ: ffmpeg 8.1.2では警告なく動作を実測済み。ただし推奨コンテナは.mkvのまま
- copyモードで元コーデックが出力コンテナ非対応の場合（例: 一部コーデックのmp4出力）、ffmpegがエラーで即座に失敗する（fail-through）

# 二分探索

### 変更

* **CRF（Constant Rate Factor / 整数値）のみを変更**
* 画質・ファイルサイズとの完全な単調性（単調減少/増加）が担保されるため、整数CRFの二分探索（例: 探索範囲 15〜36）により4〜5回の試行で最速収束。
* シーンごとに `harmonic_mean >= targetScore`（例: 95.0）を満たす最大CRF（＝最小データサイズ）を決定。



### 固定

* **Preset / Speed（エンコード速度・圧縮効率）**
* 探索中の全試行で一律固定（例: SVT-AV1 の `-preset 5` 等）。試行ごとに圧縮特性がブレるのを防止。


* **Pixel Format / Bit Depth（10-bit 推奨）**
* 入力ソースが8-bitであっても `-pix_fmt yuv420p10le`（10-bit）で固定。
* 内部演算の丸め誤差削減とカラーバンディング（階調飛び）防止により、VMAF v1での減点を防ぎ、結果として8-bit出力時よりもファイルサイズを5〜15%削減。


* **GOP / キーフレーム設定**
* シーン単位でチャンク分割するため、各チャンクの先頭フレームのみにIDRキーフレームを打つ。

# モジュール設計とパイプライン連携

## データ構造と管理基準
- **フレーム番号（整数 `int64`）による完全管理**
  - 秒数（`float64`）での管理は、23.976fps/29.97fps などの小数フレームレートで丸め誤差（1フレームの位置ズレ）を招き、画質評価（VMAF）スコアが壊滅的に急落する原因となるため内部処理から排除。
  - `StartFrame` と `EndFrame`（整数）のみを唯一の真実（SSOT）としてシーン境界を管理（秒数はTUIやログの表示時のみ `frame / fps` で算出）。

## モジュール間の連携とフォーマット差異の吸収
- **フォーマット差異のカプセル化（アダプターパターン）**
  - パイプライン制御部（二分探索エンジン）は「フレーム番号」と「ファイルパス」のみを渡し、ツールの入力形式（MP4直接、Y4M生データ、連番画像等）の差異は各モジュール内部で吸収・隠蔽する。
  - 将来新しい評価指標（SSIMULACRA 2, DISTS等）やシーン検出器を追加する際も、コアロジックを修正せずモジュール追加のみで対応可能。
- **メモリパイプによるディスクI/O・容量爆発の防止**
  - 4K等の非圧縮映像（Y4M）を一時ファイルとしてディスクに書き出すと数十〜数百GBの容量を消費するため、ディスク保存は禁止。
  - 生データを要求するツール（`av-scenechange` 等）には、FFmpegの標準出力を RAM 上のパイプ（`os/exec` の `StdoutPipe` / `Stdin`）で直結してメモリ上で処理を完結させる。

# モジュール構成（実装方針）

## ディレクトリ構成

将来拡張用の空モジュールは作らない（YAGNI）。TUI（`ui/`）はTUIフェーズ開始時に追加する。

```text
cmd/
  └── engram/
       └── main.go                # 薄いmainのみ。ロジックは書かない
internal/
  ├── cli/                        # cobra コマンド定義（引数解析と委譲のみ）
  │    ├── root.go
  │    ├── setup.go               # setupサブコマンド定義（処理本体は internal/setup へ委譲）
  │    └── optimize.go            # 本体パイプライン（engine呼び出し）
  ├── setup/                      # 依存関係セットアップ本体（FFmpeg DL・cargo build・検証）
  ├── domain/                     # 共通の型・インターフェース（依存の錨）
  │    ├── scene.go               # シーン境界情報
  │    ├── metrics.go             # 画質評価結果
  │    ├── config.go              # EncodeParams / SearchConfig
  │    └── interfaces.go          # SceneDetector / VideoEncoder / QualityEvaluator
  ├── detector/
  │    └── avscenechange/         # Y4Mパイプ処理・ffprobeでの総フレーム数取得も内包
  ├── encoder/
  │    └── ffmpeg/                # フレーム完全一致のチャンク抽出・codec別preset解決も内包
  ├── evaluator/
  │    └── libvmaf/               # libvmaf投入前の1080pリサイズも内包
  └── engine/
       ├── bsearch.go             # 単一シーンのCRF二分探索
       └── orchestrator.go        # 全体フロー制御の司令塔＋build/tmp管理
```

- 既存のスタンドアロンsetupコマンドはPhase 1でcobra配下へ移管済み（`go run ./cmd/engram setup`）。

## 共通データ契約（domain）

### シーン境界（scene.go）

浮動小数点を排除し、`int64` フレーム番号のみを唯一の真実（SSOT）とする。

```go
type Scene struct {
    Index      int   `json:"index"`
    StartFrame int64 `json:"start_frame"` // 0-indexed
    EndFrame   int64 `json:"end_frame"`   // inclusive
}

func (s Scene) FrameCount() int64 { return s.EndFrame - s.StartFrame + 1 }
```

### 画質評価結果（metrics.go）

合否判定は `harmonic_mean` のみで行う（`mean` / `min` は参考記録値）。

```go
type QualityMetrics struct {
    HarmonicMean float64 `json:"harmonic_mean"` // 合否判定用代表値
    Mean         float64 `json:"mean"`
    Min          float64 `json:"min"`
}

func (q QualityMetrics) TargetMet(target float64) bool { return q.HarmonicMean >= target }
```

### 設定（config.go）

- `Preset` は `string` 型とする。x264/x265は `"medium"` 等の文字列、SVT-AV1は数値のため、実際のエンコーダ引数への解決は `encoder/ffmpeg` 内部で行う。
- 二分探索の探索範囲等は試行パラメータ（`EncodeParams`）と分離し `SearchConfig` に持つ。

```go
type VideoCodec string

const (
    CodecAV1  VideoCodec = "av1"
    CodecHEVC VideoCodec = "hevc"
    CodecH264 VideoCodec = "h264"
)

// EncodeParams 単一試行のパラメータ
type EncodeParams struct {
    Codec    VideoCodec
    CRF      int    // 試行するCRF値
    Preset   string // 全試行で一律固定
    BitDepth int    // 10固定（yuv420p10le）
}

// SearchConfig 二分探索の全体設定（既定値は固定仕様どおり）
type SearchConfig struct {
	Codec       VideoCodec
	MinCRF      int     // 15
	MaxCRF      int     // 36
	TargetScore float64 // 95.0
	Preset      string
}
```

### インターフェース（interfaces.go）

総フレーム数はdetector内部でffprobeにより取得するため、`Detect` の引数から外す（呼び出し側のffprobe依存をなくす）。

```go
type SceneDetector interface {
    Name() string
    // 動画全体を解析し、フレーム単位のシーンリストを返す
    Detect(ctx context.Context, inputPath string) ([]Scene, error)
}

type VideoEncoder interface {
    Name() string
    // 元動画の指定シーン区間を指定パラメータでエンコードする
    EncodeChunk(ctx context.Context, inputPath string, scene Scene, params EncodeParams, outputPath string) error
    // 確定した全チャンクを無劣化結合（concat demuxer + -c copy）する
    ConcatChunks(ctx context.Context, chunkPaths []string, finalOutputPath string) error
}

type QualityEvaluator interface {
    Name() string
    // 元動画の該当区間とエンコード済みチャンクを比較評価する
    Evaluate(ctx context.Context, originalPath string, scene Scene, encodedChunkPath string) (QualityMetrics, error)
}
```

## エンジン設計

- `orchestrator.go` は全体フロー制御の司令塔: 検出 → 各シーンの二分探索 → 結合。処理は当初 **逐次**（シーン並列化は明示的にスコープ外、必要になってから検討）。
- 一時ファイルは `build/tmp/<job-id>/` に隔離し、終了時に破棄する（`build/` は配布Zipと同一構造のステージング領域という規約に従う）。
- **進捗通知は疎結合**: engineがログやTUIに直接依存するとテスト不能になるため、コールバック注入方式とする（例: `ProgressFn func(SceneEvent)`）。CLI実装ではログへ、TUI実装ではbubbleteaモデル更新へ変換する。engineは `ui/` をimportしない。

### 実装メモ（Phase 4 運用方針）

- **一時領域の後始末ポリシー**: 成功時は jobDir を丸ごと削除、**失敗時は調査のため保持**（ログにパスを出力）。1日無人運転での異常解析を優先。
- **デフォルト出力名**: `<入力>.opt.mkv`（入力と同じディレクトリ）。`--out` で変更可。出力先が jobDir 配下だと成功時クリーンアップで消えるため、起動時に拒否する。
- **concatのリストファイル**: ユーザー出力先を汚さないよう一時ディレクトリに生成し実行後に破棄。チャンクは絶対パス参照（`-safe 0`）。
- **`--shot N`**: 単一シーンのみ二分探索するデバッグモード（結合なし・勝利チャンク保持）。

## ログ方針（Phase 5で確定）

- **stdlib `log` で確定**。構造化ロギング（`log/slog`）は現状のログ出力面が小さい（進捗はコールバック、警告数行のみ）ため不採用。必要になってから導入する。
- **`charmbracelet/log` は依存ごと削除**した（未使用のまま終了）。コンソール装飾はTUI（lipgloss）が担当し、無人実行にはプレーンな方が適する。
- **`--log-file <path>`** を追加: 無人実行時にログをファイルへ追記二重化する。
- **TUIモード時**: stdlib log の出力は画面破壊防止のためUIログ欄へ迂回される（`ui.logRouter`）。復元はdeferで保証。`--log-file` 併用時は `LogMirror` 経由でファイルへのみ二重化を継続（stderrへは出さない）。
- **`--tui`**: stdoutが端末でない場合は自動的に平文ログへフォールバック（パイプ・CI・リダイレクト安全）。

## TUIウィザード化（Phase 7・実装済み）

外部意見（ダブルクリック対応・3フェーズTUI）を現行設計に照らして精査・修正したうえで実装完了。
目的: ライトユーザーには「起動してポチポチ選ぶだけのツール」、スクリプトユーザーには
「引数で回すCLI」として単一バイナリで両立させる。

### 採用する要素

- **3フェーズTUI**（bubbleteaの1アプリ内で状態遷移）: 設定ウィザード → 実行ダッシュボード（既存UI流用） → 完了サマリー
- **完了サマリーでキー待ち**: 処理完了直後にコンソールが消える問題（ダブルクリック起動時）の対策。TUIセッション終了時のみ待ち、headless実行では待たない
- **引数なし＋TTY起動でウィザード**: ルートコマンド（`engram` 裸）と `optimize` 引数なしの両方。現在はルートがヘルプ表示・optimizeがエラーのため変更が必要
- **D&Dの初期値化**: エクスプローラからのD&Dは `os.Args[1]` として届く。ただし下記の判定表のとおり、引数あり起動は即実行系に寄せるため、正式な初心者フローは「ウィザードの入力欄へD&D（パスがペーストされる）」

### 意見からの修正点（重要）

| 意見 | 修正 | 理由 |
|---|---|---|
| 引数あり＋TTYでもTUI確認画面を出す | **引数あり＋TTYは即実行**（平文ログ。--tuiで明示時のみダッシュボード） | ローカルのバッチ/スクリプトもTTY付きで動くため、引数ありで対話画面を出すと自動化が壊れる。意見自身の「スクリプトユーザー」要求と矛盾 |
| D&D直接起動＝プリセット済み設定画面 | D&D直接起動は**即実行**扱い。初心者の正規フローは「ダブルクリック→ウィザード（入力欄へD&Dペースト可）」へ誘導 | D&Dとターミナル引数起動は引数だけでは区別不能。親プロセス判定等は脆弱なので採らない（KISS） |
| 並列ワーカー数の設定・Worker表示 | **削除** | シーン逐次処理は固定方針（「エンジン設計」参照）。並列化は明示的にスコープ外 |
| 目標VMAFの編集（`--vmaf`） | **保留**（別途決定） | 探索パラメータ固定仕様（CRF 15〜36・目標95.0）に関わる変更のため、本計画には含めない |
| プリセット3段階ラベル（最高画質/バランス/極限圧縮） | 圧縮効率vs速度の3段階として採用: `slow` / `medium`(既定) / `fast` をcycle選択 | SVT-AV1の数値との差異は既存のsvtPreset解決層が吸収する |
| mattn/go-isatty によるTTY判定 | **不採用**。既存の stdlib 判定（`os.Stdout.Stat()` + `ModeCharDevice`、ui.ErrNoTTY）を流用 | 依存追加の必要がない |
| ファイル選択ダイアログ | v1は**パス入力フィールド**（存在検証付き） | bubblesに標準ファイルピッカーが無い。Windows Terminal等は入力欄へのD&Dでパスが貼り付くため実用上カバーされる。本格的なピッカーは将来拡張 |

### 起動モード判定（最終形）

| 起動方法 | 引数 | stdout | 動作 |
|---|---|---|---|
| ダブルクリック／ショートカット（`engram` 裸） | なし | TTY | **ウィザード**起動 |
| `engram optimize`（引数なし） | なし | TTY | **ウィザード**起動 |
| ターミナルで `engram optimize in.mp4` | あり | TTY | **即実行**（平文ログ）。`--tui` 併用でダッシュボード |
| `--headless` 明示 | 任意 | 任意 | 対話UIを一切出さず平文ログで実行（将来の完了待ち抑止も兼ねる） |
| パイプ／CI／リダイレクト | 任意 | 非TTY | 平文ログで即実行（現行フォールバック踏襲）。引数なしの場合はヘルプ表示 |

- `--headless` と `--tui` の同時指定はエラー
- `--shot` は開発者デバッグ用のためウィザード項目にしない（フラグ専用）

### ウィザード項目（v1・既存フラグと同じ範囲のみ）

```
入力ファイル : [ パス入力（存在検証。D&Dペースト可）        ]
コーデック   : < h264 >            （h264 → hevc → av1 をcycle）
プリセット   : < バランス >        （高圧縮(slow) → バランス(medium) → 高速(fast)）
音声         : < copy >            （copy → opus → aac → none）
出力先       : [ 空=既定(<入力>.opt.mkv)                            ]

  [Enter: 最適化開始]   [q: 終了]
※ 探索仕様（CRF 15〜36 / VMAF目標95.0）は固定。画面下部に明記する。
```

### 実装メモ

- `ui.Model` に `stage`（setup / running / summary）を追加し、既存ダッシュボードは running 相当として流用。`pipelineDoneMsg` で即quitせず summary へ遷移し、q/Enter でquitする。**失敗時もFAILED表示でキー待ち**に変更（エラーを読む前にコンソールが消える問題の対策）。model_test の旧quit前提アサートは更新済み
- CLI側: ルートコマンドと optimize の Args を0または1へ緩め、`decideLaunch(引数有無, TTY, --tui, --headless)` の純関数で起動モードを決定（単体テストでマトリクス照合）。engine（Orchestrator/BisectScene）は無変更
- サマリー表示内容: 出力先／サイズ削減率／シーン別採用CRF一覧／達成率
- 実装上の要点: ①tea.Program生成前にModelへ `sender` 間接層（ポインタレシーバ）を埋め込み、生成後の prog.Send 差し替えに対応 ②値レシーバのUpdate内でフォーム状態を変更する箇所は「更新後のModelを返す」パターンで統一（confirmWizard等） ③Enter連打によるパイプライン二重起動は `wiz.starting` フラグで防止 ④TTY判定はstdlib完結（`os.Stdout.Stat()`+`ModeCharDevice`）を `ui.IsTerminal()` として公開

## 開発フェーズ

1. **Phase 1**: cobra再編（`cmd/engram` 化 ＋ setup移管・旧削除）
2. **Phase 2**: domain型定義 ＋ detector実装（シーン分割JSON出力まで動く）
3. **Phase 3**: encoder / evaluator実装 → 単一シーンで二分探索が通る
4. **Phase 4**: orchestrator統合（全シーン逐次処理）
5. **Phase 5**: TUIダッシュボード（`ui/` 追加、ログ方針の再検討もここ）
6. **Phase 6**: 音声3モード（`--audio copy/opus/aac/none`、最終ミックス方式）＋配布物整備
7. **Phase 7**: TUIウィザード化（上記「TUIウィザード化」節の計画どおり）
