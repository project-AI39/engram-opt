# AGENTS.md

## 現状

- 依存関係の自動セットアップ、cobra CLI骨格、Phase 2〜4（domain／detector／encoder／evaluator／engine.bsearch／orchestrator統合）、Phase 5（bubbletea TUIダッシュボード＋ログ方針確定）、Phase 6（音声3モード＋README整備・配布レイアウト統一）、Phase 7（TUIウィザード化: setup→run→summaryの3ステージ・起動モード自動判定・--headless）、配布物整備（LICENSE・THIRD-PARTY-NOTICES自動生成・`go run ./cmd/engram-package` によるZip化・解凍先からの実行検証済み）まで **実装完了・動作確認済み**。次ステップ候補はCI（GitHub ActionsでのDev-CI Parity）、実動画での長時間検証、NOTICESへのライセンス全文埋め込み。開発フェーズは memo.md「モジュール構成（実装方針）」、実測知見は memo.md「依存ツール」節を参照。
- 導入済みライブラリはすべて使用中: `cobra`（CLI）／`bubbletea`・`lipgloss`・`bubbles`（TUIダッシュボード `internal/ui/`）。先行導入用アンカー `tools/tools.go` は役目を終えて削除済み（`charmbracelet/log` は依存ごと除去）。
- 設計の唯一の情報源は `memo.md`。作業前に必ず読むこと。
- 構想: Go製の動画最適化CLI（シーン分割 → エンコード → VMAF v1評価 → CRF二分探索のPer-Shot最適化）＋TUIダッシュボード。1日単位の無人動作を想定。

## テスト方針

- **配置**: 単体テストは各パッケージ内の `*_test.go`。実バイナリを使う統合テストは同パッケージの `integration_test.go`。パイプライン全走査は `test/e2e/`。
- **ガード**: 統合/E2Eは冒頭で `testutil.RequireBinaries(t, ...)` を呼ぶ（`-short` 指定時や未セットアップ環境ではスキップ理由付きでSkipされる）。
- **実行**: 高速ループは `go test -short ./internal/...`（数秒）。フル検証は `go test ./internal/... ./test/...`（統合＋E2Eで約1〜2分）。
- **フィクスチャ**: テスト動画はGitにコミットしない。`testutil.GenerateSampleVideo` が lavfi で動的生成する（320x240/30fps/6秒=180フレーム、ハードカット60/120フレーム目）。
- **仕様照合**: 各アサートはmemo.md固定点に対応づけ済み（フレーム完全一致＝select区間、10-bit＝yuv420p10le、IDR先頭のみ＝キーフレーム数1、harmonic_mean>=目標、成功時tmp破棄等）。

## アーキテクチャの固定方針（memo.md 由来）

- **完全ポータブル配布**: Zip解凍だけで動作。ユーザーへのランタイム導入・PATH設定要求は禁止。
- **環境構築はGoスクリプト一発**: `go run ./cmd/engram setup`（OS/arch自動判別、FFmpeg静的ビルドのDL、Rust製 `av-scenechange` を `cargo build --release` でローカルビルド）。ローカル開発とCIで同一コマンドを使う（Dev-CI Parity）。**開発者環境にはRustツールチェーン（cargo）が必須**。
- **依存バイナリのpin**: FFmpeg 8.1.2 full build（GyanD/codexffmpeg GitHubリリースzip・公式SHA256照合。essentialsにはlibsvtav1が無いためfullを採用）と av-scenechange v0.24.1 をバージョンpinしSHA256検証する。セットアップは冪等（導入済みなら検証のみ）で、検証にはlibvmafフィルタ＋必須エンコーダ（h264/hevc/av1-svt）の存在確認を含む。
- **VMAF実装時の注意**（実測済み）: フィルタ名は8系で `vmaf` → `libvmaf` に改名。`vmaf_v1.0.16_3d0h` はCAMBI特徴量の関係で低解像度入力だと失敗するため、libvmaf投入前に1920x1080へのリサイズが必要（詳細はmemo.md）。
- **外部バイナリの呼び出し**: 必ず `os.Executable()` 基準で同梱 `bin/{tool}` を解決する（`toolbin.DetectLayout`: 本体隣のbin/存在を自己検証し、無ければリポジトリ `build/` へフォールバック）。PATH参照やシステムのffmpeg呼び出しは書かない。tmpも同一base直下（配布: `<本体>/tmp`、開発: `<repo>/build/tmp`）。
- **`build/` はステージング領域**: 配布Zipと同一構造（本体 / `bin/` / `tmp/`）を生成する。配布は `build/` をそのまま圧縮するだけ。`build/tmp/` は実行時の一時チャンク・ログ置き場（自動生成・終了時破棄）。

## エンコード仕様の固定点（変更前に memo.md 再確認）

- 探索パラメータは**整数CRFのみ**（既定 15〜36、単調性前提の二分探索。Phase 8からウィザードで変更可、検証は `SearchConfig.Validate()` に一元）。Preset/Speed は全試行で一律固定。
- **音声はシーン分割の対象外**: 完成映像への最終ミックスで1回だけ処理する（`--audio copy` 既定=無劣化copy、opus/aacはチャンネル数から自動ビットレート、none=破棄）。実装は `domain.AudioMuxer`＋orchestratorのオプション依存 `Muxer`。
- 入力が8-bitでも出力は既定 `-pix_fmt yuv420p10le`（10-bit。バンディング防止とサイズ削減のため）。Phase 8から8-bit（yuv420p）も選択可能（encoderは `EncodeParams.BitDepth` を参照）。
- チャンク分割はシーン単位。IDRキーフレームは各チャンク先頭のみ。
- VMAF: FFmpeg内蔵 `vmaf_v1.0.16_3d0h.json` を使用（外部モデルファイル管理不要）。フォールバックは `vmaf_v0.6.1neg`。
- 合否判定: シーンごとの `harmonic_mean >= targetScore`（既定 95.0）を満たす最大CRFを採用。`mean` や `min` は使わない。
