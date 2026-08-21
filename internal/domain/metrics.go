package domain

// QualityMetrics 評価アルゴリズムが出力する知覚画質スコア。
type QualityMetrics struct {
	HarmonicMean float64 `json:"harmonic_mean"` // 判定用代表値（調和平均）
	Mean         float64 `json:"mean"`          // 算術平均（参考記録値）
	Min          float64 `json:"min"`           // ワースト1フレームのスコア（参考記録値）
}

// TargetMet 目標スコアを満たしているか判定。
// 合否は harmonic_mean のみで行う（mean / min は使わない固定仕様）。
func (q QualityMetrics) TargetMet(targetScore float64) bool {
	return q.HarmonicMean >= targetScore
}
