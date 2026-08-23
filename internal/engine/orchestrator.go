package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"engram-opt/internal/domain"
	"engram-opt/internal/toolbin"
)

// Orchestrator はPer-Shot最適化パイプライン全体の司令塔。
// 検出 → 各シーンのCRF二分探索（逐次・並列化は意図的にスコープ外） → 無劣化結合
// の順でフローを制御する。engineはUIに依存せず、進捗はコールバックで通知する。
type Orchestrator struct {
	Detector  domain.SceneDetector
	Encoder   domain.VideoEncoder
	Evaluator domain.QualityEvaluator

	// Muxer / Audio は最終ミックスでの音声付与（memo.md「音声処理」）。
	// Audio が none または空、あるいは Muxer が nil の場合は音声なしで出力する
	// （既存の呼び出し側・テストとの後方互換）。
	Muxer domain.AudioMuxer
	Audio domain.AudioMode
}

// ProgressCallbacks は進捗通知のためのコールバック群。すべてnil許容。
type ProgressCallbacks struct {
	// OnDetectionDone 検出完了時に一度だけ呼ばれる。
	OnDetectionDone func(scenes []domain.Scene)
	// OnSceneStart index番目（0-indexed）のシーン処理開始時。totalは総シーン数。
	OnSceneStart func(index, total int)
	// OnTrial シーン内の各CRF試行完了時に呼ばれる。
	OnTrial func(trial Trial)
	// OnSceneDone index番目のシーン確定時に呼ばれる。
	OnSceneDone func(index, total int, result *Result)
}

// PipelineReport パイプライン実行結果のサマリ。
type PipelineReport struct {
	Scenes      []domain.Scene
	Results     []*Result // シーン順
	OutputPath  string
	TotalTrials int                // 全シーン合計の試行回数
	Metric      domain.ScoreMetric // 合否判定に使われた基準指標（表示用）
}

// Run パイプラインを実行し、完成動画を outputPath へ出力する。
//
// 一時ファイルは workDir（想定: build/tmp/<job-id>/）配下に隔離され、
// 成功時には workDir ごと破棄する。失敗時は調査用に残す（ログにパスを出す）。
func (o *Orchestrator) Run(ctx context.Context, inputPath, outputPath, workDir string, cfg domain.SearchConfig, cb ProgressCallbacks) (*PipelineReport, error) {
	report, err := o.run(ctx, inputPath, outputPath, workDir, cfg, cb)
	if err != nil {
		log.Printf("[pipeline] failed; temp kept for inspection: %s", workDir)
		return nil, err
	}
	// 成功時のみ一時領域を掃除（memo.md「build/tmp は終了時破棄」規約）
	if err := os.RemoveAll(workDir); err != nil {
		log.Printf("[pipeline] warning: removing temp dir failed: %v", err)
	} else {
		log.Printf("[pipeline] temp cleaned: %s", workDir)
	}
	return report, nil
}

func (o *Orchestrator) run(ctx context.Context, inputPath, outputPath, workDir string, cfg domain.SearchConfig, cb ProgressCallbacks) (*PipelineReport, error) {
	if o.Detector == nil || o.Encoder == nil || o.Evaluator == nil {
		return nil, fmt.Errorf("orchestrator requires detector, encoder and evaluator")

	}
	// 相対パスの正規化: 子プロセスの中には実行ディレクトリを変えるものがある
	// （libvmaf評価は cmd.Dir=評価作業領域で起動し log_path を相対指定する）。
	// その場合でも入出力が壊れないよう、engine境界で絶対パスへ統一する。
	if abs, err := filepath.Abs(inputPath); err == nil {
		inputPath = abs
	}
	if abs, err := filepath.Abs(outputPath); err == nil {
		outputPath = abs
	}
	// 元動画保護の唯一の防壁: 出力が入力と同一パスなら fail-fast する。
	// 結合は concat demuxer（list.txt）経由のため ffmpeg 自己保護が発火せず、
	// 検証なしでは -c copy が元動画を exit 0 で上書きする。
	if err := RequireDistinctPaths(inputPath, outputPath); err != nil {
		return nil, err
	}
	// 音声設定の早期検証（fail-fast）。実行終盤のmuxで初めて気づくのを防ぐ。
	switch o.Audio {
	case "", domain.AudioNone:
		// 音声なし（従来動作・後方互換）
	case domain.AudioCopy, domain.AudioOpus, domain.AudioAAC:
		if o.Muxer == nil {
			return nil, fmt.Errorf("audio mode %q requires an AudioMuxer on the orchestrator", o.Audio)
		}
	default:
		return nil, fmt.Errorf("invalid audio mode %q", o.Audio)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 1) シーン検出
	scenes, err := o.Detector.Detect(ctx, inputPath)
	if err != nil {
		return nil, fmt.Errorf("scene detection: %w", err)
	}
	if len(scenes) == 0 {
		return nil, fmt.Errorf("no scenes detected in %s", inputPath)
	}
	log.Printf("[pipeline] detected %d scene(s)", len(scenes))
	if cb.OnDetectionDone != nil {
		cb.OnDetectionDone(scenes)
	}

	// 2) シーンごとの二分探索（逐次）
	total := len(scenes)
	results := make([]*Result, 0, total)
	for _, sc := range scenes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		shotDir := filepath.Join(workDir, fmt.Sprintf("shot%04d", sc.Index))
		if err := os.MkdirAll(shotDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating shot dir: %w", err)
		}
		if cb.OnSceneStart != nil {
			cb.OnSceneStart(sc.Index, total)
		}
		res, err := BisectScene(ctx, o.Encoder, o.Evaluator, inputPath, sc, cfg, shotDir, cb.OnTrial)
		if err != nil {
			return nil, fmt.Errorf("scene %d/%d: %w", sc.Index+1, total, err)
		}
		results = append(results, res)
		if cb.OnSceneDone != nil {
			cb.OnSceneDone(sc.Index, total, res)
		}
	}

	// 3) 確定チャンクを無劣化結合（シーン順）
	chunks := make([]string, 0, total)
	for _, r := range results {
		chunks = append(chunks, r.BestChunkPath)
	}

	// 最終出力はユーザーパスへの直書きではなく、同一ディレクトリ内のステージングへ
	// 書き出してから確定する。mux/concat の途中死で部分破損ファイルが完成パスに
	// 静置されるのを防ぐ（無人運用での「壊れた完成品」混入防止）。
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}
	stagedPath := stagingPathFor(outputPath)
	defer func() { _ = os.Remove(stagedPath) }() // 失敗時・中断時の掃除（成功後は存在しない）

	// 音声付与の要否。必要な場合は結合結果をjobDir内の中間ファイルへ出し、
	// 最終ミックス（MuxAudio）でステージングへ書き出す。音声が不要なら
	// 結合が直接ステージングを生成する。
	useAudio := o.Audio != "" && o.Audio != domain.AudioNone
	concatTarget := stagedPath
	if useAudio {
		concatTarget = filepath.Join(workDir, "concat_video.mkv")
	}
	if err := o.Encoder.ConcatChunks(ctx, chunks, concatTarget); err != nil {
		return nil, fmt.Errorf("concat: %w", err)
	}
	if useAudio {
		log.Printf("[pipeline] muxing audio (mode=%s)", o.Audio)
		if err := o.Muxer.MuxAudio(ctx, concatTarget, inputPath, o.Audio, stagedPath); err != nil {
			return nil, fmt.Errorf("audio mux (%s): %w", o.Audio, err)
		}
	}

	// 原子的確定: ステージング完成後にユーザー指定パスへ置換する。
	// 同一ディレクトリ内renameのためボリューム跨ぎは発生せず、Windowsでも
	// 既存ファイルへの上書きが可能（os.Rename は MoveFileEx(REPLACE_EXISTING)）。
	// 上書きは設計どおりだが無通知だと「前回の出力消失」と誤解されるため、
	// 既存ファイルがある場合のみ明示的にログへ残す（TUI表示中もログ欄へ流れる）。
	if _, err := os.Stat(outputPath); err == nil {
		log.Printf("[pipeline] overwriting existing output: %s", outputPath)
	}
	if err := renameWithRetry(ctx, stagedPath, outputPath); err != nil {
		return nil, err
	}

	sumTrials := 0
	for _, r := range results {
		sumTrials += r.Trials
	}
	return &PipelineReport{
		Scenes:      scenes,
		Results:     results,
		OutputPath:  outputPath,
		TotalTrials: sumTrials,
		Metric:      cfg.EffectiveMetric(),
	}, nil
}

// stagingPathFor は出力と同ディレクトリに作るステージング用パスを返す。
// 先頭ドットで隠しファイル扱いとし、PID接尾辞で衝突を避ける。
// 末尾は必ず元と同じ拡張子にする——ffmpegは出力フォーマットを拡張子から
// 推定するため、拡張子を持たない名前（.final.mkv.part-N 等）では
// 「Invalid argument」(-22) で起動に失敗する（実測）。
func stagingPathFor(output string) string {
	dir := filepath.Dir(output)
	ext := filepath.Ext(output)
	base := strings.TrimSuffix(filepath.Base(output), ext)
	return filepath.Join(dir, fmt.Sprintf(".%s.part-%d%s", base, os.Getpid(), ext))
}

// renameWithRetry はステージング確定の置換を短期リトライする。
// WindowsではAVスキャナや検索インデクサが完成直後のファイルを一瞬開き、
// MoveFileEx(REPLACE_EXISTING)が共有違反で失敗することがある（一過性・再試行可）。
// 数時間かけた探索の成果を確定1発で失わないための防御。
func renameWithRetry(ctx context.Context, oldPath, newPath string) error {
	err := retryWithBackoff(ctx, 4, func() error {
		return os.Rename(oldPath, newPath)
	})
	if err != nil {
		return fmt.Errorf("finalizing output: %w", err)
	}
	return nil
}

// retryWithBackoff は op を最大 attempts 回まで指数バックオフ付きで再試行する
// （待機: 250ms, 500ms, 1s）。opが成功したら即時return。ctxは待機中も中断できる。
func retryWithBackoff(ctx context.Context, attempts int, op func() error) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i > 0 {
			delay := time.Duration(250*(1<<(i-1))) * time.Millisecond
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		lastErr = op()
		if lastErr == nil {
			return nil
		}
		log.Printf("[pipeline] attempt %d/%d failed (%v); retrying", i+1, attempts, lastErr)
	}
	return lastErr
}

// RequireDistinctPaths は入力と出力が同一パスでないことを検証する。
// Windowsではパス大小文字を同一視する。エクスポートは呼び出し側（CLIウィザード等）
// がパイプライン起動前に早期検証できるようにするため。
func RequireDistinctPaths(input, output string) error {
	// 大小文字の同一視（Windows）はtoolbin共通実装に一元化
	if toolbin.SameAbsPath(input, output) {
		return fmt.Errorf("output path must differ from input path: %s", output)
	}
	return nil
}
