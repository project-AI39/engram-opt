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
		{"", "", true},
		{"mp3", "", true},
		{"COPY", "", true}, // 大文字は許容しない（CLIフラグは小文字統一）
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
		{0, 128, 128},  // 音声なし（呼ばれないはずだが安全側）
		{1, 128, 128},  // mono はステレオ扱い
		{2, 128, 128},  // stereo
		{5, 128, 128},  // 境界未満
		{6, 256, 320},  // 5.1
		{8, 256, 320},  // 7.1
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
