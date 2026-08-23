package domain

import "testing"

// 合否判定は「指定指標スコア >= targetScore」。境界値（ちょうど一致）が「達成」扱い。
func TestTargetMetBoundary(t *testing.T) {
	q := QualityMetrics{HarmonicMean: 95.0}

	if !q.TargetMet(MetricHarmonic, 95.0) {
		t.Fatal("exact boundary must be MET (>= semantics)")
	}
	if q.TargetMet(MetricHarmonic, 95.01) {
		t.Fatal("target slightly above score must be MISS")
	}
	if !q.TargetMet(MetricHarmonic, 94.99) {
		t.Fatal("target slightly below score must be MET")
	}
}

// Score は指標種別に応じて正しい統計を返す。未知種別はharmonicへフォールバックする。
func TestQualityMetricsScore(t *testing.T) {
	q := QualityMetrics{HarmonicMean: 90.0, Mean: 95.5, Min: 70.2}
	cases := []struct {
		m    ScoreMetric
		want float64
	}{
		{MetricHarmonic, 90.0},
		{MetricMean, 95.5},
		{MetricMin, 70.2},
		{ScoreMetric("bogus"), 90.0}, // 防御フォールバック
		{"", 90.0},                   // ゼロ値も既定扱い
	}
	for _, tc := range cases {
		if got := q.Score(tc.m); got != tc.want {
			t.Fatalf("Score(%q) = %v, want %v", tc.m, got, tc.want)
		}
	}
}

// ParseScoreMetric は実値3種のみを受け付ける。
func TestParseScoreMetric(t *testing.T) {
	for _, s := range []string{"harmonic_mean", "mean", "min"} {
		if _, err := ParseScoreMetric(s); err != nil {
			t.Fatalf("%q should parse: %v", s, err)
		}
	}
	if _, err := ParseScoreMetric("average"); err == nil {
		t.Fatal("unknown metric must be rejected")
	}
}

// SearchConfig.Metric の検証: 未指定（""）は許容、未知値は拒否。
func TestSearchConfigValidateMetric(t *testing.T) {
	base := SearchConfig{Codec: CodecH264, MinCRF: 15, MaxCRF: 36, TargetScore: 95.0, Preset: "medium"}

	unset := base // Metric未指定
	if err := unset.Validate(); err != nil {
		t.Fatalf("unset metric should be accepted: %v", err)
	}
	for _, m := range []ScoreMetric{MetricHarmonic, MetricMean, MetricMin} {
		c := base
		c.Metric = m
		if err := c.Validate(); err != nil {
			t.Fatalf("metric %q should be accepted: %v", m, err)
		}
	}
	bad := base
	bad.Metric = ScoreMetric("average")
	if err := bad.Validate(); err == nil {
		t.Fatal("unknown metric must be rejected")
	}
	if got := unset.EffectiveMetric(); got != MetricHarmonic {
		t.Fatalf("EffectiveMetric(unset) = %q, want harmonic", got)
	}
}
