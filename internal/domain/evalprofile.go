package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// EvalProfile は評価アルゴリズムと評価解像度のセット。
// アルゴリズムごとに妥な評価解像度が異なるため、単体ではなくセットで指定する
// （memo.md「評価プロファイル」）。スコアは同一プロファイル内でのみ比較可能。
type EvalProfile struct {
	Name   string // CLI/ウィザードで選ぶID
	Model  string // libvmaf の version= 名
	Width  int    // 評価用正規化解像度（両入力をこのサイズへ揃えて比較）
	Height int
}

// 組み込みプロファイル。モデルの実在はpin版ffmpegに対して実機確認済み
// （memo.md「評価プロファイル」参照）。追加はこの表＋ヘルプ文言のみ。
var evalProfiles = []EvalProfile{
	{Name: "hd1080", Model: "vmaf_v1.0.16_3d0h", Width: 1920, Height: 1080},
	{Name: "uhd4k", Model: "vmaf_4k_v0.6.1", Width: 3840, Height: 2160},
}

// DefaultEvalProfileName は未指定時に使うプロファイル。
const DefaultEvalProfileName = "hd1080"

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

// ParseOutRes は出力解像度指定をパースする。"native" はリサイズなし(0,0)、
// それ以外は "1280x720" 形式。エンコーダの要件上、偶数のみ許容する。
func ParseOutRes(s string) (width, height int, err error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "native") {
		return 0, 0, nil
	}
	parts := strings.SplitN(strings.ToLower(s), "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf(`invalid --out-res %q: expect "native" or "<even>x<even>" (e.g. 1280x720)`, s)
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
