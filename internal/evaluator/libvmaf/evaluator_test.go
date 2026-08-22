package libvmaf

import (
	"testing"

	"engram-opt/internal/domain"
)

// フィクスチャは FFmpeg 8.1.2 の libvmaf 実測出力を基にした最小構成。
const fixtureJSON = `{
  "version": "60016fbd",
  "fps": 51.89,
  "frames": [
    {"frameNum": 0, "metrics": {"vmaf": 21.981203}},
    {"frameNum": 1, "metrics": {"vmaf": 13.805536}},
    {"frameNum": 2, "metrics": {"vmaf": 16.630676}}
  ],
  "pooled_metrics": {
    "integer_motion3_mmxv_18": {"min": 1.859004, "max": 3.939203, "mean": 2.471915, "harmonic_mean": 2.430978},
    "vmaf": {"min": 8.589416, "max": 30.777355, "mean": 20.655487, "harmonic_mean": 19.690776}
  },
  "aggregate_metrics": {}
}`

func TestParseReport(t *testing.T) {
	scene := domain.Scene{Index: 0, StartFrame: 0, EndFrame: 2} // 3フレーム
	q, err := parseReport([]byte(fixtureJSON), scene)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.HarmonicMean != 19.690776 {
		t.Fatalf("harmonic_mean = %v", q.HarmonicMean)
	}
	if q.Mean != 20.655487 {
		t.Fatalf("mean = %v", q.Mean)
	}
	if q.Min != 8.589416 {
		t.Fatalf("min = %v", q.Min)
	}
	if !q.TargetMet(domain.MetricHarmonic, 19.69) || q.TargetMet(domain.MetricHarmonic, 19.70) {
		t.Fatalf("TargetMet boundary check failed: %+v", q)
	}
}

func TestParseReportErrors(t *testing.T) {
	t.Run("frame count mismatch", func(t *testing.T) {
		scene := domain.Scene{StartFrame: 0, EndFrame: 9} // 10フレーム期待だが実データは3
		if _, err := parseReport([]byte(fixtureJSON), scene); err == nil {
			t.Fatal("expected frame count mismatch error")
		}
	})
	t.Run("missing pooled vmaf", func(t *testing.T) {
		noPooled := `{"frames":[{"frameNum":0,"metrics":{"vmaf":1}}],"pooled_metrics":{}}`
		if _, err := parseReport([]byte(noPooled), domain.Scene{StartFrame: 0, EndFrame: 0}); err == nil {
			t.Fatal("expected missing pooled metric error")
		}
	})
	t.Run("broken json", func(t *testing.T) {
		if _, err := parseReport([]byte("{not json"), domain.Scene{}); err == nil {
			t.Fatal("expected json error")
		}
	})
}
