package ffmpeg

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"engram-opt/internal/domain"
	"engram-opt/internal/testutil"
	"engram-opt/internal/toolbin"
)

// MuxAudio の実機統合テスト（memo.md「音声処理」の仕様照合）。
func TestMuxAudioIntegration(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe")
	ctx := context.Background()
	dir := t.TempDir()

	// 音声つき入力（ステレオAAC）と音声なし映像を作る
	withAudio := testutil.GenerateSampleVideoWithAudio(t, dir)
	videoOnly := filepath.Join(dir, "vonly.mkv")
	genVideoOnly(t, videoOnly)

	inStreams := testutil.ProbeAudioStreams(t, ctx, withAudio)
	if len(inStreams) != 1 || inStreams[0].CodecName != "aac" || inStreams[0].Channels != 2 {
		t.Fatalf("fixture audio mismatch: %+v", inStreams)
	}

	enc := New()

	t.Run("copy preserves source codec", func(t *testing.T) {
		out := filepath.Join(dir, "copy.mkv")
		if err := enc.MuxAudio(ctx, videoOnly, withAudio, domain.AudioCopy, out); err != nil {
			t.Fatalf("mux copy: %v", err)
		}
		got := testutil.ProbeAudioStreams(t, ctx, out)
		if len(got) != 1 || got[0].CodecName != "aac" || got[0].Channels != 2 {
			t.Fatalf("audio after copy = %+v, want single stereo aac", got)
		}
	})

	t.Run("opus re-encodes via libopus", func(t *testing.T) {
		if !testutil.HasFFmpegEncoder(t, "libopus") {
			t.Skip("libopus unavailable in this ffmpeg build")
		}
		out := filepath.Join(dir, "opus.mkv")
		if err := enc.MuxAudio(ctx, videoOnly, withAudio, domain.AudioOpus, out); err != nil {
			t.Fatalf("mux opus: %v", err)
		}
		got := testutil.ProbeAudioStreams(t, ctx, out)
		if len(got) != 1 || got[0].CodecName != "opus" || got[0].Channels != 2 {
			t.Fatalf("audio after opus = %+v, want single stereo opus", got)
		}
	})

	t.Run("aac re-encodes via native aac", func(t *testing.T) {
		out := filepath.Join(dir, "aac.mkv")
		if err := enc.MuxAudio(ctx, videoOnly, withAudio, domain.AudioAAC, out); err != nil {
			t.Fatalf("mux aac: %v", err)
		}
		got := testutil.ProbeAudioStreams(t, ctx, out)
		if len(got) != 1 || got[0].CodecName != "aac" || got[0].Channels != 2 {
			t.Fatalf("audio after aac = %+v, want single stereo aac", got)
		}
	})

	t.Run("silent input yields video-only output", func(t *testing.T) {
		out := filepath.Join(dir, "silent.mkv")
		if err := enc.MuxAudio(ctx, videoOnly, videoOnly, domain.AudioCopy, out); err != nil {
			t.Fatalf("mux silent: %v", err)
		}
		if got := testutil.ProbeAudioStreams(t, ctx, out); len(got) != 0 {
			t.Fatalf("silent input should produce no audio streams, got %+v", got)
		}
	})

	t.Run("none mode is rejected", func(t *testing.T) {
		out := filepath.Join(dir, "none.mkv")
		if err := enc.MuxAudio(ctx, videoOnly, withAudio, domain.AudioNone, out); err == nil {
			t.Fatal("expected error for none mode")
		}
	})
}

// genVideoOnly は音声なしの極小動画を生成する（ミックス元として使用）。
func genVideoOnly(t *testing.T, out string) {
	t.Helper()
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg unavailable (%v)", err)
	}
	b, cerr := exec.Command(ffmpegPath,
		"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=1",
		"-an", "-pix_fmt", "yuv420p10le",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "30",
		out).CombinedOutput()
	if cerr != nil {
		t.Fatalf("generating video-only fixture failed: %v\n%s", cerr, b)
	}
}
