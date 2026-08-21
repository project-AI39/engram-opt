package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"engram-opt/internal/domain"
)

// fakeDetector は固定シーン列を返すだけの実装。
type fakeDetector struct {
	scenes []domain.Scene
}

func (f *fakeDetector) Name() string { return "fake-detector" }

func (f *fakeDetector) Detect(context.Context, string) ([]domain.Scene, error) {
	return f.scenes, nil
}

// failSceneEncoder は指定インデックスのシーンでエンコードを失敗させる。
type failSceneEncoder struct {
	fakeEncoder
	failIndex int
}

func (f *failSceneEncoder) EncodeChunk(ctx context.Context, input string, sc domain.Scene, p domain.EncodeParams, out string) error {
	if sc.Index == f.failIndex {
		return fmt.Errorf("synthetic encode failure")
	}
	return f.fakeEncoder.EncodeChunk(ctx, input, sc, p, out)
}

func twoScenes() []domain.Scene {
	return []domain.Scene{
		{Index: 0, StartFrame: 0, EndFrame: 49},
		{Index: 1, StartFrame: 50, EndFrame: 99},
	}
}

func TestOrchestratorRunSuccess(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "job")
	output := filepath.Join(t.TempDir(), "out", "final.mkv")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatal(err)
	}

	enc := &fakeEncoder{}
	orch := &Orchestrator{
		Detector:  &fakeDetector{scenes: twoScenes()},
		Encoder:   enc,
		Evaluator: &fakeEvaluator{scoreAt: func(int) float64 { return 100 }}, // 全CRFで達成→各1試行
	}
	cfg := domain.SearchConfig{Codec: domain.CodecH264, MinCRF: 15, MaxCRF: 36, TargetScore: 90, Preset: "medium"}

	var starts, dones []int
	var detectCalls int
	report, err := orch.Run(context.Background(), "in.mp4", output, workDir, cfg, ProgressCallbacks{
		OnDetectionDone: func([]domain.Scene) { detectCalls++ },
		OnSceneStart: func(i, _ int) {
			starts = append(starts, i)
			assertDirExists(t, filepath.Join(workDir, fmt.Sprintf("shot%04d", i)))
		},
		OnSceneDone: func(i, _ int, _ *Result) { dones = append(dones, i) },
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if detectCalls != 1 || len(starts) != 2 || len(dones) != 2 {
		t.Fatalf("callbacks: detect=%d starts=%v dones=%v", detectCalls, starts, dones)
	}
	if len(report.Results) != 2 || report.TotalTrials != 2 {
		t.Fatalf("report = %+v", report)
	}
	for i, r := range report.Results {
		if r.CRF != cfg.MaxCRF || !r.MetTarget || r.Trials != 1 {
			t.Fatalf("results[%d] = %+v", i, r)
		}
	}
	// 結合はシーン順のチャンクで1回
	if len(enc.concatCalls) != 1 {
		t.Fatalf("concat calls = %d, want 1", len(enc.concatCalls))
	}
	got := enc.concatCalls[0]
	// BisectScene の試行ファイル名は scene<Index>_crf<CRF>.mkv
	want0 := fmt.Sprintf("scene0000_crf%02d.mkv", cfg.MaxCRF)
	want1 := fmt.Sprintf("scene0001_crf%02d.mkv", cfg.MaxCRF)
	if !strings.HasSuffix(got[0], want0) || !strings.HasSuffix(got[1], want1) {
		t.Fatalf("chunk order wrong: %v", got)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("output missing: %v", err)
	}
	// 成功時は一時ディレクトリごと破棄される
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Fatalf("workDir should be removed on success: %v", err)
	}
}

func TestOrchestratorRunFailureKeepsWorkDir(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "job")
	output := filepath.Join(t.TempDir(), "final.mkv")

	orch := &Orchestrator{
		Detector:  &fakeDetector{scenes: twoScenes()},
		Encoder:   &failSceneEncoder{failIndex: 1},
		Evaluator: &fakeEvaluator{scoreAt: func(int) float64 { return 100 }},
	}
	cfg := domain.SearchConfig{Codec: domain.CodecH264, MinCRF: 15, MaxCRF: 36, TargetScore: 90, Preset: "medium"}

	_, err := orch.Run(context.Background(), "in.mp4", output, workDir, cfg, ProgressCallbacks{})
	if err == nil {
		t.Fatal("expected error from failing scene")
	}
	if !strings.Contains(err.Error(), "scene 2/2") {
		t.Fatalf("error should identify failing scene: %v", err)
	}
	// 失敗時は調査のため workDir が残る
	assertDirExists(t, workDir)
}

func TestOrchestratorRequiresComponents(t *testing.T) {
	orch := &Orchestrator{}
	if _, err := orch.Run(context.Background(), "in.mp4", "out.mkv", t.TempDir(),
		domain.SearchConfig{}, ProgressCallbacks{}); err == nil {
		t.Fatal("expected error when components are missing")
	}
}

func assertDirExists(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		t.Fatalf("dir %s should exist during processing (err=%v)", path, err)
	}
}
