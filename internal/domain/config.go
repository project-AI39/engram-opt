package domain

// VideoCodec 対応コーデック種別。
type VideoCodec string

const (
	CodecAV1  VideoCodec = "av1"  // libsvtav1
	CodecHEVC VideoCodec = "hevc" // libx265
	CodecH264 VideoCodec = "h264" // libx264
)

// EncodeParams 単一試行のエンコードパラメータ。
type EncodeParams struct {
	Codec    VideoCodec
	CRF      int    // 試行するCRF値（整数のみ。範囲はSearchConfigで制御）
	Preset   string // 全試行で一律固定。codec別の実際の引数解決はencoder/ffmpeg内部で行う
	BitDepth int    // 10固定（yuv420p10le）
}

// SearchConfig CRF二分探索の全体設定。
type SearchConfig struct {
	Codec       VideoCodec
	MinCRF      int     // 探索下限（既定15）
	MaxCRF      int     // 探索上限（既定36）
	TargetScore float64 // 合否目標スコア（既定95.0、harmonic_mean基準）
	Preset      string  // 探索中の全試行で一律固定するpreset
}

// 探索パラメータの既定値（memo.md の固定仕様）。
const (
	DefaultMinCRF      = 15
	DefaultMaxCRF      = 36
	DefaultTargetScore = 95.0
)
