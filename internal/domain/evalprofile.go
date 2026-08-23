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
	Name      string // CLI/ウィザードで選ぶID。libvmaf系はversion=へ渡すモデル名そのもの
	Algorithm string // 評価アルゴリズムID。対応するQualityEvaluator実装のみ受け付ける
	Model     string // アルゴリズム内でのモデル指定（libvmafなら version= 名）
	Width     int    // 評価用正規化解像度（両入力をこのサイズへ揃えて比較）
	Height    int
}

// 組み込みプロファイル。Nameはlibvmafのversion=に渡すモデル名そのもの——
// ffmpegを普段使う人がコマンドと対応を一目で判るようにする（実機確認済み）。
//
// 構造は「アルゴリズム → そのアルゴリズムが対応する評価解像度のリスト」の2軸。
// ウィザードはこの構造をそのまま2段選択（アルゴリズム→解像度）に使う。
// 新アルゴリズム追加はこのレジストリへの1ブロック追加＋対応評価器実装のみ。
var evalAlgorithms = []struct {
	ID       string
	Profiles []EvalProfile
}{
	{
		ID: "libvmaf",
		Profiles: []EvalProfile{
			{Name: "vmaf_v1.0.16_3d0h", Algorithm: "libvmaf", Model: "vmaf_v1.0.16_3d0h", Width: 1920, Height: 1080},
			{Name: "vmaf_4k_v0.6.1", Algorithm: "libvmaf", Model: "vmaf_4k_v0.6.1", Width: 3840, Height: 2160},
		},
	},
}

// DefaultEvalProfileName は未指定時に使うプロファイル。
const DefaultEvalProfileName = "vmaf_v1.0.16_3d0h"

// DefaultEvalAlgorithm は現在実装されている唯一の評価アルゴリズム。
// QualityEvaluator 実装（libvmaf）はこれ以外のAlgorithmを受け付けない。
const DefaultEvalAlgorithm = "libvmaf"

// EvalAlgorithmIDs はアルゴリズムIDの一覧（ウィザード第1段の選択肢）。
func EvalAlgorithmIDs() []string {
	ids := make([]string, 0, len(evalAlgorithms))
	for _, a := range evalAlgorithms {
		ids = append(ids, a.ID)
	}
	return ids
}

// EvalProfilesFor は指定アルゴリズムが対応する評価解像度（プロファイル）一覧を返す。
// 未知名には空スライスを返す（呼び出し側はValidate/Resolveで弾く）。
func EvalProfilesFor(algorithm string) []EvalProfile {
	for _, a := range evalAlgorithms {
		if a.ID == algorithm {
			return a.Profiles
		}
	}
	return nil
}

// DefaultEvalProfile は既定プロファイルを返す（テスト等で明示的に渡す場合の利便用）。
func DefaultEvalProfile() EvalProfile {
	return evalAlgorithms[0].Profiles[0]
}

// ResolveEvalProfile は名前からプロファイルを解決する。
// 未知の名前は黙って既定へ落とさずエラーにする（フェイルファスト方針）。
func ResolveEvalProfile(name string) (EvalProfile, error) {
	for _, a := range evalAlgorithms {
		for _, p := range a.Profiles {
			if p.Name == name {
				return p, nil
			}
		}
	}
	var names []string
	for _, a := range evalAlgorithms {
		for _, p := range a.Profiles {
			names = append(names, p.Name)
		}
	}
	return EvalProfile{}, fmt.Errorf("unknown eval profile %q (valid: %s)", name, strings.Join(names, ", "))
}

// EvalProfileNames は全プロファイルIDの一覧（CLIヘルプ等の表示順）。
func EvalProfileNames() []string {
	var names []string
	for _, a := range evalAlgorithms {
		for _, p := range a.Profiles {
			names = append(names, p.Name)
		}
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

// ParseOutRes は出力解像度指定をパースする。
// 空文字列は「入力動画と同じ解像度」（実行時に入力の実寸へ解決される）を意味し、
// それ以外は "<偶数>x<偶数>" の直接指定のみを受け付ける。
// プリセット名や native といった語は使わない——ユーザーに常に実寸を意識させるため。
func ParseOutRes(s string) (width, height int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}
	parts := strings.SplitN(strings.ToLower(s), "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf(`invalid --out-res %q: leave empty for input resolution, or use <even>x<even> (e.g. 1920x1080)`, s)
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
