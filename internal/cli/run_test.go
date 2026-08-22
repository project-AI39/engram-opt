package cli

import (
	"path/filepath"
	"testing"

	"engram-opt/internal/domain"
)

// 出力先が一時ジョブディレクトリ配下の場合は拒否される（成功時クリーンアップで
// 成果物ごと消える事故の防止仕様）。
func TestEnsureOutside(t *testing.T) {
	jobDir := filepath.Join(t.TempDir(), "job")

	cases := []struct {
		name    string
		output  string
		wantErr bool
	}{
		{"inside job dir", filepath.Join(jobDir, "out.mkv"), true},
		{"job dir itself", jobDir, true},
		{"sibling of job dir", filepath.Join(filepath.Dir(jobDir), "ok.mkv"), false},
		{"unrelated path", filepath.Join(t.TempDir(), "ok.mkv"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ensureOutside(jobDir, tc.output)
			if tc.wantErr && err == nil {
				t.Fatalf("output %q should be rejected", tc.output)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("output %q should be accepted: %v", tc.output, err)
			}
		})
	}
}

// memo.md 固定仕様との照合: 探索範囲は15〜36、目標スコア95.0。
func TestBuildSearchConfigDefaultsMatchSpec(t *testing.T) {
	cfg, err := buildSearchConfig("h264", "medium", "", "", "native")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MinCRF != domain.DefaultMinCRF || cfg.MaxCRF != domain.DefaultMaxCRF {
		t.Fatalf("CRF range = [%d,%d], want defaults from domain constants", cfg.MinCRF, cfg.MaxCRF)
	}
	if cfg.TargetScore != domain.DefaultTargetScore {
		t.Fatalf("target = %v, want %v", cfg.TargetScore, domain.DefaultTargetScore)
	}
	if cfg.Codec != domain.CodecH264 || cfg.Preset != "medium" {
		t.Fatalf("codec/preset = %s/%s", cfg.Codec, cfg.Preset)
	}
}

func TestBuildSearchConfigRejectsUnknownCodec(t *testing.T) {
	if _, err := buildSearchConfig("vp9", "medium", "", "", "native"); err == nil {
		t.Fatal("unsupported codec must be rejected at CLI layer")
	}
}

// 起動モード判定（memo.md「TUIウィザード化」のマトリクス照合）。
func TestDecideLaunch(t *testing.T) {
	cases := []struct {
		name     string
		hasInput bool
		tty      bool
		tui      bool
		headless bool
		want     launchMode
		wantErr  bool
	}{
		{"double-click / bare launch", false, true, false, false, launchWizard, false},
		{"terminal with input runs plain", true, true, false, false, launchPlain, false},
		{"terminal with input and --tui", true, true, true, false, launchTUI, false},
		{"pipe with input ignores tui", true, false, true, false, launchPlain, false},
		{"bare launch outside terminal shows help", false, false, false, false, launchHelp, false},
		{"bare --headless errors even on tty", false, true, false, true, 0, true},
		{"headless forces plain", true, true, false, true, launchPlain, false},
		{"headless in pipe", true, false, false, true, launchPlain, false},
		{"headless requires input", false, true, false, true, 0, true},
		{"tui and headless are exclusive", true, true, true, true, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decideLaunch(tc.hasInput, tc.tty, tc.tui, tc.headless)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got mode %v", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("mode = %v, err = %v; want %v", got, err, tc.want)
			}
		})
	}
}

// 出力未指定時の既定名 <入力>.opt.mkv（拡張子置換）。
func TestDefaultOutputPathIfEmpty(t *testing.T) {
	cases := []struct{ out, in, want string }{
		{"", "video.mp4", "video.opt.mkv"},
		{"", "a/b/c.MOV", "a/b/c.opt.mkv"},
		{"explicit.mkv", "video.mp4", "explicit.mkv"}, // 指定済みなら素通し
		{"", "noext", "noext.opt.mkv"},
	}
	for _, c := range cases {
		if got := defaultOutputPathIfEmpty(c.out, c.in); got != c.want {
			t.Fatalf("defaultOutputPathIfEmpty(%q,%q) = %q, want %q", c.out, c.in, got, c.want)
		}
	}
}
