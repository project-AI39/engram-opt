package domain

import "fmt"

// QualityMetrics は評価アルゴリズムが出力する画質スコア群。
type QualityMetrics struct {
	HarmonicMean float64 `json:"harmonic_mean"` // 調和平均（既定の合否指標。低スコア帧のペナルティが最大）
	Mean         float64 `json:"mean"`          // 算術平均
	Min          float64 `json:"min"`           // 最悪1フレームのスコア
}

// ScoreMetric は合否判定の基準となるVMAF統計の種別。
// ゼロ値は MetricHarmonic（=従来の固定仕様）として扱われる。
type ScoreMetric string

const (
	MetricHarmonic ScoreMetric = "harmonic" // 調和平均（既定）
	MetricMean     ScoreMetric = "mean"     // 算術平均
	MetricMin      ScoreMetric = "min"      // 最悪フレーム
)

// ParseScoreMetric はCLI/ウィザードの文字列値を ScoreMetric へ変換する。未知名はエラー。
func ParseScoreMetric(s string) (ScoreMetric, error) {
	switch m := ScoreMetric(s); m {
	case MetricHarmonic, MetricMean, MetricMin:
		return m, nil
	default:
		return "", fmt.Errorf("invalid score metric %q (use harmonic | mean | min)", s)
	}
}

// Score は指定種別のスコアを返す。未知種別はパニックではなくharmonicへフォールバックする
// （呼び出し側でValidate済みという前提の防御実装）。
func (q QualityMetrics) Score(m ScoreMetric) float64 {
	switch m {
	case MetricMean:
		return q.Mean
	case MetricMin:
		return q.Min
	default:
		return q.HarmonicMean
	}
}

// TargetMet は目標スコアを満たすかどうかを、指定した基準指標で判定する。
func (q QualityMetrics) TargetMet(metric ScoreMetric, targetScore float64) bool {
	return q.Score(metric) >= targetScore
}
