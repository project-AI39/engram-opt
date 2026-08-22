package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// EvalProfile は評価アルゴリズムと評価解像度のセット。
// アルゴリズムごとに妥な評価解像度が異なるため、単体ではなくセットで指定する
// （memo.md「評価プロファイル」）。スコアは同一プロファイル内でのみ比較可能。
//
// 拡張規約: 新しい評価アルゴリズムの追加は、このレジストリへ
// 「Algorithm=その評価器が解釈できるID」のエントリを1行足し、対応する
// QualityEvaluator 実装を配線するだけ。未知のAlgorithmは評価器側で
// エラーになるため、誤った組合せは静かに動かない（フェイルファスト）。
type EvalProfile struct {
	Name      string // CLI/ウィザードで選ぶID（<algorithm>-<resolution> 形式）
	Algorithm string // 評価アルゴリズムID。対応するQualityEvaluator実装のみ受け付ける
	Model     string // アルゴリズム内でのモデル指定（libvmafなら version= 名）
	Width     int    // 評価用正規化解像度（両入力をこのサイズへ揃えて比較）
	Height    int
}

// 組み込みプロファイル。モデルの実在はpin版ffmpegに対して実機確認済み
// （memo.md「評価プロファイル」参照）。追加はこの表＋ヘルプ文言のみで済む設計。
// 現在はlibvmaf系のみ。将来的にXPSNR/SSIM等を追加する場合は
// Algorithm を分けた別エントリとなる（例: {Name:"xpsnr-hd", Algorithm:"xpsnr", ...}）。
var evalProfiles = []EvalProfile{
	{Name: "vmaf-hd1080", Algorithm: "libvmaf", Model: "vmaf_v1.0.16_3d0h", Width: 1920, Height: 1080},
	{Name: "vmaf-uhd4k", Algorithm: "libvmaf", Model: "vmaf_4k_v0.6.1", Width: 3840, Height: 2160},
}

// DefaultEvalProfileName は未指定時に使うプロファイル。
const DefaultEvalProfileName = "vmaf-hd1080"

// DefaultEvalAlgorithm は現在実装されている唯一の評価アルゴリズム。
// QualityEvaluator 実装（libvmaf）はこれ以外のAlgorithmを受け付けない。
const DefaultEvalAlgorithm = "libvmaf"

// DefaultEvalProfile は既定プロファイルを返す（テスト等で明示的に渡す場合の利便用）。
func DefaultEvalProfile() EvalProfile {
	return evalProfiles[0]
}

// ResolveEvalProfile は名前からプロファイルを解決する。
// 未知の名前は黙って既定へ落とさずエラーにする（フェイルファスト方針）。
func ResolveEvalProfile(name string) (EvalProfile, error) {
	for _, p := range evalProfiles {
		if p.Name == name {
			return p, nil
		}
	}
	names := make([]string, 0, len(evalProfiles))
	for _, p := range evalProfiles {
		names = append(names, p.Name)
	}
	return EvalProfile{}, fmt.Errorf("unknown eval profile %q (valid: %s)", name, strings.Join(names, ", "))
}

// EvalProfileNames は選択肢IDの一覧（ウィザードcycle表示順）。
func EvalProfileNames() []string {
	names := make([]string, 0, len(evalProfiles))
	for _, p := range evalProfiles {
		names = append(names, p.Name)
	}
	return names
}

// Validate はプロファイルの完全性を検証する（評価直前の最終防衛線）。
func (p EvalProfile) Validate() error {
	if p.Model == "" {
		return fmt.Errorf("model is empty")
	}
	if p.Width <= 0 || p.Height <= 0 {
		return fmt.Errorf("invalid evaluation resolution %dx%d", p.Width, p.Height)
	}
	return nil
}

// 出力解像度プリセット（16:9基準）。CLI/Wizard共通で受ける簡易名。
// "sd" はストリーミング慣例の 854x480 を採用する。
var outResPresets = map[string][2]int{
	"sd":  {854, 480},
	"hd":  {1280, 720},
	"fhd": {1920, 1080},
	"4k":  {3840, 2160},
}

// OutResPresets はプリセット名と解像度の一覧（ヘルプ表示順）。
func OutResPresets() []struct {
	Name          string
	Width, Height int
} {
	return []struct {
		Name          string
		Width, Height int
	}{
		{"native", 0, 0},
		{"sd", 854, 480},
		{"hd", 1280, 720},
		{"fhd", 1920, 1080},
		{"4k", 3840, 2160},
	}
}

// ParseOutRes は出力解像度指定をパースする。
// 受ける形式: "native"(未指定扱い) / プリセット名(sd|hd|fhd|4k・大文字小文字不問) /
// "<偶数>x<偶数>"（例: 1280x720）。エンコーダの要件上、直接指定は偶数のみ許容。
func ParseOutRes(s string) (width, height int, err error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "native") {
		return 0, 0, nil
	}
	if dim, ok := outResPresets[strings.ToLower(s)]; ok {
		return dim[0], dim[1], nil
	}
	parts := strings.SplitN(strings.ToLower(s), "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf(`invalid --out-res %q: expect native | sd | hd | fhd | 4k | <even>x<even>`, s)
	}
	w, werr := strconv.Atoi(parts[0])
	h, herr := strconv.Atoi(parts[1])
	if werr != nil || herr != nil {
		return 0, 0, fmt.Errorf("invalid --out-res %q: width/height must be integers", s)
	}
	if w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("invalid --out-res %q: dimensions must be positive", s)
	}
	if w%2 != 0 || h%2 != 0 {
		return 0, 0, fmt.Errorf("invalid --out-res %q: dimensions must be even numbers (encoder requirement)", s)
	}
	return w, h, nil
}
