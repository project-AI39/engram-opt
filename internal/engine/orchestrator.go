package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"engram-opt/internal/domain"
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
	TotalTrials int // 全シーン合計の試行回数
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

	// 音声付与の要否。必要な場合は結合結果をjobDir内の中間ファイルへ出し、
	// 最終ミックス（MuxAudio）で outputPath へ書き出す。音声が不要なら
	// 従来どおり結合が直接 outputPath を生成する。
	useAudio := o.Audio != "" && o.Audio != domain.AudioNone
	concatTarget := outputPath
	if useAudio {
		concatTarget = filepath.Join(workDir, "concat_video.mkv")
	}
	if err := o.Encoder.ConcatChunks(ctx, chunks, concatTarget); err != nil {
		return nil, fmt.Errorf("concat: %w", err)
	}
	if useAudio {
		log.Printf("[pipeline] muxing audio (mode=%s)", o.Audio)
		if err := o.Muxer.MuxAudio(ctx, concatTarget, inputPath, o.Audio, outputPath); err != nil {
			return nil, fmt.Errorf("audio mux (%s): %w", o.Audio, err)
		}
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
	}, nil
}
