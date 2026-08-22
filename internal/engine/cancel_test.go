package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"engram-opt/internal/domain"
)

func bisectCfg() domain.SearchConfig {
	return domain.SearchConfig{Codec: domain.CodecH264, MinCRF: 15, MaxCRF: 36, TargetScore: 90, Preset: "medium"}
}

// 開始前にキャンセル済みのcontextでは即座に中断する（試行を一切行わない）。
func TestBisectScenePreCanceledContext(t *testing.T) {
	enc := &fakeEncoder{}
	ev := &fakeEvaluator{scoreAt: func(int) float64 { return 100 }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := BisectScene(ctx, enc, ev, "in.mp4",
		domain.Scene{Index: 0, StartFrame: 0, EndFrame: 9}, bisectCfg(), t.TempDir(), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if res != nil {
		t.Fatal("no result should be produced")
	}
	if len(enc.calls) != 0 {
		t.Fatalf("no encode trial should run, got %d", len(enc.calls))
	}
}

// countingCancelEvaluator は2回目のEvaluate完了時にctxをキャンセルする。
// 二分探索ループの冒頭チェックで中断されることを検証するための仕掛け。
type countingCancelEvaluator struct {
	fakeEvaluator
	cancel context.CancelFunc
	calls  int
}

func (f *countingCancelEvaluator) Evaluate(_ context.Context, orig string, sc domain.Scene, chunk string, _ string, _ domain.EvalProfile) (domain.QualityMetrics, error) {
	f.calls++
	if f.calls == 2 {
		f.cancel()
	}
	return f.fakeEvaluator.Evaluate(context.Background(), orig, sc, chunk, "", domain.EvalProfile{})
}

// 試行と試行の間でキャンセルされた場合、次の試行に入らずに中断する。
func TestBisectSceneMidLoopCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// crf<30 で達成 / 30以上で未達: 上限36は未達のため探索ループへ進む
	enc := &fakeEncoder{}
	ev := &countingCancelEvaluator{
		fakeEvaluator: fakeEvaluator{scoreAt: func(crf int) float64 {
			if crf < 30 {
				return 100
			}
			return 50
		}},
		cancel: cancel,
	}

	_, err := BisectScene(ctx, enc, ev, "in.mp4",
		domain.Scene{Index: 0, StartFrame: 0, EndFrame: 9}, bisectCfg(), t.TempDir(), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// 上限CRF＋下限CRFの2試行のみで中断していること
	if ev.calls != 2 {
		t.Fatalf("evaluate calls = %d, want 2", ev.calls)
	}
}

// Orchestrator も開始前キャンセルを即座に反映する。
func TestOrchestratorPreCanceledContext(t *testing.T) {
	orch := &Orchestrator{
		Detector:  &fakeDetector{scenes: twoScenes()},
		Encoder:   &fakeEncoder{},
		Evaluator: &fakeEvaluator{scoreAt: func(int) float64 { return 100 }},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := orch.Run(ctx, "in.mp4", "out.mkv", filepath.Join(t.TempDir(), "job"), bisectCfg(), ProgressCallbacks{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
