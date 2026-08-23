# AGENTS.md

## 現状

- **機能開発と改善フェーズはすべて完了**（2026-08-24時点）。Phase 1〜8（依存セットアップ自動化／シーン検出／CRF二分探索／VMAF評価／パイプライン統合／TUIダッシュボード＋ウィザード3ステージ〔setup→run→summary〕・起動モード自動判定・--headless／音声4モード／配布Zip化〔LICENSE・THIRD-PARTY-NOTICES全文埋め込み・決定論的Zip〕／`--version`＝Zip名同一バージョン／CI構築〔windows-latest Dev-CI Parity・race検出はCIのみ・初回pushで稼働〕／評価プロファイル制〔vmaf_v1.0.16_3d0h@1080p / vmaf_4k_v0.6.1@2160p・フォールバック廃止=フェイルファスト〕・--out-res/--enc-args／cobra mousetrap無効化によるダブルクリック→ウィザード直行・裸起動経路のdecideLaunch一本化・裸起動エラー時[Enter]待ち・フラグ値のウィザード初期値反映）まで実装完了・動作確認済み。
- 改善フェーズ実績: バグハント5ラウンド（不具合11件修正）・staticcheck全解消・ファズターゲット5個（約1150万実行違反ゼロ）・precheck層新設・registerRun分割等のリファクタ・コミット規約統一（gitmoji＋Conventional Commits・全92コミット書換済み。絵文字/type対応は `.vscode/settings.json` テンプレート）・ドキュメント整理（memo/README/AGENTS再編）。
- **残作業はmemo.md「8. 将来課題とチェックリスト」のみ**（長時間100シーン超の無人完走・Ctrl+C中断耐性・BitDepth 8bit実TUI確認・TUI中ログ二重化の実機確認）。新機能は原則追加しない。動作仕様の固定点を変えるときは必ずmemo更新が先。
- memo.md構成（2026-08-24再編）: §1現状／§2目的／§3アーキテクチャ／**§4固定仕様**／§5外部ツール実測知見／§6堅牢化ポリシー／§7実測サマリー／§8将来課題とチェックリスト／§9履歴概要。開発ログ・実行記録の詳細はgitログ参照（memoからは結論のみ残し削除済み）。
- 導入済みライブラリはすべて使用中: `cobra`（CLI）／`bubbletea`・`lipgloss`・`bubbles`（TUI `internal/ui/`）。先行導入用アンカー `tools/tools.go` は役目を終えて削除済み（`charmbracelet/log` は依存ごと除去）。
- 設計の唯一の情報源は `memo.md`。作業前に必ず読むこと。
- Windows環境での作業教訓: PowerShell経由の大型文字列置換はCRLFと衝突して破壊的になり得るため、構造変更・一括編集はEditツール使用＋git復元前提で行う。リポジトリは `core.autocrlf=false` 運用。
- 構想: Go製の動画最適化CLI（シーン分割 → エンコード → VMAF v1評価 → CRF二分探索のPer-Shot最優化）＋TUIダッシュボード。1日単位の無人動作を想定。

## テスト方針

- **配置**: 単体テストは各パッケージ内の `*_test.go`。実バイナリを使う統合テストは同パッケージの `integration_test.go`。パイプライン全走査は `test/e2e/`。
- **ガード**: 統合/E2Eは冒頭で `testutil.RequireBinaries(t, ...)` を呼ぶ（`-short` 指定時や未セットアップ環境ではスキップ理由付きでSkipされる）。
- **実行**: 高速ループは `go test -short ./internal/...`（数秒）。フル検証は `go test ./internal/... ./test/...`（統合＋E2Eで約1〜2分）。**ファジング**: `internal/domain/fuzz_test.go` のシードは通常 `go test` でも毎回実行される。深掘りは `go test ./internal/domain/ -run '^$' -fuzz FuzzParseOutRes -fuzztime 20s` 等でローカル実行する（失敗コーパスは testdata/fuzz へ落ちるのでコミットする）。
- **品質ゲート（自動強制）**: `go run ./cmd/engram-package` はビルド前に `gofmt -l` ＋ `go vet ./...` を実行し、違反があればZip化を中止する（`internal/devcheck`）。単発実行は `go run ./cmd/engram-setup check`。素の `go build` にはフック不可のため迂回可能——最終門番はこのpackage経由。**静的解析**: `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` を継続的な手動監査として随時実行する（ゲート必須にはしない。依存追加を避ける方針のため手動監査位置づけ）。
- **フィクスチャ**: テスト動画はGitにコミットしない。`testutil.GenerateSampleVideo` が lavfi で動的生成する（320x240/30fps/6秒=180フレーム、ハードカット60/120フレーム目）。
- **仕様照合**: 各アサートはmemo.md固定点に対応づけ済み（フレーム完全一致＝select区間、10-bit＝yuv420p10le、チャンク先頭IDR＝FirstFrameIsKey、選択指標>=目標、成功時tmp破棄等）。

## アーキテクチャの固定方針（memo.md 由来）

- **完全ポータブル配布**: Zip解凍だけで動作。ユーザーへのランタイム導入・PATH設定要求は禁止。
- **環境構築はGoスクリプト一発**: `go run ./cmd/engram-setup`（OS/arch自動判別、FFmpeg静的ビルドのDL、Rust製 `av-scenechange` を `cargo build --release` でローカルビルド）。ローカル開発とCIで同一コマンドを使う（Dev-CI Parity）。**開発者環境にはRustツールチェーン（cargo）が必須**。
- **依存バイナリのpin**: FFmpeg 8.1.2 full build（GyanD/codexffmpeg GitHubリリースzip・公式SHA256照合。essentialsにはlibsvtav1が無いためfullを採用）と av-scenechange v0.24.1 をバージョンpinしSHA256検証する。セットアップは冪等（導入済みなら検証のみ）で、検証にはlibvmafフィルタ＋必須エンコーダ（h264/hevc/av1-svt）の存在確認を含む。
- **VMAF実装時の注意**（実測済み）: フィルタ名は8系で `vmaf` → `libvmaf` に改名。`vmaf_v1.0.16_3d0h` はCAMBI特徴量の関係で低解像度入力だと失敗するため、libvmaf投入前に1920x1080へのリサイズが必要（詳細はmemo.md）。
- **外部バイナリの呼び出し**: 必ず `os.Executable()` 基準で同梱 `bin/{tool}` を解決する（`toolbin.DetectLayout`: 本体隣のbin/存在を自己検証し、無ければリポジトリ `build/` へフォールバック）。PATH参照やシステムのffmpeg呼び出しは書かない。tmpも同一base直下（配布: `<本体>/tmp`、開発: `<repo>/build/tmp`）。
- **`build/` はステージング領域**: 配布Zipと同一構造（本体 / `bin/` / `tmp/`）を生成する。配布は `build/` をそのまま圧縮するだけ。`build/tmp/` は実行時の一時チャンク・ログ置き場（自動生成・終了時破棄）。

## エンコード仕様の固定点（変更前に memo.md 再確認）

- 探索パラメータは**整数CRFのみ**（既定 15〜36、単調性前提の二分探索。Phase 8からウィザードで変更可、検証は `SearchConfig.Validate()` に一元）。Preset/Speed は全試行で一律固定。
- **音声はシーン分割の対象外**: 完成映像への最終ミックスで1回だけ処理する（`--audio copy` 既定=無劣化copy、opus/aacはチャンネル数から自動ビットレート、none=破棄）。実装は `domain.AudioMuxer`＋orchestratorのオプション依存 `Muxer`。
- 入力が8-bitでも出力は既定 `-pix_fmt yuv420p10le`（10-bit。バンディング防止とサイズ削減のため）。Phase 8から8-bit（yuv420p）も選択可能（encoderは `EncodeParams.BitDepth` を参照）。
- チャンク分割はシーン単位。チャンク先頭は必ずIDR（copy結合とシークの前提）。**先頭以降の適応的キーフレームはエンコーダー判断に委ねる**（scenecut抑止は廃止。配置はCRF非依存のため二分探索と両立、詳細はmemo.md §4.2「エンコード固定点」）。
- VMAF: **評価プロファイル制**（アルゴリズム×評価解像度のセット、`domain.EvalProfile`）。既定`vmaf_v1.0.16_3d0h`=3d0h@1920x1080、`vmaf_4k_v0.6.1`=4Kモデル@3840x2160（pin版ffmpegに同梱・実機確認済み）。Nameは`<algorithm>-<resolution>`形式とし、将来アルゴリズム追加時はAlgorithmフィールドを分けた新エントリで拡張。**フォールバックは廃止**（2026-08決定）——評価失敗は即エラーとするフェイルファスト方針。スコアは同一プロファイル内でのみ比較可能
- 合否判定: シーンごとに `選択指標 >= targetScore`（既定 95.0）を満たす最大CRFを採用。指標は `--metric harmonic_mean|mean|min`（既定harmonic_mean。libvmaf JSONキーと同一）で選択可。
