package domain

import (
	"fmt"
	"strings"
)

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
	BitDepth int    // 出力ビット深度: 10(yuv420p10le) / 8(yuv420p)。既定はSearchConfig.BitDepth

	// OutWidth / OutHeight は出力リサイズ先（0=ソース解像度維持）。
	// シーン選択（フレーム番号）はリサイズに影響されないため、select後にscaleする。
	OutWidth  int
	OutHeight int
}

// SearchConfig CRF二分探索の全体設定。
type SearchConfig struct {
	Codec       VideoCodec
	MinCRF      int         // 探索下限（既定15）
	MaxCRF      int         // 探索上限（既定36）
	TargetScore float64     // 合否目標スコア（既定95.0。基準指標はMetricで決まる）
	Preset      string      // 探索中の全試行で一律固定するpreset
	BitDepth    int         // 出力ビット深度（既定10。0は「未指定」扱いでDefaultBitDepthへ正規化される）
	Metric      ScoreMetric // 合否判定に使うVMAF統計（既定harmonic_mean）

	// Eval は評価プロファイル（アルゴリズム×評価解像度のセット）。
	// ゼロ値は「未指定」扱いでhd1080へ正規化される。
	Eval EvalProfile

	// OutWidth / OutHeight は出力リサイズ先（0,0=ソース解像度維持）。
	OutWidth  int
	OutHeight int
}

// 探索パラメータの既定値（memo.md「パラメータ一覧と露出方針」）。
// Phase 8からウィザード経由で変更可能になったため「既定値」扱い。
const (
	DefaultMinCRF      = 15
	DefaultMaxCRF      = 36
	DefaultTargetScore = 95.0
	DefaultBitDepth    = 10
)

// ResolveVideoCodec はCLI/ウィザード入力を VideoCodec へ解決する。
// コーデック短名（h264等）とffmpegエンコーダ実名（libx264等）の両方を受け付ける。
// 未知名はエラー（フェイルファスト）。
func ResolveVideoCodec(s string) (VideoCodec, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "h264", "libx264", "avc":
		return CodecH264, nil
	case "hevc", "libx265", "h265":
		return CodecHEVC, nil
	case "av1", "libsvtav1", "svt-av1", "svtav1":
		return CodecAV1, nil
	default:
		return "", fmt.Errorf("unsupported codec %q (use h264 | hevc | av1 or libx264 | libx265 | libsvtav1)", s)
	}
}

// Validate はSearchConfigの整合性を検証する（ウィザード確定時とCLI構築時の共通検証）。
func (c SearchConfig) Validate() error {
	switch c.Codec {
	case CodecAV1, CodecHEVC, CodecH264:
	default:
		return fmt.Errorf("unsupported codec %q", c.Codec)
	}
	if c.MinCRF < 0 || c.MinCRF > 63 {
		return fmt.Errorf("min CRF %d out of range [0,63]", c.MinCRF)
	}
	if c.MaxCRF < 0 || c.MaxCRF > 63 {
		return fmt.Errorf("max CRF %d out of range [0,63]", c.MaxCRF)
	}
	if c.MinCRF > c.MaxCRF {
		return fmt.Errorf("min CRF %d must be <= max CRF %d", c.MinCRF, c.MaxCRF)
	}
	if c.TargetScore <= 0 || c.TargetScore > 100 {
		return fmt.Errorf("target score %.2f out of range (0,100]", c.TargetScore)
	}
	if strings.TrimSpace(c.Preset) == "" {
		return fmt.Errorf("preset is empty")
	}
	switch c.BitDepth {
	case 0, DefaultBitDepth, 8:
		// 0は未指定扱い（既定10へ正規化）。それ以外の値は不可
	default:
		return fmt.Errorf("unsupported bit depth %d (use 8 or 10)", c.BitDepth)
	}
	switch c.Metric {
	case "", MetricHarmonic, MetricMean, MetricMin:
		// ""は未指定扱い（EffectiveMetricで既定harmonicへ正規化）
	default:
		return fmt.Errorf("unsupported score metric %q (use harmonic | mean | min)", c.Metric)
	}
	if c.Eval.Model != "" {
		if c.Eval.Width <= 0 || c.Eval.Height <= 0 || c.Eval.Name == "" {
			return fmt.Errorf("incomplete eval profile %q (%dx%d)", c.Eval.Name, c.Eval.Width, c.Eval.Height)
		}
	}
	switch {
	case c.OutWidth == 0 && c.OutHeight == 0:
		// リサイズなし
	case c.OutWidth > 0 && c.OutHeight > 0 &&
		c.OutWidth%2 == 0 && c.OutHeight%2 == 0:
		// 偶数解像度のみ許容
	default:
		return fmt.Errorf("invalid out resolution %dx%d (use even positive dimensions; leave unset to follow the input)", c.OutWidth, c.OutHeight)
	}
	return nil
}

// EffectiveMetric は正規化後の合否指標を返す（ゼロ値→MetricHarmonic）。
func (c SearchConfig) EffectiveMetric() ScoreMetric {
	if c.Metric == "" {
		return MetricHarmonic
	}
	return c.Metric
}

// EffectiveBitDepth は正規化後の出力ビット深度を返す（0→DefaultBitDepth）。
func (c SearchConfig) EffectiveBitDepth() int {
	if c.BitDepth == 0 {
		return DefaultBitDepth
	}
	return c.BitDepth
}

// EffectiveEvalProfile は正規化後の評価プロファイルを返す（ゼロ値→hd1080）。
// 未知のモデル名が入っていた場合もここでは弾かない——ResolveEvalProfileによる
// 構築時検証（フェイルファスト）が前提。
func (c SearchConfig) EffectiveEvalProfile() EvalProfile {
	if c.Eval.Model == "" {
		return DefaultEvalProfile()
	}
	return c.Eval
}

// AudioMode 最終出力への音声の扱い（memo.md「音声処理」）。
// 音声はシーン分割の対象外であり、完成映像への最終ミックス時に1回だけ適用される。
type AudioMode string

const (
	AudioCopy AudioMode = "copy" // 元音声を無劣化コピー（既定）
	AudioOpus AudioMode = "opus" // libopusへ再圧縮（VBR・ビットレート自動）
	AudioAAC  AudioMode = "aac"  // AACへ再圧縮（ABR・ビットレート自動）
	AudioNone AudioMode = "none" // 音声トラックを破棄
)

// DefaultAudioMode はCLIフラグの既定値。
const DefaultAudioMode = AudioCopy

// ParseAudioMode はCLIフラグ値を AudioMode へ変換する。未知名はエラー。
func ParseAudioMode(s string) (AudioMode, error) {
	switch m := AudioMode(strings.ToLower(strings.TrimSpace(s))); m {
	case AudioCopy, AudioOpus, AudioAAC, AudioNone:
		return m, nil
	case "libopus": // ffmpegの-c:a実名も受ける（表示はopusで統一）
		return AudioOpus, nil
	default:
		return "", fmt.Errorf("invalid audio mode %q (use copy | opus | aac | none; alias: libopus)", s)
	}
}

// TargetBitrateKbps はチャンネル数から目標ビットレート(kbps)を返す。
// ch < 6 をステレオ扱い（mono含む）、ch >= 6 をサラウンド扱いとする。
func TargetBitrateKbps(channels int, mode AudioMode) int {
	if channels >= 6 {
		if mode == AudioAAC {
			return 320
		}
		return 256
	}
	return 128
}
