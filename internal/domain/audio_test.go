package domain

import (
	"strings"
	"testing"
)

func TestParseAudioMode(t *testing.T) {
	cases := []struct {
		in      string
		want    AudioMode
		wantErr bool
	}{
		{"copy", AudioCopy, false},
		{"opus", AudioOpus, false},
		{"aac", AudioAAC, false},
		{"none", AudioNone, false},
		{"libopus", AudioOpus, false}, // ffmpegの-c:a実名エイリアス
		{"COPY", AudioCopy, false},    // 大文字小文字不問（Trim/ToLowerで正規化）
		{"", "", true},
		{"mp3", "", true},
	}
	for _, c := range cases {
		got, err := ParseAudioMode(c.in)
		if c.wantErr {
			if err == nil || !strings.Contains(err.Error(), "audio mode") {
				t.Errorf("ParseAudioMode(%q): expected audio-mode error, got %v (%q)", c.in, err, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("ParseAudioMode(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

// TargetBitrateKbps はチャンネル数からビットレートを自動判定する（memo.md 音声処理）。
func TestTargetBitrateKbps(t *testing.T) {
	cases := []struct {
		channels int
		opus     int
		aac      int
	}{
		{0, 128, 128}, // 音声なし（呼ばれないはずだが安全側）
		{1, 128, 128}, // mono はステレオ扱い
		{2, 128, 128}, // stereo
		{5, 128, 128}, // 境界未満
		{6, 256, 320}, // 5.1
		{8, 256, 320}, // 7.1
	}
	for _, c := range cases {
		if got := TargetBitrateKbps(c.channels, AudioOpus); got != c.opus {
			t.Errorf("TargetBitrateKbps(%d, opus) = %d, want %d", c.channels, got, c.opus)
		}
		if got := TargetBitrateKbps(c.channels, AudioAAC); got != c.aac {
			t.Errorf("TargetBitrateKbps(%d, aac) = %d, want %d", c.channels, got, c.aac)
		}
	}
}

// SearchConfig.Validate（memo.md「パラメータ一覧」A節の範囲制約）。
func TestSearchConfigValidate(t *testing.T) {
	valid := SearchConfig{Codec: CodecH264, MinCRF: 15, MaxCRF: 36, TargetScore: 95.0, Preset: "medium", BitDepth: 10}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	// BitDepth 0 は「未指定」扱いで許容される（既存テストのリテラル互換）
	zero := valid
	zero.BitDepth = 0
	if err := zero.Validate(); err != nil {
		t.Fatalf("bit depth 0 (unset) should be accepted: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*SearchConfig)
	}{
		{"unknown codec", func(c *SearchConfig) { c.Codec = "vp9" }},
		{"min crf negative", func(c *SearchConfig) { c.MinCRF = -1 }},
		{"max crf over 63", func(c *SearchConfig) { c.MaxCRF = 64 }},
		{"min greater than max", func(c *SearchConfig) { c.MinCRF = 40; c.MaxCRF = 20 }},
		{"target zero", func(c *SearchConfig) { c.TargetScore = 0 }},
		{"target over 100", func(c *SearchConfig) { c.TargetScore = 100.5 }},
		{"empty preset", func(c *SearchConfig) { c.Preset = " " }},
		{"unsupported bit depth", func(c *SearchConfig) { c.BitDepth = 12 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.mut(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
