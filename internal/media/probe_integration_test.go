package media

// probe.go の実機プローブ群（ffprobe必須のため統合テスト扱い）。

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"engram-opt/internal/testutil"
	"engram-opt/internal/toolbin"
)

// TestProbeDurationSecondsIntegration は通常コンテナでは既知値、
// エレメンタリストリーム（durationメタデータ無し）では unknown になることを検証する。
func TestProbeDurationSecondsIntegration(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe")
	ctx := context.Background()

	video := testutil.GenerateSampleVideo(t, t.TempDir())
	dur, ok := ProbeDurationSeconds(ctx, video)
	if !ok || dur <= 0 {
		t.Fatalf("container input: dur=%f ok=%v, want known positive duration", dur, ok)
	}

	raw := genElementaryH264(t, t.TempDir())
	if _, ok := ProbeDurationSeconds(ctx, raw); ok {
		t.Fatal("elementary stream has no format duration; must be reported unknown")
	}
}

// TestProbeStreamNotesIntegration は複数音声・字幕の検出と、
// 単一構成での注意文ゼロを検証する。
func TestProbeStreamNotesIntegration(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe")
	ctx := context.Background()
	dir := t.TempDir()
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg unavailable (%v)", err)
	}

	multi := filepath.Join(dir, "multi.mkv")
	b, err := exec.Command(ffmpegPath, "-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=1",
		"-map", "0:v", "-map", "1:a", "-map", "2:a",
		"-c:v", "libx264", "-crf", "30", "-pix_fmt", "yuv420p", "-c:a", "aac",
		multi).CombinedOutput()
	if err != nil {
		t.Fatalf("generating multi-audio fixture: %v\n%s", err, toolbin.Tail(string(b), 5))
	}

	notes := strings.Join(ProbeStreamNotes(ctx, multi), "\n")
	if !strings.Contains(notes, "2 audio streams") {
		t.Fatalf("notes should mention dual audio, got %q", notes)
	}

	subs := filepath.Join(dir, "subs.mkv")
	srt := filepath.Join(dir, "s.srt")
	if werr := os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nx\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	b, err = exec.Command(ffmpegPath, "-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=30",
		"-i", srt,
		"-map", "0:v", "-map", "1:s",
		"-c:v", "libx264", "-crf", "30", "-pix_fmt", "yuv420p", "-c:s", "srt",
		subs).CombinedOutput()
	if err != nil {
		t.Fatalf("generating subtitle fixture: %v\n%s", err, toolbin.Tail(string(b), 5))
	}
	notes = strings.Join(ProbeStreamNotes(ctx, subs), "\n")
	if !strings.Contains(notes, "subtitle") {
		t.Fatalf("notes should mention subtitles, got %q", notes)
	}

	plain := testutil.GenerateSampleVideo(t, dir)
	if got := ProbeStreamNotes(ctx, plain); len(got) != 0 {
		t.Fatalf("single-audio video should have no notes, got %v", got)
	}
}

// genElementaryH264 は duration メタデータを持たない生H.ESを生成する。
func genElementaryH264(t *testing.T, dir string) string {
	t.Helper()
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg unavailable (%v)", err)
	}
	out := filepath.Join(dir, "raw.h264")
	b, cerr := exec.Command(ffmpegPath, "-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=30",
		"-frames:v", "10", "-c:v", "libx264", "-crf", "30", "-f", "h264",
		out).CombinedOutput()
	if cerr != nil {
		t.Fatalf("generating elementary stream: %v\n%s", cerr, toolbin.Tail(string(b), 5))
	}
	return out
}
