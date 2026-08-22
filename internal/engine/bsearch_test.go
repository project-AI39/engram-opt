package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"engram-opt/internal/domain"
)

// fakeEncoder は EncodeChunk 呼び出しでマーカーファイルを作るだけの実装。
type fakeEncoder struct {
	calls         []domain.EncodeParams
	concatCalls   [][]string // ConcatChunks に渡されたチャンクパス列の履歴
	concatTargets []string   // ConcatChunks に渡された出力先の履歴
}

func (f *fakeEncoder) Name() string { return "fake-encoder" }

func (f *fakeEncoder) EncodeChunk(_ context.Context, _ string, _ domain.Scene, p domain.EncodeParams, outputPath string) error {
	f.calls = append(f.calls, p)
	return os.WriteFile(outputPath, []byte("chunk"), 0o644)
}

func (f *fakeEncoder) ConcatChunks(_ context.Context, chunkPaths []string, finalOutputPath string) error {
	f.concatCalls = append(f.concatCalls, chunkPaths)
	f.concatTargets = append(f.concatTargets, finalOutputPath)
	return os.WriteFile(finalOutputPath, []byte("concat"), 0o644)
}

var crfInName = regexp.MustCompile(`crf(\d+)\.mkv$`)

// fakeEvaluator はチャンクファイル名からCRFを読み取り、scoreAt(crf) をスコアとして返す。
type fakeEvaluator struct {
	scoreAt func(crf int) float64
}

func (f *fakeEvaluator) Name() string { return "fake-evaluator" }

func (f *fakeEvaluator) Evaluate(_ context.Context, _ string, scene domain.Scene, encodedChunkPath string, _ string, _ domain.EvalProfile) (domain.QualityMetrics, error) {
	m := crfInName.FindStringSubmatch(encodedChunkPath)
	if m == nil {
		return domain.QualityMetrics{}, fmt.Errorf("cannot parse crf from %q", encodedChunkPath)
	}
	crf, err := strconv.Atoi(m[1])
	if err != nil {
		return domain.QualityMetrics{}, err
	}
	score := f.scoreAt(crf)
	// フレーム数一致チェック（実実装と同じ契約）をここでは省略し、代表値のみ返す
	_ = scene
	return domain.QualityMetrics{HarmonicMean: score, Mean: score, Min: score - 5}, nil
}

func newBisectCfg(scoreAt func(int) float64, t *testing.T) (domain.VideoEncoder, domain.QualityEvaluator, domain.SearchConfig) {
	t.Helper()
	return &fakeEncoder{}, &fakeEvaluator{scoreAt: scoreAt},
		domain.SearchConfig{
			Codec:       domain.CodecH264,
			MinCRF:      15,
			MaxCRF:      36,
			TargetScore: 90.0,
			Preset:      "medium",
		}
}

// TestBisectSceneBoundary は境界が範囲内にあるケース。
// score(crf)=110-crf, target=90 → crf<=20 が達成。答えは20になるはず。
func TestBisectSceneBoundary(t *testing.T) {
	enc, ev, cfg := newBisectCfg(func(crf int) float64 { return float64(110 - crf) }, t)
	workDir := t.TempDir()

	res, err := BisectScene(context.Background(), enc, ev, "input.mp4",
		domain.Scene{Index: 0, StartFrame: 0, EndFrame: 99}, cfg, workDir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.CRF != 20 || !res.MetTarget {
		t.Fatalf("result = %+v, want CRF=20 MetTarget=true", res)
	}
	if got, want := res.Metrics.HarmonicMean, 90.0; got != want {
		t.Fatalf("harmonic_mean = %v, want %v", got, want)
	}
	// 試行順: 36(miss) 15(hit) 25(miss) 20(hit) 22(miss) 21(miss) → 境界確定で6試行
	if res.Trials != 6 {
		t.Fatalf("trials = %d, want 6", res.Trials)
	}
	assertOnlyBestFileRemains(t, workDir, res.BestChunkPath)
}

// TestBisectSceneAlwaysMet は上限CRFでも達成するケース。1試行で終わるはず。
func TestBisectSceneAlwaysMet(t *testing.T) {
	enc, ev, cfg := newBisectCfg(func(int) float64 { return 100.0 }, t)
	workDir := t.TempDir()

	res, err := BisectScene(context.Background(), enc, ev, "input.mp4",
		domain.Scene{Index: 3, StartFrame: 300, EndFrame: 399}, cfg, workDir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.CRF != cfg.MaxCRF || !res.MetTarget {
		t.Fatalf("result = %+v, want CRF=%d MetTarget=true", res, cfg.MaxCRF)
	}
	if res.Trials != 1 {
		t.Fatalf("trials = %d, want 1", res.Trials)
	}
	assertOnlyBestFileRemains(t, workDir, res.BestChunkPath)
}

// TestBisectSinglePointRangeEncodesOnce はMin==Maxの縮退レンジで
// 同一CRFの二重エンコードを行わず1試行で打ち切ることを検証する。
func TestBisectSinglePointRangeEncodesOnce(t *testing.T) {
	enc, ev, _ := newBisectCfg(func(int) float64 { return 50.0 }, t)
	fe := enc.(*fakeEncoder)
	cfg := domain.SearchConfig{
		Codec: domain.CodecH264, Preset: "medium",
		MinCRF: 20, MaxCRF: 20, TargetScore: 90.0,
	}
	workDir := t.TempDir()

	res, err := BisectScene(context.Background(), enc, ev, "input.mp4",
		domain.Scene{Index: 2, StartFrame: 200, EndFrame: 299}, cfg, workDir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fe.calls) != 1 {
		t.Fatalf("encode calls = %d, want 1 (no re-encode of same CRF)", len(fe.calls))
	}
	if res.CRF != 20 || res.MetTarget || res.Trials != 1 {
		t.Fatalf("result = %+v, want CRF=20 MetTarget=false Trials=1", res)
	}
	assertOnlyBestFileRemains(t, workDir, res.BestChunkPath)
}

// spreadEvaluator は3指標に幅を持たせたスコアを返す（指標選択の検証用）。
// score(crf)=110-crf に対し mean=+2 / min=-8 のオフセットを付ける。
type spreadEvaluator struct{}

func (spreadEvaluator) Name() string { return "spread" }

func (spreadEvaluator) Evaluate(_ context.Context, _ string, _ domain.Scene, encodedChunkPath string, _ string, _ domain.EvalProfile) (domain.QualityMetrics, error) {
	m := crfInName.FindStringSubmatch(encodedChunkPath)
	if m == nil {
		return domain.QualityMetrics{}, fmt.Errorf("cannot parse crf from %q", encodedChunkPath)
	}
	crf, err := strconv.Atoi(m[1])
	if err != nil {
		return domain.QualityMetrics{}, err
	}
	score := float64(110 - crf)
	return domain.QualityMetrics{HarmonicMean: score, Mean: score + 2, Min: score - 8}, nil
}

// TestBisectMetricSelection は同一スコア分布でも合否指標ごとに採用CRFが変わることを検証する。
// target=90 / score(crf)=110-crf のとき:
//
//	harmonic >= 90 → crf <= 20
//	mean     >= 90 → crf <= 22
//	min      >= 90 → 到達不能（MinCRFベストエフォート・未達）
func TestBisectMetricSelection(t *testing.T) {
	scene := domain.Scene{Index: 0, StartFrame: 0, EndFrame: 99}
	base := domain.SearchConfig{
		Codec: domain.CodecH264, MinCRF: 15, MaxCRF: 36,
		TargetScore: 90.0, Preset: "medium",
	}
	enc := &fakeEncoder{}
	workDir := t.TempDir()

	for _, tc := range []struct {
		metric  domain.ScoreMetric
		wantCRF int
	}{
		{domain.MetricHarmonic, 20},
		{domain.MetricMean, 22},
	} {
		t.Run(string(tc.metric), func(t *testing.T) {
			cfg := base
			cfg.Metric = tc.metric
			res, err := BisectScene(context.Background(), enc, spreadEvaluator{}, "in.mp4", scene, cfg, workDir, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.CRF != tc.wantCRF || !res.MetTarget {
				t.Fatalf("%s: result = %+v, want CRF=%d met=true", tc.metric, res, tc.wantCRF)
			}
		})
	}

	t.Run("min unreachable adopts min crf", func(t *testing.T) {
		cfg := base
		cfg.Metric = domain.MetricMin
		res, err := BisectScene(context.Background(), enc, spreadEvaluator{}, "in.mp4", scene, cfg, workDir, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.CRF != cfg.MinCRF || res.MetTarget {
			t.Fatalf("min metric: result = %+v, want best-effort MinCRF unmet", res)
		}
	})
}

// TestBisectSceneNeverMet は下限CRFでも未達のケース。MinCRF採用・未達フラグ。
func TestBisectSceneNeverMet(t *testing.T) {
	enc, ev, cfg := newBisectCfg(func(int) float64 { return 50.0 }, t)
	workDir := t.TempDir()

	var observed []Trial
	res, err := BisectScene(context.Background(), enc, ev, "input.mp4",
		domain.Scene{Index: 1, StartFrame: 100, EndFrame: 199}, cfg, workDir,
		func(tr Trial) { observed = append(observed, tr) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.CRF != cfg.MinCRF || res.MetTarget {
		t.Fatalf("result = %+v, want CRF=%d MetTarget=false", res, cfg.MinCRF)
	}
	if len(observed) != res.Trials {
		t.Fatalf("observer saw %d trials, result says %d", len(observed), res.Trials)
	}
	assertOnlyBestFileRemains(t, workDir, res.BestChunkPath)
}

func TestBisectSceneInvalidRange(t *testing.T) {
	enc, ev, _ := newBisectCfg(func(int) float64 { return 100 }, t)
	cfg := domain.SearchConfig{MinCRF: 30, MaxCRF: 20}
	if _, err := BisectScene(context.Background(), enc, ev, "in.mp4",
		domain.Scene{}, cfg, t.TempDir(), nil); err == nil {
		t.Fatal("expected error for inverted CRF range")
	}
}

func assertOnlyBestFileRemains(t *testing.T, dir, bestPath string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Base(bestPath)
	for _, e := range entries {
		if e.Name() != want {
			t.Fatalf("stale trial file remains: %s (want only %s)", e.Name(), want)
		}
	}
	if len(entries) == 0 {
		t.Fatalf("best chunk missing: %s", want)
	}
}

// --out-res で指定した出力解像度は全試行のEncodeParamsへ伝播する。
// （実機検証で「scaleが効かずソース解像度のまま出力される」回帰を捕捉したテスト）
func TestBisectSceneAppliesOutRes(t *testing.T) {
	enc := &fakeEncoder{}
	ev := &fakeEvaluator{scoreAt: func(int) float64 { return 100 }}
	cfg := domain.SearchConfig{
		Codec: domain.CodecH264, MinCRF: 15, MaxCRF: 36, TargetScore: 90,
		Preset: "medium", OutWidth: 1280, OutHeight: 720,
	}
	_, err := BisectScene(context.Background(), enc, ev, "in.mp4",
		domain.Scene{Index: 0, StartFrame: 0, EndFrame: 9}, cfg, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(enc.calls) == 0 {
		t.Fatal("no encode calls recorded")
	}
	for i, p := range enc.calls {
		if p.OutWidth != 1280 || p.OutHeight != 720 {
			t.Fatalf("call[%d] out res = %dx%d, want 1280x720", i, p.OutWidth, p.OutHeight)
		}
	}
}
