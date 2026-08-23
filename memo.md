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
- Go言語製のセットアップコマンド（本体CLI `cmd/engram-setup`（開発者専用バイナリ・ランタイムから分離）、実体は `internal/setup`）を使用し、実行環境のOS（Windows / macOS / Linux）およびアーキテクチャ（amd64 / arm64）を自動判別。
- シェルを経由しないクロスプラットフォーム構築を実現。ダウンロード・外部コマンド実行はGo標準ライブラリ（`net/http`, `os/exec`）で直接実行し、アーカイブ展開のみ純Goライブラリ [`mholt/archives`](https://github.com/mholt/archives) を使用（旧archiverのメンテされる後継。標準ライブラリ非対応の xz / 7z も展開可能）：
  1. 各OS向けFFmpeg静的ビルドバイナリの自動ダウンロード＆展開
  2. Rust製ツール（`av-scenechange`）のローカルコンパイル（`cargo build --release`）
  3. パーミッション設定（Unix系: `0755`）および拡張子制御（Windows: `.exe`）
- ローカル開発環境とCI/CD（GitHub Actions）で同一の `go run ./cmd/engram-setup` を実行し、環境差異（Dev-CI Parity）を完全に排除。

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
  ├── engram-opt (.exe)         # GoでコンパイルされたCLI本体
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
- 手順（冪等・毎回上書き）: 同梱バイナリ存在確認 → `go build -o build/engram-opt.exe ./cmd/engram` → `THIRD-PARTY-NOTICES.txt` 自動生成 → LICENSE/README.txt/tmp プレースホルダ配置 → `dist/engram-opt_<version>_<os>-<arch>.zip` 作成。
- バージョン名: 既定は `git describe --tags --always --dirty`、未整備なら `snapshot`。`-version` / `-out` / `-no-zip` フラグあり。
- Zipは決定論的（エントリ時刻をエポック固定）→ 同内容なら同一ハッシュ。
- **NOTICES方式（Phase 8で全文埋め込みに強化済み）**: 同梱物ごとに**ライセンス全文を逐字埋め込み**する。GPLv3は本文同梱が配布要件、MIT/Apacheも著作権表示＋許諾文の同梱が条件のためURL参照では不十分（コンプライアンス精査の結果方式変更）。
  - Goモジュール: ビルド済み本体から `go version -m` で**実リンク依存のみ**収集し、モジュールキャッシュ内LICENSEファイルを実読みして埋め込む。`go mod download -json` によりキャッシュ欠けも自動補完。旧 `go list -m all` 方式（ビルド不要なテスト依存まで列挙しunknown多数）からの置き換え。
  - タグzipにLICENSEが含まれない依存（実例: mikelolasagasti/xz v1.0.1）は、上流原文を `third_party/licenses/modules/<path>_<ver>_LICENSE.txt` としてベンダリングしオーバーライドする。mattn/go-localereader は同様の事例のためLICENSE追加済みリビジョンへ更新。
  - FFmpeg GPLv3原文・av-scenechange MIT原文は `third_party/licenses/` 配下に公式ソースから取得したものを固定同梱。生成時に分類ラベル照合を行い破損・取り違えを出荷前に検知する。
  - **unknown・埋め込み不能依存が1件でもあればパッケージング全体を失敗させる**（fail-fast）。全件一括列挙のため修正ループが不要。fail-fast時は期待されるオーバーライドファイル名をエラー文に表示（再ベンダリング手順内蔵）。
  - GPL §6対応としてFFmpeg節に「無改変のまま convey・Corresponding Source は同URLから取得可能」の文言を明記。
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

# パラメータ一覧と露出方針（Phase 8）

外部意見「抽象ラベル（高速/高圧縮等）はかえって分かりにくい。最低限の知識がある人向けに内部パラメータをそのまま選択させたい」を受けて全パラメータを棚卸しした。
方針: **内部で実際に使われている名前と値をそのまま選択肢として出し、全項目に最適な既定値を事前入力する**。触らなければ従来の固定仕様と同一の挙動。

### A. ウィザードへ露出するチューナブル（今回実装）

| # | 表示名 | 内部実体 | 既定 | 選択肢・範囲 |
|---|---|---|---|---|
| 1 | Codec | SearchConfig.Codec | h264 | h264 / hevc / av1 |
| 2 | Preset | EncodeParams.Preset | medium | **実値リスト**: h264/hevc = x264流9段階（ultrafast / superfast / veryfast / faster / fast / medium / slow / slower / veryslow）。av1 = 数値文字列 "1"〜"13"（svtPresetの数値透過経路）。コーデック切替時は当該既定（medium / "6"）へ自動リセット |
| 3 | Min CRF | SearchConfig.MinCRF（旧: DefaultMinCRF=15 定数のみで露出なし） | 15 | 整数 0〜63 |
| 4 | Max CRF | SearchConfig.MaxCRF（旧: DefaultMaxCRF=36 定数のみ） | 36 | 整数 0〜63 かつ Min<=Max。※x264/x265は51超でffmpegが失敗するため実質上限は51、av1用ヘッドルームとして63まで許容 |
| 5 | Target VMAF | SearchConfig.TargetScore（旧: DefaultTargetScore=95.0 定数のみ） | 95.0 | harmonic_mean基準の実数。0 < t <= 100（ウィザード入力は50〜100に制限）。Phase 7で「保留」とした --vmaf 相当を本方針で解消 |
| 6 | Bit Depth | SearchConfig.BitDepth → EncodeParams.BitDepth（旧: encoder.go が yuv420p10le をハードコードし BitDepth 未参照だった） | 10 | 8 (=yuv420p) / 10 (=yuv420p10le)。encoder を params 参照に修正 |

- 検証は `domain.SearchConfig.Validate()` に一元化し、ウィザード確定時とCLI構築時の双方で呼ぶ。BitDepth==0 は「未指定＝既定10」扱いとし、既存テストのSearchConfigリテラルを壊さない。

### B. 既に露出済み（変更なし）

入力ファイル／音声モード（copy/opus/aac/none）／出力先（空=<入力>.opt.mkv、ensureOutside検証込み）。

### C. 意図的に露出しない（構造的固定点）

| 項目 | 露出しない理由 |
|---|---|
| GOP長＝シーン長（`-g`） | 周期IDRがチャンク内に侵入しない上限保証。先頭IDR必須はcopy結合とシークの前提 |
| 先頭以降の適応キーフレーム（scenecut等） | エンコーダー既定に委ねる方針（2026-08〜）。抑止は廃止。詳細は「エンコード仕様」節 |
| select整数フレーム抽出（-ss禁止） | フレーム完全一致の根幹（浮動小数点秒は評価崩壊の実測あり） |
| settb/setpts時間基準正規化＋shortest=eof_action=endall | VMAFペアリング正確性の根幹（実測済み） |
| 評価時1920x1080強制スケール | vmaf_v1.0.16_3d0h のCAMBI特徴量の要求（低解像度で失敗） |
| VMAFモデル対（3d0h主力＋0.6.1neg自動フォールバック） | 信頼性機構であり選好パラメータではない |
| シーン逐次処理 | 固定方針 |
| 音声自動ビットレート表（ch<6→128k、>=6→opus 256k/aac 320k） | opus/aac選択時の内部詳細。上書きは将来課題 |
| av-scenechange検出閾値 | 現状デフォルト呼び出し。SceneDetectorインターフェース拡張が必要 → 将来課題 |
| --shot N | 開発デバッグ専用フラグ（ウィザード対象外の決定を維持） |

### D. 将来課題

- CLIフラグ化（`--min-crf` / `--max-crf` / `--target` / `--bit-depth`）: headless運用での同値指定。今回スコープはTUIのみ
- av-scenechange閾値・音声ビットレート上書きの露出

# 二分探索

### 変更

* **CRF（Constant Rate Factor / 整数値）のみを変更**
* 画質・ファイルサイズとの完全な単調性（単調減少/増加）が担保されるため、整数CRFの二分探索（例: 探索範囲 15〜36）により4〜5回の試行で最速収束。
* シーンごとに `指標スコア >= targetScore`（例: 95.0）を満たす最大CRF（＝最小データサイズ）を決定。
* **合否基準指標は選択可能**（2026-08〜）: `harmonic_mean`（既定。libvmaf JSONキーと同一の実名）/ `mean` / `min`。CLIは `--metric`、ウィザードは Metric フィールドで選択。
  - harmonic_mean: 調和平均。低スコア帧のペナルティが最大（知覚的代理として最も保守的）
  - mean: 算術平均。良好フレームが悪いフレームを補うため最も緩い
  - min: 最悪1フレーム基準。単一の落ち帧で目標不可になり得るため、MinCRFベストエフォート落ちが増え得る（仕様として受容）
  - 3統計ともlibvmafのpooled_metricsから取得済みであり、二分探索の単調性前提は各指標で成立する



### 固定

* **Preset / Speed（エンコード速度・圧縮効率）**
* 探索中の全試行で一律固定（例: SVT-AV1 の `-preset 5` 等）。試行ごとに圧縮特性がブレるのを防止。


* **Pixel Format / Bit Depth（10-bit 推奨）**
* 入力ソースが8-bitであっても `-pix_fmt yuv420p10le`（10-bit）で固定。
* 内部演算の丸め誤差削減とカラーバンディング（階調飛び）防止により、VMAF v1での減点を防ぎ、結果として8-bit出力時よりもファイルサイズを5〜15%削減。


* **GOP / キーフレーム設定**
* チャンクの**先頭フレームは必ずIDR**（ストリームコピー結合と正確なシークの前提。全エンコーダ共通の保証）。
* **先頭以降の適応的キーフレーム挿入はエンコーダー判断に委ねる**（2026-08 方針変更）。
  - 圧縮効率・品質の双方で、検出器が分割しなかった弱い遷移へのIフレーム配備をエンコーダーに任せる方が有利
  - 抑止フラグ（x264 `-sc_threshold 0` / x265 `-x265-params scenecut=0`）は廃止。実測: 除去のみで既定scenecut(40)が有効化される
  - **二分探索との両立**: キーフレーム配置はlookaheadのソース解析由来（SATDコスト比）で決まりCRF非依存のため、試行間でGOP構造は不変。単調性前提を実質的に維持する
  - **IDRストーム対策**: エンコーダー既定のmin-keyint（自動 ≈ GOP/10）が挿入間隔の下限を保証し、`-g`＝シーン長が周期IDRの上限を保証する。加えて二分探索自体が「無駄なIフレームで膨らんだ試行」をサイズ面で評価するため、過剰挿入は自ずと採用されにくい
  - 役割分離: シーン境界（チャンク分割点）= av-scenechangeが権威 / チャンク内のI配置 = エンコーダーが権威

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
  │    ├── run.go                 # 即実行系（RunE・decideLaunch・buildSearchConfig）
  │    ├── jobdir.go              # ジョブ一時領域の生成・72h掃除
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

- 既存のスタンドアロンsetupコマンドはPhase 1でcobra配下へ移管済み（`go run ./cmd/engram-setup`）。

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
- **引数なし＋TTY起動でウィザード**: ルートコマンド（`engram` 裸）と `optimize` 引数なしの両方。現在はルートがヘルプ表示・optimizeがエラーのため変更が必要（→ Phase 7で実装済み。その後のoptimize廃止により、現行は `engram-opt` 裸が単一導線）
- **D&Dの初期値化**: エクスプローラからのD&Dは `os.Args[1]` として届く。ただし下記の判定表のとおり、引数あり起動は即実行系に寄せるため、正式な初心者フローは「ウィザードの入力欄へD&D（パスがペーストされる）」

### 意見からの修正点（重要）

| 意見 | 修正 | 理由 |
|---|---|---|
| 引数あり＋TTYでもTUI確認画面を出す | **引数あり＋TTYは即実行**（平文ログ。--tuiで明示時のみダッシュボード） | ローカルのバッチ/スクリプトもTTY付きで動くため、引数ありで対話画面を出すと自動化が壊れる。意見自身の「スクリプトユーザー」要求と矛盾 |
| D&D直接起動＝プリセット済み設定画面 | D&D直接起動は**即実行**扱い。初心者の正規フローは「ダブルクリック→ウィザード（入力欄へD&Dペースト可）」へ誘導 | D&Dとターミナル引数起動は引数だけでは区別不能。親プロセス判定等は脆弱なので採らない（KISS） |
| 並列ワーカー数の設定・Worker表示 | **削除** | シーン逐次処理は固定方針（「エンジン設計」参照）。並列化は明示的にスコープ外 |
| 目標VMAFの編集（`--vmaf`） | **保留**（別途決定） | 探索パラメータ固定仕様（CRF 15〜36・目標95.0）に関わる変更のため、本計画には含めない |
| プリセット3段階ラベル（最高画質/バランス/極限圧縮） | ~~採用~~ → **Phase 8で廃止**。抽象ラベルより実値選択が適切という外部意見を受け、「パラメータ一覧と露出方針（Phase 8）」節の実値リスト選択へ置換 | SVT-AV1の数値との差異は既存のsvtPreset解決層が吸収する |
| mattn/go-isatty によるTTY判定 | **不採用**。既存の stdlib 判定（`os.Stdout.Stat()` + `ModeCharDevice`、ui.ErrNoTTY）を流用 | 依存追加の必要がない |
| ファイル選択ダイアログ | v1は**パス入力フィールド**（存在検証付き） | bubblesに標準ファイルピッカーが無い。Windows Terminal等は入力欄へのD&Dでパスが貼り付くため実用上カバーされる。本格的なピッカーは将来拡張 |

### 起動モード判定（最終形）

| 起動方法 | 引数 | stdout | 動作 |
|---|---|---|---|
| ダブルクリック／ショートカット／端末での裸起動（`engram-opt`） | なし | TTY | **ウィザード**起動 |
| ターミナルで `engram-opt in.mp4` | あり | TTY | **即実行**（平文ログ）。`--tui` 併用でダッシュボード |
| `--headless` 明示 | **あり（必須）** | 任意 | 対話UIを一切出さず平文ログで実行。入力未指定時はエラー終了（将来の完了待ち抑止も兼ねる） |
| パイプ／CI／リダイレクト | 任意 | 非TTY | 平文ログで即実行（現行フォールバック踏襲）。引数なしの場合はヘルプ表示 |

※ 上2行は Phase 7 計画時の `engram` / `engram optimize` 表記を、optimizeサブコマンド廃止後の現行CLI名に更新済み。

- `--headless` と `--tui` の同時指定はエラー
- `--shot` は開発者デバッグ用のためウィザード項目にしない（フラグ専用）。入力必須
- 実装上の確定事項: RunE は全起動経路を単一の `decideLaunch` へ通す。
  かつて裸起動のみバイパス経路（`bareRunE`）だったため、`--headless` 裸起動が
  ウィザードを開く・排他チェックが効かない漏れがあった（2026-08 レビューで発見・統合済み）。
  bare+TTY=ウィザード、bare+非TTY=ヘルプ（`launchHelp`）、bare+`--headless`=エラー。
  フラグ値（--codec/--preset/--out 等）はウィザード初期値としても反映される。ただしウィザード単独起動（--codec未指定）の初期選択CodecはAV1（2026-08変更。CLI/ヘッドレスのフラグ既定h264は不変）

### ダブルクリック起動の確定実装（cobra mousetrap無効化）

上表の「ダブルクリック→ウィザード」を実現するには cobra 既定機能の解除が必須:
cobraは親プロセスが explorer.exe（＝ダブルクリック）のとき **mousetrap** 機能により
「This is a command line tool.」を表示してコマンドを実行せず終了する。

- 対策: `cobra.MousetrapHelpText = ""` を internal/cli パッケージの `init()` で設定。
  mousetrapは explorer.exe 親のときしか発動しないため、端末起動・headless・パイプへの影響はゼロ
- 保険として、TUI開始前の致命的エラー（bin/不在等）でも窓が瞬時に閉じないよう、
  **裸起動＋TTY のときのみ** main.go 側で `[Enter]キーで終了します...` 待ちを入れる
  （`cli.ShouldPauseAfterError()`。引数あり／headless／パイプは従来どおり即終了）

### ウィザード項目（Phase 8改訂・実値選択）

```
入力ファイル : [ パス入力（存在検証。D&Dペースト可）        ]
Codec        : < h264 >      （h264 → hevc → av1 をcycle）
Preset       : < medium >    （実値: ultrafast..veryslow 9段階／av1時は "1".."13"）
Min CRF      : [ 15 ]
Max CRF      : [ 36 ]
Target VMAF  : [ 95.0 ]      （harmonic_mean >= この値 の最大CRFを採用）
Bit Depth    : < 10 >        （10 = yuv420p10le / 8 = yuv420p）
Audio        : < copy >      （copy → opus → aac → none）
Output       : [ 空=<入力>.opt.mkv                            ]

  [Enter] 最適化開始   [Esc/Ctrl+C] 終了
※ 全項目に最適な既定値を事前入力。触らなければ従来の固定仕様と同一の挙動
```

### 実装メモ

- `ui.Model` に `stage`（setup / running / summary）を追加し、既存ダッシュボードは running 相当として流用。`pipelineDoneMsg` で即quitせず summary へ遷移し、q/Enter でquitする。**失敗時もFAILED表示でキー待ち**に変更（エラーを読む前にコンソールが消える問題の対策）。model_test の旧quit前提アサートは更新済み
- CLI側: ルートコマンドと optimize の Args を0または1へ緩め、`decideLaunch(引数有無, TTY, --tui, --headless)` の純関数で起動モードを決定（単体テストでマトリクス照合）。engine（Orchestrator/BisectScene）は無変更
- サマリー表示内容: 出力先／サイズ削減率／シーン別採用CRF一覧／達成率
- 実装上の要点: ①tea.Program生成前にModelへ `sender` 間接層（ポインタレシーバ）を埋め込み、生成後の prog.Send 差し替えに対応 ②値レシーバのUpdate内でフォーム状態を変更する箇所は「更新後のModelを返す」パターンで統一（confirmWizard等） ③Enter連打によるパイプライン二重起動は `wiz.starting` フラグで防止 ④TTY判定はstdlib完結（`os.Stdout.Stat()`+`ModeCharDevice`）を `ui.IsTerminal()` として公開
- **ウィザード設定の永続化は意図的に未実施**（2026-08決定）: 前回値の保存・復元は「デフォルト値が常に妥当である」設計に対する利得が薄く、永続化レイヤーと既定値変更時の不整合コストが上回るため。頻繁に同じ非既定値を使う層はCLIフラグが担う。将来需要が顕在化した場合は「変更項目のみ記憶」の最小形を再検討する

## 設計強化の記録（第2次根本レビュー反映）

- **CLI構成の再編（2026-08）**:
  - `optimize` サブコマンドを廃止。入力動画はrootの位置引数で直接受ける（`engram-opt <input>` で即実行、引数なし＋端末ならウィザード）。単一目的ツールにサブコマンド層は不要
  - setupを開発者専用バイナリ **`cmd/engram-setup`** へ分離。ランタイム本体 `cmd/engram-opt` からダウンロード/展開機能を排除（出番ゼロの死に重量と攻撃面の削減）。利用者のZipにはbin/同梱済みのためsetup不要
  - Dev-CI Parityは `go run ./cmd/engram-setup` を維持

- **TUIデザインシステム刷新（Phase 5/7の見た目のみ・挙動は不変）**: `internal/ui/style.go` にテーマを一元化（パレット: バイオレット=ブランド/フォーカス、シアン=進行中、緑/オレンジ/赤=合否/失敗）。部品は角丸パネル（titledPanel/plainPanel）、chip、keyHint、phaseBadge、statBlock。桁揃え規約: **先に `%-*s` パディング→着色**（逆順はANSIエスケープが幅に算入され色違いセルで崩れる。旧実装の潜在バグ）。列幅は `tableCol` をヘッダ/行で共有。テストは非TTYでlipglossがASCIIフォールバックするため全文字列アサートがそのまま成立。

- **キーフレーム方針の転換（2026-08）**: 「IDR先頭のみ」から「先頭IDR必須＋以降はエンコーダー任せ」へ。抑止フラグ廃止の実機検証: フラグ除去だけでx264/x265の既定scenecut(40)が有効化される（赤→青ハードカットでframe60に追加Kを確認）。なおscenecut発火はエンコーダー判断で、testsrc2→smptebars遷移はh264が発火・x265が不発という差異も実測（統合テストはコーデック毎に適切な信号源を使用）。SVT-AV1(scd)は小さいフィクスチャでは発火せず。

- **出力=入力同一パスの禁止**: `engine.RequireDistinctPaths` をorchestrator入口とウィザードファクトリで検証（Windowsは大小文字同一視）。結合はconcat demuxer経由のためffmpeg自己保護が発火せず、無検証だと`-c copy`が元動画をexit 0で上書きする。
- **最終出力の原子的確定**: ユーザーパス直書きを廃止し、同ディレクトリのステージング（`.名前.part-PID.拡張子`）へ書き出してからrename。**拡張子で終わる名前が必須**——ffmpegは出力フォーマットを拡張子から推定し、`.final.mkv.part-N`のような名前ではInvalid argument(-22)で失敗する（実測）。失敗時の断片はdeferで掃除。
- **シグナル対応**: `signal.NotifyContext`（Ctrl+C/SIGTERM→ctxキャンセル→CommandContextの子プロセス停止）。孤児ffmpeg対策。
- **tmpライフサイクル**: jobDir生成は`newJobDir`に一元化（PID接尾辞＝同一秒衝突防止、全起動経路で使用）。起動時に72時間超のstaleジョブディレクトリを掃除（ベストエフォート）。
- **HEVCシーンカット抑止の実効化**: `-sc_threshold 0` はlibx264専用AVOptionでlibx265には黙って無視される（プレースボ）。libx265は `-x265-params scenecut=0` が正解。
- **評価ログの配置と保持**: VMAFレポートはjobDir配下の専用サブディレクトリに出す（%TEMP%禁止規約準拠）。掃除対象は自分が作ったサブディレクトリのみ（呼び出し側所有ディレクトリを消さない）。成功時のみ削除、失敗時は証拠として保持。モデルフォールバック時は警告ログ（スコアのモデル間非比較可能性の明示）。
- **Evaluate契約の変更**: `QualityEvaluator.Evaluate(..., workDir)` に作業領域引数を追加。評価成果物はシーン作業領域に属するという所有権の明確化。

### パス処理の安全性（監査記録・2026-08実機検証済み）

ユーザー指定パス（日本語・空白・括弧・シングルクォート）に関する監査結論:

- **argv配列渡しの徹底**: 全外部プロセス呼び出し（ffmpeg/ffprobe/av-scenechange）は
  `exec.Command` の引数配列で、シェルを介さない。空白・Unicode・引用符は原理的に安全
- **concatリストへのユーザーパス非介入**: 結合対象は自前生成チャンク（jobDir配下のASCII名）のみ。
  リスト内エスケープ（`concatEscape`: `'` → `'\''`）も防御として実装済み
- **filter_complexへのユーザーパス非介入**: VMAF評価の元動画は `-i` 入力として参照し、
  フィルタグラフ内にはパスを埋め込まない（log_path等はjobDir相対の固定名）
- **出力確定はGo API**: `MkdirAll` + 同一ディレクトリ内 `os.Rename` のため未存在ネスト先でも動作
- 実機検証: 日本語＋空白＋括弧＋クォート入りの入力/既定出力/-o深ネスト/音声copyミックス/
  VMAF収束（harmonic 60→97で正常二分探索）をすべてexit 0で確認。単一シーン入力も
  TestOrchestratorRunSingleScene で恒久化
- 既知の制限: Windowsの260文字制限（LongPathsEnabled無効環境）はGo/ffmpeg共通の上位制約として許容

### 既知のスケーラビリティ課題（意図的に未実施）

試行毎の `select='between(n,S,E)'` は動画先頭から全デコードするため、総デコード量 ≈ 試行数 × Σ(前方フレーム数)。2時間映画（S≈300）では数十時間オーダーの純デコードとなり、主用途（長時間動画）と衝突する。対策候補:
1. キーフレームアンカーへの事前シーク: ffprobe packet走査でS以前の最終キーフレームを取得し `-ss` 前段＋n番号再基準化。 CFR前提の番号整合（pts×fps丸め）が正確性の要
2. シーン単位All-Intraメザニン生成: 試行はメザニンから抽出。ただし中間ディスク書き出しがメモリパイプ方針と衝突

正確性リスク（フレーム完全一致保証の崩壊）が高いため設計検討を先送り。E2Eのフレーム一致テストが安全網。


1. **Phase 1**: cobra再編（`cmd/engram` 化 ＋ setup移管・旧削除）
2. **Phase 2**: domain型定義 ＋ detector実装（シーン分割JSON出力まで動く）
3. **Phase 3**: encoder / evaluator実装 → 単一シーンで二分探索が通る
4. **Phase 4**: orchestrator統合（全シーン逐次処理）
5. **Phase 5**: TUIダッシュボード（`ui/` 追加、ログ方針の再検討もここ）
6. **Phase 6**: 音声3モード（`--audio copy/opus/aac/none`、最終ミックス方式）＋配布物整備
7. **Phase 7**: TUIウィザード化（上記「TUIウィザード化」節の計画どおり）

## 実動画での長時間検証（未実施・次ステップ候補）

E2E（6秒合成動画）を通過した後の、実運用前の最終確認チェックリスト。
実行は開発者環境（bin/ 導入済み）で行い、結果の要約を本節に追記する。

### 検証マトリクス

- [ ] **長時間・多シーン**: 1〜2時間クラスの実動画（シーン数100以上目安）で無人完走
- [ ] **Codec 3種**: h264 / hevc / av1 の各探索が完走し、出力が再生可能
- [ ] **Audio 4モード**: copy / opus / aac / none の最終ミックスが反映される
- [ ] **Bit Depth 2種**: yuv420p10le / yuv420p の両方
- [ ] **連続稼働**: 複数ファイルを順次投入し、tmp掃除（成功時破棄・72hスイープ）とメモリレベルの安定性を確認
- [ ] **中断耐性**: Ctrl+C 中断時に子プロセス（ffmpeg等）が残らないこと
- [ ] **ログ**: --log-file 併用でTUI表示中も二重化が継続すること

### 観察ポイント

- ディスク残量: tmp配下に全試行チャンクが一時蓄積されるため、最大試行サイズ × 同時保持数を見込む
- VMAF失敗時のフォールバック: 低解像度ソースで vmaf_v0.6.1neg へ落ちる経路を目視確認
- サブジェクト境界: ハードカット以外（フェード等）で av-scenechange の検出感度が許容範囲か

### 第1回実行記録（2026-08-23 / test.webm）

- 入力: 90.1秒 / 1920x1080 / 23.976fps / VP9+Opus / 18.7MB（nb_framesメタデータ無し）
- 結果: 26シーン検出・165試行・25/26シーンが目標達成・exit 0
- 出力整合性: フレーム数 2160=2160 / yuv420p10le / Opus 2ch copy / 先頭IDR / tmp掃除済み
- **発見・修正**: 相対パス指定時、libvmaf評価の子ffmpeg（cmd.Dir変更あり）のみ入力を解決できず失敗。
  Orchestrator境界での絶対パス正規化で修正（TestOrchestratorRelativePathsIntegration恒久化）
- 観察: サイズ 18.7MB→53.8MB(+187%)。VP9ソースに対しH264/mediumがVMAF95を満たすには
  高ビットレートが必要なためで、品質優先設計としては正常。サイズ重視ユーザーへの案内は
  codec=av1選択またはtarget引き下げ。長時間動画・全codec/audioマトリクスは次回以降の実施項目

### 第2回実行記録（2026-08-23 / 4コンテナ×4コーデックマトリクス）

- 入力: test1.mp4（10秒/720p/H264+AAC）から生成した4種を testdata/ 配下で使用
  （MOV=H264+PCM / WebM=VP9+Opus / MP4=AV1+AAC / MKV=4K HEVC+FLAC。
   WebMはFLAC非対応・このffmpegのmov muxerはAV1/VP9拒否のため実機確認の上組合せ決定）
- 結果: **全4本 exit 0**。各8シーン・計190試行・31/32シーン目標達成
  （480pの1シーンはMinCRFベストエフォート採用＝仕様どおり）
- 整合性（全本）: フレーム数一致(240)/yuv420p10le/音声copy維持/**PCM→MKV無劣化格納**/先頭IDR/tmp掃除ゼロ残
- サイズ: webm +47% / mov -7% / mp4(AV1) +52% / mkv(4K) -34% —— ソース効率と
  出力codecの圧縮率差によるもので品質優先設計としては正常
- 追加修正なし。相対パス修正後、初回のフルマトリクスが通過

### 評価プロファイル導入（2026-08・第2回検証の知見を実装へ反映）

**動機**: 第2回マトリクス検証で4K出力のみサイズ-34%となった原因を追跡した結果、
1080p縮小評価のバイアスを定量化——ツール内95合格の出力を4Kネイティブ再評価すると
harmonic 87.45 / 最悪フレーム 80.6 であり、ネイティブ解像度の劣化を見逃していた。

- **評価プロファイル制**: アルゴリズム×評価解像度をセットで指定（domain.EvalProfile）。
  vmaf_v1.0.16_3d0h@1920x1080（既定）/ vmaf_4k_v0.6.1@3840x2160。`n  Name=version=モデル名（ffmpegコマンドと同一表記）。
  Nameは`<algorithm>-<resolution>`形式。EvalProfile.Algorithm フィールドにより評価器が解釈可能な
  アルゴリズムのみを受け付け（libvmaf評価器は libvmaf 以外を即エラー＝フェイルファスト）、
  将来のXPSNR/SSIM等追加はレジストリ1行＋対応評価器実装で拡張する。
  vmaf_4k_v0.6.1 がpin版ffmpegへ同梱されていることは実機確認済み
- **フォールバック廃止**: 従来の3d0h失敗時neg自動切替は「暗黙にスコア基準が変わる」リスク
  があるため廃止。評価失敗は即エラー（フェイルファスト）。決定: ユーザー確認済み
- **--out-res**: 出力リサイズ（select後にscale）。既定native=元解像度維持。
  シーン検出は常に元解像度で行うため検出精度に影響しない
- スコア比較は同一プロファイル内でのみ有効。プロファイル併用時はtarget調整をREADMEで案内
