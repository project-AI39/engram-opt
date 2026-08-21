# AGENTS.md

## 現状

- 依存関係の自動セットアップ、cobra CLI骨格、Phase 2〜4（domain／detector／encoder／evaluator／engine.bsearch／orchestrator統合）、Phase 5（bubbletea TUIダッシュボード＋ログ方針確定）まで **全フェーズ実装完了・動作確認済み**。コア機能は完成。次ステップ候補は配布物整備（README/LICENSE/THIRD-PARTY-NOTICES、ビルドスクリプト、CI）と実動画での長時間検証。開発フェーズは memo.md「モジュール構成（実装方針）」、実測知見は memo.md「依存ツール」節を参照。
- 導入済みライブラリはすべて使用中: `cobra`（CLI）／`bubbletea`・`lipgloss`・`bubbles`（TUIダッシュボード `internal/ui/`）。先行導入用アンカー `tools/tools.go` は役目を終えて削除済み（`charmbracelet/log` は依存ごと除去）。
- 設計の唯一の情報源は `memo.md`。作業前に必ず読むこと。
- 構想: Go製の動画最適化CLI（シーン分割 → エンコード → VMAF v1評価 → CRF二分探索のPer-Shot最適化）＋TUIダッシュボード。1日単位の無人動作を想定。

## アーキテクチャの固定方針（memo.md 由来）

- **完全ポータブル配布**: Zip解凍だけで動作。ユーザーへのランタイム導入・PATH設定要求は禁止。
- **環境構築はGoスクリプト一発**: `go run ./cmd/engram setup`（OS/arch自動判別、FFmpeg静的ビルドのDL、Rust製 `av-scenechange` を `cargo build --release` でローカルビルド）。ローカル開発とCIで同一コマンドを使う（Dev-CI Parity）。**開発者環境にはRustツールチェーン（cargo）が必須**。
- **依存バイナリのpin**: FFmpeg 8.1.2（gyan.dev essentials zip）と av-scenechange v0.24.1 をバージョンpinしSHA256検証する。セットアップは冪等（導入済みなら検証のみ）。
- **VMAF実装時の注意**（実測済み）: フィルタ名は8系で `vmaf` → `libvmaf` に改名。`vmaf_v1.0.16_3d0h` はCAMBI特徴量の関係で低解像度入力だと失敗するため、libvmaf投入前に1920x1080へのリサイズが必要（詳細はmemo.md）。
- **外部バイナリの呼び出し**: 必ず `os.Executable()` からの相対パス（`../bin/{tool}`）。PATH参照やシステムのffmpeg呼び出しは書かない。
- **`build/` はステージング領域**: 配布Zipと同一構造（本体 / `bin/` / `tmp/`）を生成する。配布は `build/` をそのまま圧縮するだけ。`build/tmp/` は実行時の一時チャンク・ログ置き場（自動生成・終了時破棄）。

## エンコード仕様の固定点（変更前に memo.md 再確認）

- 探索パラメータは**整数CRFのみ**（範囲 15〜36、単調性前提の二分探索）。Preset/Speed は全試行で一律固定。
- 入力が8-bitでも出力は `-pix_fmt yuv420p10le`（10-bit固定。バンディング防止とサイズ削減のため）。
- チャンク分割はシーン単位。IDRキーフレームは各チャンク先頭のみ。
- VMAF: FFmpeg内蔵 `vmaf_v1.0.16_3d0h.json` を使用（外部モデルファイル管理不要）。フォールバックは `vmaf_v0.6.1neg`。
- 合否判定: シーンごとの `harmonic_mean >= targetScore`（既定 95.0）を満たす最大CRFを採用。`mean` や `min` は使わない。
