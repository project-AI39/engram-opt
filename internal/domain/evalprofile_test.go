package domain

import (
	"strings"
	"testing"
)

// プロファイル解決: 既知名は対応モデルを返し、未知名はフェイルファストでエラー。
func TestResolveEvalProfile(t *testing.T) {
	p, err := ResolveEvalProfile("vmaf_4k_v0.6.1")
	if err != nil {
		t.Fatalf("uhd4k should resolve: %v", err)
	}
	if p.Model != "vmaf_4k_v0.6.1" || p.Width != 3840 || p.Height != 2160 {
		t.Fatalf("uhd4k = %+v", p)
	}
	if _, err := ResolveEvalProfile("nope"); err == nil {
		t.Fatal("unknown profile must be rejected")
	} else if !strings.Contains(err.Error(), "valid:") {
		t.Fatalf("error should list valid names: %v", err)
	}
}

// ゼロ値SearchConfigは既定プロファイルへ正規化される（旧呼び出し側との互換）。
func TestEffectiveEvalProfileDefaults(t *testing.T) {
	var c SearchConfig
	if got := c.EffectiveEvalProfile(); got.Name != DefaultEvalProfileName {
		t.Fatalf("default profile = %s, want %s", got.Name, DefaultEvalProfileName)
	}
}

// 出力解像度パース: 空欄 / WxH直接指定 / プリセット名拒否 / 各種不正値。
func TestParseOutRes(t *testing.T) {
	cases := []struct {
		in      string
		w, h    int
		wantErr bool
	}{
		{"", 0, 0, false}, // 空欄=入力解像度（実行時解決）
		{"", 0, 0, false},
		{"1280x720", 1280, 720, false},
		{"3840x2160", 3840, 2160, false},
		{"1920X1080", 1920, 1080, false}, // 大文字X許容
		{"sd", 0, 0, true},               // プリセット名は不採用
		{"HD", 0, 0, true},               // プリセット名は不採用
		{"fhd", 0, 0, true},              // プリセット名は不採用 //
		{"4K", 0, 0, true},               // プリセット名は不採用 //
		{"1279x720", 0, 0, true},         // 奇数
		{"0x720", 0, 0, true},            // 非正
		{"abc", 0, 0, true},              // 形式不正
		{"1920", 0, 0, true},             // x無し
	}
	for _, tc := range cases {
		w, h, err := ParseOutRes(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseOutRes(%q) should fail", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseOutRes(%q): %v", tc.in, err)
		}
		if w != tc.w || h != tc.h {
			t.Fatalf("ParseOutRes(%q) = (%d,%d), want (%d,%d)", tc.in, w, h, tc.w, tc.h)
		}
	}
}

// Validate: Eval未指定はOK、不完全なプロファイルと奇数リサイズは拒否。
func TestSearchConfigValidateEvalAndOutRes(t *testing.T) {
	base := SearchConfig{Codec: CodecH264, MinCRF: 15, MaxCRF: 36, TargetScore: 95, Preset: "medium"}
	if err := base.Validate(); err != nil {
		t.Fatalf("base should validate: %v", err)
	}

	badProfile := base
	badProfile.Eval = EvalProfile{Name: "x", Model: "some-model", Width: 0, Height: 0}
	if err := badProfile.Validate(); err == nil {
		t.Fatal("eval profile without resolution must be rejected")
	}

	badRes := base
	badRes.OutWidth, badRes.OutHeight = 1919, 1080
	if err := badRes.Validate(); err == nil {
		t.Fatal("odd width must be rejected")
	}
}
