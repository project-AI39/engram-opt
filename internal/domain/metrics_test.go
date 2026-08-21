package domain

import "testing"

// memo.md 固定仕様: 合否判定は「harmonic_mean >= targetScore」のみ。
// 境界値（ちょうど一致）が「達成」扱いであることを明示する。
func TestTargetMetBoundary(t *testing.T) {
	q := QualityMetrics{HarmonicMean: 95.0}

	if !q.TargetMet(95.0) {
		t.Fatal("exact boundary must be MET (>= semantics)")
	}
	if q.TargetMet(95.01) {
		t.Fatal("target slightly above score must be MISS")
	}
	if !q.TargetMet(94.99) {
		t.Fatal("target slightly below score must be MET")
	}
}
