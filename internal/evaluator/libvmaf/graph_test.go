package libvmaf

import (
	"strings"
	"testing"

	"engram-opt/internal/domain"
)

// 評価グラフはプロファイルの解像度へ両入力を正規化することを保証する。
// アルゴリズムが前提とする解像度へ揃えないとスコアがずれ、
// 二分探索が誤ったCRF境界を掴むため（ユーザー指摘への恒久対策）。
func TestBuildEvalGraphNormalizesBothInputs(t *testing.T) {
	sc := domain.Scene{Index: 2, StartFrame: 30, EndFrame: 89}

	cases := []struct {
		profile     domain.EvalProfile
		wantScale   string // 両チェーンに含まれるべき scale 指定
		wantModel   string
		wantSelects []string // 参照側のみに含まれる select 範囲
	}{
		{domain.DefaultEvalProfile(), "scale=1920:1080", "vmaf_v1.0.16_3d0h", nil},
		{
			profile:   domain.EvalProfile{Name: "vmaf-uhd4k", Algorithm: "libvmaf", Model: "vmaf_4k_v0.6.1", Width: 3840, Height: 2160},
			wantScale: "scale=3840:2160",
			wantModel: "vmaf_4k_v0.6.1",
		},
	}

	for _, tc := range cases {
		g := buildEvalGraph(sc, tc.profile, 24000, 1001)
		if n := strings.Count(g, tc.wantScale); n != 2 {
			t.Fatalf("profile %s: scale=%q appears %d times, want 2 (both inputs): %s", tc.profile.Name, tc.wantScale, n, g)
		}
		if !strings.Contains(g, "model='version="+tc.wantModel+"'") {
			t.Fatalf("profile %s: model missing: %s", tc.profile.Name, g)
		}
		if !strings.Contains(g, "select='between(n,30,89)'") {
			t.Fatalf("reference chain must select the scene range: %s", g)
		}
		if strings.Count(g, "select=") != 1 {
			t.Fatalf("only reference side may contain select: %s", g)
		}
		// 正規化式は両チェーンに同一で入る（framesyncペアリング崩壊の防止）
		if n := strings.Count(g, "settb=1/24000,setpts=1001*N"); n != 2 {
			t.Fatalf("timestamp normalization must appear on both chains (%d): %s", n, g)
		}
	}
}
