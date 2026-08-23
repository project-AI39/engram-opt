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
