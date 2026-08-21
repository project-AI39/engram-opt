// Package e2e は実バイナリを用いたパイプライン全体の走査テスト。
// 検出 → 各シーンのCRF二分探索 → 無劣化結合 が完走し、
// 入力と同一フレーム数・10-bit固定仕様どおりの出力が生成されることを検証する。
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"engram-opt/internal/detector/avscenechange"
	"engram-opt/internal/domain"
	ffenc "engram-opt/internal/encoder/ffmpeg"
	"engram-opt/internal/engine"
	"engram-opt/internal/evaluator/libvmaf"
	"engram-opt/internal/testutil"
)

func TestPipelineEndToEnd(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe", "av-scenechange")
	ctx := context.Background()

	root := t.TempDir()
	jobDir := filepath.Join(root, "job")
	output := filepath.Join(root, "final.opt.mkv")
	video := testutil.GenerateSampleVideo(t, root)

	inInfo := testutil.ProbeStreamInfo(t, ctx, video)
	if inInfo.Frames != 180 {
		t.Fatalf("fixture spec mismatch: sample should be 180 frames, got %d", inInfo.Frames)
	}

	orch := &engine.Orchestrator{
		Detector:  avscenechange.New(),
		Encoder:   ffenc.New(),
		Evaluator: libvmaf.New(),
	}
	cfg := domain.SearchConfig{
		Codec:       domain.CodecH264,
		MinCRF:      domain.DefaultMinCRF,
		MaxCRF:      domain.DefaultMaxCRF,
		TargetScore: domain.DefaultTargetScore,
		Preset:      "medium",
	}

	report, err := orch.Run(ctx, video, output, jobDir, cfg, engine.ProgressCallbacks{})
	if err != nil {
		t.Fatalf("pipeline failed: %v", err)
	}

	// 出力検証: 無劣化結合で総フレーム数が保存され、10-bit固定仕様を満たす
	outInfo := testutil.ProbeStreamInfo(t, ctx, output)
	if outInfo.Frames != inInfo.Frames {
		t.Fatalf("output frames = %d, want %d (lossless concat must preserve all frames)",
			outInfo.Frames, inInfo.Frames)
	}
	if outInfo.PixFmt != "yuv420p10le" {
		t.Fatalf("output pix_fmt = %q, want yuv420p10le", outInfo.PixFmt)
	}

	// レポート整合: 採用CRFは探索範囲内、全シーンが1回以上の試行を持つ
	if len(report.Results) < 3 {
		t.Fatalf("hard cuts at 60/120 should yield >=3 shots, got %d", len(report.Results))
	}
	var covered int64
	for i, r := range report.Results {
		if r.CRF < cfg.MinCRF || r.CRF > cfg.MaxCRF {
			t.Fatalf("results[%d] CRF=%d outside search range [%d,%d]", i, r.CRF, cfg.MinCRF, cfg.MaxCRF)
		}
		if r.Trials < 1 {
			t.Fatalf("results[%d] trials = %d", i, r.Trials)
		}
		covered += r.Scene.FrameCount()
	}
	if covered != inInfo.Frames {
		t.Fatalf("scene coverage = %d, want %d", covered, inInfo.Frames)
	}
	if report.TotalTrials < len(report.Results) {
		t.Fatalf("totalTrials = %d, must be >= shot count %d", report.TotalTrials, len(report.Results))
	}

	// サイズ情報の取得（入力・出力とも実在ファイルであること）
	inStat, serr := os.Stat(video)
	if serr != nil || inStat.Size() <= 0 {
		t.Fatalf("input stat failed: %v", serr)
	}
	outStat, oerr := os.Stat(output)
	if oerr != nil || outStat.Size() <= 0 {
		t.Fatalf("output stat failed: %v", oerr)
	}

	// memo.md 規約: 一時領域は成功時に破棄
	if _, serr := os.Stat(jobDir); !os.IsNotExist(serr) {
		t.Fatalf("job dir %s should be removed on success", jobDir)
	}
}
