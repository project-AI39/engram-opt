package ffmpeg

import (
	"testing"

	"engram-opt/internal/domain"
)

// buildSelectVF のスケール連結: リサイズ指定時のみ select/setpts の末尾に付く。
func TestBuildSelectVF(t *testing.T) {
	sc := domain.Scene{Index: 0, StartFrame: 10, EndFrame: 49}

	if got, want := buildSelectVF(sc, 0, 0),
		"select='between(n,10,49)',setpts=PTS-STARTPTS"; got != want {
		t.Fatalf("native vf = %q, want %q", got, want)
	}
	if got, want := buildSelectVF(sc, 1280, 720),
		"select='between(n,10,49)',setpts=PTS-STARTPTS,scale=1280:720"; got != want {
		t.Fatalf("resized vf = %q, want %q", got, want)
	}
}
