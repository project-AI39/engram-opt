package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"engram-opt/internal/detector/avscenechange"
	"engram-opt/internal/domain"
	ffenc "engram-opt/internal/encoder/ffmpeg"
	"engram-opt/internal/engine"
	"engram-opt/internal/evaluator/libvmaf"
	"engram-opt/internal/toolbin"
)

func newOptimizeCmd() *cobra.Command {
	var (
		outPath string
		shot    int
		codec   string
		preset  string
	)

	cmd := &cobra.Command{
		Use:   "optimize <input>",
		Args:  cobra.ExactArgs(1),
		Short: "Optimize a video per-shot (Phase 3: detection + single-shot CRF search)",
		Long: `Pipeline stages implemented so far:
  [x] scene detection (av-scenechange via FFmpeg Y4M pipe)
  [x] per-shot CRF bisection (--shot N, Phase 3 preview)
  [ ] whole-video orchestration & concat (Phase 4)
  [ ] TUI dashboard (Phase 5)

With --shot N the specified scene is encoded and evaluated at several CRF
values (integer binary search) to find the largest CRF whose VMAF
harmonic_mean reaches the target score.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			scenes, err := avscenechange.New().Detect(ctx, args[0])
			if err != nil {
				return err
			}

			data, err := json.MarshalIndent(scenes, "", "  ")
			if err != nil {
				return fmt.Errorf("marshaling result: %w", err)
			}
			data = append(data, '\n')
			if outPath == "" {
				if _, err := cmd.OutOrStdout().Write(data); err != nil {
					return fmt.Errorf("writing to stdout: %w", err)
				}
			} else if err := os.WriteFile(outPath, data, 0o644); err != nil {
				return fmt.Errorf("writing result file: %w", err)
			}
			log.Printf("[optimize] scene detection done: %d scene(s)", len(scenes))

			// --shot 未指定は現状ここまで（全シーン統合は Phase 4）
			if shot < 0 {
				log.Printf("[optimize] use --shot N to run the CRF search on a single scene (full pipeline arrives in Phase 4)")
				return nil
			}
			if shot >= len(scenes) {
				return fmt.Errorf("--shot %d out of range (0..%d)", shot, len(scenes)-1)
			}
			return runShotBisect(ctx, scenes[shot], args[0], codec, preset)
		},
	}
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "write scene JSON to file instead of stdout")
	cmd.Flags().IntVar(&shot, "shot", -1, "run the CRF bisect on this scene index only (Phase 3 preview)")
	cmd.Flags().StringVar(&codec, "codec", string(domain.CodecH264), "trial encode codec: h264 | hevc | av1")
	cmd.Flags().StringVar(&preset, "preset", "medium", "encoder preset (identical across all trials)")
	return cmd
}

// runShotBisect は単一シーンに対するCRF二分探索を実行し、結果をログへ出力する。
// 試行ファイルは build/tmp/<job-id>/ 配下に隔離する（build/tmp 規約）。
func runShotBisect(ctx context.Context, scene domain.Scene, inputPath, codecName, preset string) error {
	c := domain.VideoCodec(codecName)
	switch c {
	case domain.CodecH264, domain.CodecHEVC, domain.CodecAV1:
		// OK
	default:
		return fmt.Errorf("unsupported codec %q (use h264 | hevc | av1)", codecName)
	}

	root, err := toolbin.RepoRoot()
	if err != nil {
		return err
	}
	jobID := time.Now().Format("20060102-150405")
	workDir := filepath.Join(root, "build", "tmp", jobID, fmt.Sprintf("shot%04d", scene.Index))
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("creating work dir: %w", err)
	}

	cfg := domain.SearchConfig{
		Codec:       c,
		MinCRF:      domain.DefaultMinCRF,
		MaxCRF:      domain.DefaultMaxCRF,
		TargetScore: domain.DefaultTargetScore,
		Preset:      preset,
	}

	res, err := engine.BisectScene(ctx,
		ffenc.New(), libvmaf.New(),
		inputPath, scene, cfg, workDir,
		func(tr engine.Trial) {
			status := "MISS"
			if tr.MetTarget {
				status = "HIT "
			}
			log.Printf("[optimize] shot %d trial crf=%2d harmonic_mean=%6.2f min=%6.2f [%s]",
				scene.Index, tr.CRF, tr.Metrics.HarmonicMean, tr.Metrics.Min, status)
		})
	if err != nil {
		return err
	}

	log.Printf("[optimize] shot %d RESULT: crf=%d met=%v trials=%d harmonic_mean=%.2f",
		scene.Index, res.CRF, res.MetTarget, res.Trials, res.Metrics.HarmonicMean)
	log.Printf("[optimize] best chunk kept at: %s", res.BestChunkPath)
	return nil
}
