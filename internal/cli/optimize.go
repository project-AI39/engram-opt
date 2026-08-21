package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"engram-opt/internal/detector/avscenechange"
	"engram-opt/internal/domain"
	ffenc "engram-opt/internal/encoder/ffmpeg"
	"engram-opt/internal/engine"
	"engram-opt/internal/evaluator/libvmaf"
	"engram-opt/internal/toolbin"
	"engram-opt/internal/ui"
)

func newOptimizeCmd() *cobra.Command {
	var (
		output  string
		shot    int
		codec   string
		preset  string
		tui     bool
		logFile string
	)

	cmd := &cobra.Command{
		Use:   "optimize <input>",
		Args:  cobra.ExactArgs(1),
		Short: "Per-shot optimize: detect scenes, bisect CRF per shot, lossless concat",
		Long: `Full Per-Shot optimization pipeline:

  1. scene detection (av-scenechange via FFmpeg Y4M pipe)
  2. per-shot CRF bisection: find the largest CRF whose VMAF
     harmonic_mean reaches the target score
  3. lossless concatenation of the chosen chunks

Temporary trial files live under build/tmp/<job-id>/ and are removed on
success (kept on failure for debugging). Output defaults to
<input>.opt.mkv next to the source; override with --out.

Use --shot N for a debug run of a single scene's CRF search
(no concat; the winning trial chunk is kept).`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			input := args[0]

			// --log-file: 無人実行向けにログをファイルへも二重化する
			var logSink io.Writer // --tui 時は ui.Options.LogMirror に渡し、表示中も二重化を維持する
			if logFile != "" {
				f, lerr := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
				if lerr != nil {
					return fmt.Errorf("opening log file: %w", lerr)
				}
				defer f.Close()
				logSink = f
				prev := log.Writer()
				log.SetOutput(io.MultiWriter(prev, f))
				defer log.SetOutput(prev)
			}

			root, err := toolbin.RepoRoot()
			if err != nil {
				return err
			}
			jobDir := filepath.Join(root, "build", "tmp", time.Now().Format("20060102-150405"))

			cfg, err := buildSearchConfig(codec, preset)
			if err != nil {
				return err
			}

			// デバッグモード: 単一シーンの二分探索のみ（結合しない）
			if shot >= 0 {
				return runShotDebug(ctx, input, shot, cfg, jobDir)
			}

			outPath := output
			if outPath == "" {
				outPath = strings.TrimSuffix(input, filepath.Ext(input)) + ".opt.mkv"
			}
			// 成功時に jobDir を丸ごと削除するため、出力先がその配下だと消えてしまう
			if err := ensureOutside(jobDir, outPath); err != nil {
				return err
			}

			orch := &engine.Orchestrator{
				Detector:  avscenechange.New(),
				Encoder:   ffenc.New(),
				Evaluator: libvmaf.New(),
			}

			// TUIモード。端末がなければ平文ログへフォールバックする。
			var report *engine.PipelineReport
			if tui {
				rep, uerr := ui.Run(ctx, orch, input, outPath, jobDir, cfg, ui.Options{
					InputPath:  input,
					OutputPath: outPath,
					Codec:      cfg.Codec,
					Preset:     preset,
					Target:     cfg.TargetScore,
					LogMirror:  logSink,
				})
				switch {
				case uerr == nil:
					report = rep
				case errors.Is(uerr, ui.ErrNoTTY):
					log.Printf("[optimize] %v; falling back to plain logs", uerr)
				default:
					return uerr
				}
			}
			if report == nil {
				rep, perr := orch.Run(ctx, input, outPath, jobDir, cfg, engine.ProgressCallbacks{
					OnDetectionDone: func(scenes []domain.Scene) {
						log.Printf("[optimize] detected %d scene(s)", len(scenes))
					},
					OnSceneStart: func(i, total int) {
						log.Printf("[optimize] shot %d/%d start", i+1, total)
					},
					OnTrial: func(tr engine.Trial) {
						status := "MISS"
						if tr.MetTarget {
							status = "HIT "
						}
						log.Printf("[optimize] shot %d trial crf=%2d harmonic_mean=%6.2f min=%6.2f [%s]",
							tr.Scene.Index, tr.CRF, tr.Metrics.HarmonicMean, tr.Metrics.Min, status)
					},
					OnSceneDone: func(i, _ int, r *engine.Result) {
						log.Printf("[optimize] shot %d done: crf=%d met=%v trials=%d",
							i, r.CRF, r.MetTarget, r.Trials)
					},
				})
				if perr != nil {
					return perr
				}
				report = rep
			}
			printSummary(input, report)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "out", "o", "", "final output path (default: <input>.opt.mkv)")
	cmd.Flags().IntVar(&shot, "shot", -1, "debug: run CRF search on this scene index only")
	cmd.Flags().StringVar(&codec, "codec", string(domain.CodecH264), "encode codec: h264 | hevc | av1")
	cmd.Flags().StringVar(&preset, "preset", "medium", "encoder preset (identical across all trials)")
	cmd.Flags().BoolVar(&tui, "tui", false, "show interactive dashboard (falls back to plain logs when stdout is not a terminal)")
	cmd.Flags().StringVar(&logFile, "log-file", "", "append log output to this file (for unattended runs)")
	return cmd
}

// buildSearchConfig はCLIフラグ値から探索設定を構築する。
func buildSearchConfig(codecName, preset string) (domain.SearchConfig, error) {
	c := domain.VideoCodec(codecName)
	switch c {
	case domain.CodecH264, domain.CodecHEVC, domain.CodecAV1:
		// OK
	default:
		return domain.SearchConfig{}, fmt.Errorf("unsupported codec %q (use h264 | hevc | av1)", codecName)
	}
	return domain.SearchConfig{
		Codec:       c,
		MinCRF:      domain.DefaultMinCRF,
		MaxCRF:      domain.DefaultMaxCRF,
		TargetScore: domain.DefaultTargetScore,
		Preset:      preset,
	}, nil
}

// runShotDebug は指定シーン1本だけ二分探索を実行するデバッグ用パス。
// 結合は行わず、勝った試行チャンクを残す（調査・品質確認用）。
func runShotDebug(ctx context.Context, input string, idx int, cfg domain.SearchConfig, jobDir string) error {
	scenes, err := avscenechange.New().Detect(ctx, input)
	if err != nil {
		return err
	}
	log.Printf("[optimize] detected %d scene(s)", len(scenes))
	if idx >= len(scenes) {
		return fmt.Errorf("--shot %d out of range (0..%d)", idx, len(scenes)-1)
	}
	sc := scenes[idx]

	shotDir := filepath.Join(jobDir, fmt.Sprintf("shot%04d", sc.Index))
	if err := os.MkdirAll(shotDir, 0o755); err != nil {
		return fmt.Errorf("creating shot dir: %w", err)
	}

	res, err := engine.BisectScene(ctx, ffenc.New(), libvmaf.New(), input, sc, cfg, shotDir,
		func(tr engine.Trial) {
			status := "MISS"
			if tr.MetTarget {
				status = "HIT "
			}
			log.Printf("[optimize] shot %d trial crf=%2d harmonic_mean=%6.2f min=%6.2f [%s]",
				sc.Index, tr.CRF, tr.Metrics.HarmonicMean, tr.Metrics.Min, status)
		})
	if err != nil {
		return err
	}
	log.Printf("[optimize] shot %d RESULT: crf=%d met=%v trials=%d harmonic_mean=%.2f",
		sc.Index, res.CRF, res.MetTarget, res.Trials, res.Metrics.HarmonicMean)
	log.Printf("[optimize] best chunk kept at: %s", res.BestChunkPath)
	return nil
}

// ensureOutside は output が jobDir 配下でないことを確認する。
// 成功時には jobDir を丸ごと削除するため、配下にあると成果物も消えてしまう。
func ensureOutside(jobDir, output string) error {
	absJob, err := filepath.Abs(jobDir)
	if err != nil {
		return err
	}
	absOut, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absJob, absOut)
	if err == nil && !strings.HasPrefix(rel, "..") {
		return fmt.Errorf("output path %q must be outside the temp dir %q", output, jobDir)
	}
	return nil
}

// printSummary は完了サマリ（達成率・サイズ削減・出力先）を出す。
func printSummary(input string, r *engine.PipelineReport) {
	met := 0
	for _, res := range r.Results {
		if res.MetTarget {
			met++
		}
	}
	log.Printf("[optimize] %d/%d shot(s) met target score", met, len(r.Results))

	var inSize, outSize int64
	if st, err := os.Stat(input); err == nil {
		inSize = st.Size()
	}
	if st, err := os.Stat(r.OutputPath); err == nil {
		outSize = st.Size()
	}
	if inSize > 0 && outSize > 0 {
		log.Printf("[optimize] size: %.2f MB -> %.2f MB (-%.1f%%)",
			float64(inSize)/(1<<20), float64(outSize)/(1<<20),
			100*(1-float64(outSize)/float64(inSize)))
	}
	log.Printf("[optimize] output: %s (total trials=%d)", r.OutputPath, r.TotalTrials)
}
