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

- **FFmpeg 8.1.2**: gyan.dev の `ffmpeg-8.1.2-essentials_build.zip`（essentialsでもlibvmaf有効を確認済み）。公式 `.sha256` で検証。
- **av-scenechange v0.24.1**: codeload.github.com のタグtarballをSHA256検証後に `cargo build --release` でローカルビルド。
- pin値（URL / SHA256）の実体は `internal/setup` に集約。更新時はここだけ直せばよい。

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

* ツール本体は `os.Executable()` からの相対パス（`../bin/{tool}`）で外部バイナリを呼び出すため、システム環境変数（`PATH`）への依存がゼロ。

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
* 対応エンコーダ: `libsvtav1` (AV1), `libx265` (HEVC), `libx264` (H.264) 等。


* **FFmpeg内蔵 `vmaf_v1.0.16_3d0h.json**`
* 用途: 各シーンの知覚画質評価（最新VMAF v1モデル、フォールバック: `vmaf_v0.6.1neg`）。
* 形式: FFmpegバイナリ内にビルトイン（外部モデルファイルの管理不要）。
* 判定指標: **`harmonic_mean`（調和平均）**
* 単純平均（`mean`）の弱点である「局所的なブロックノイズ（破綻）の見落とし」を防ぎつつ、最小値（`min`）のような外れ値への過敏な反応を抑えた最適な代表値として採用。
* 実装時の注意（ffmpeg 8.1.2 で実測）:
  * フィルタ名は `vmaf` ではなく **`libvmaf`**（8系で改名済み。`vmafmotion` という別フィルタもあるので混同しないこと）。
  * `vmaf_v1.0.16_3d0h` はCAMBI特徴量を含むため、入力が小さい解像度（例: 160x120）だと `no feature 'cambi_hrs_1080_...'` エラーで失敗する。1920x1080入力では動作確認済み → 評価パイプラインでは libvmaf への投入前にリサイズ（または解像度ガード）が必要。
  * フォールバックの `vmaf_v0.6.1neg` は低解像度でも正常動作。





## 二分探索

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

## ログ方針

- 当面はstdlib `log` のまま進める。無人実行のログはファイル出力前提のため、構造化ロギング（`log/slog`）への移行も含めてTUIフェーズ開始時に再検討する。

## 開発フェーズ

1. **Phase 1**: cobra再編（`cmd/engram` 化 ＋ setup移管・旧削除）
2. **Phase 2**: domain型定義 ＋ detector実装（シーン分割JSON出力まで動く）
3. **Phase 3**: encoder / evaluator実装 → 単一シーンで二分探索が通る
4. **Phase 4**: orchestrator統合（全シーン逐次処理）
5. **Phase 5**: TUIダッシュボード（`ui/` 追加、ログ方針の再検討もここ）
