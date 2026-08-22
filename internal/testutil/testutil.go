// Package testutil は統合テスト/E2E共通のヘルパーを提供する。
//
// 責務:
//   - 実バイナリ（build/bin 配下）の存在ガード。未セットアップ環境や -short 実行時は
//     スキップ理由付きでSkipし、単体テストの高速ループを妨げない
//   - テスト動画の動的生成（バイナリフィクスチャをGitにコミットしないため。
//     pin済みffmpeg 8.1.2 で生成するため再現性は確保される）
//   - ffprobe による出力検証（フレーム数・pix_fmt等。仕様との照合に使用）
package testutil

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"engram-opt/internal/toolbin"
)

// RequireBinaries は指定した同梱バイナリが解決できることを確認する。
// -short 指定時、または未セットアップ環境ではスキップ理由付きで t.Skip する。
func RequireBinaries(t testing.TB, names ...string) {
	t.Helper()
	if testing.Short() {
		t.Skip("-short: integration test skipped")
	}
	for _, n := range names {
		if _, err := toolbin.Resolve(n); err != nil {
			t.Skipf("binary %q unavailable (%v); run 'go run ./cmd/engram setup' first", n, err)
		}
	}
}

// GenerateSampleVideo は複数パターンをハードカット結合したテスト動画を lavfi で
// 生成してパスを返す。
//
// 仕様: 320x240 / 30fps / 6秒 = 180フレーム。
// 構成: testsrc2(2s) + smptebars(2s) + testsrc2(2s) → セグメント境界 60/120 フレーム目に
// 強い内容差のあるハードカットが存在する（シーン検出の確定入力として利用可能）。
func GenerateSampleVideo(t testing.TB, dir string) string {
	t.Helper()
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg unavailable (%v); run 'go run ./cmd/engram setup' first", err)
	}
	out := filepath.Join(dir, "sample.mp4")
	filter := "testsrc2=size=320x240:rate=30:duration=2[a];" +
		"smptebars=size=320x240:rate=30:duration=2[b];" +
		"testsrc2=size=320x240:rate=30:duration=2[c];" +
		"[a][b][c]concat=n=3:v=1:a=0[out]"
	cmd := exec.Command(ffmpegPath,
		"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-filter_complex", filter, "-map", "[out]",
		"-pix_fmt", "yuv420p", "-c:v", "libx264", "-crf", "18",
		out)
	if b, cerr := cmd.CombinedOutput(); cerr != nil {
		t.Fatalf("generating sample video failed: %v\n%s", cerr, b)
	}
	return out
}

// StreamInfo は動画ストリーム検証のための最小限の情報。
type StreamInfo struct {
	Frames    int64  // デコード実数（-count_frames）
	PixFmt    string // 例: yuv420p10le（10-bit固定仕様の検証）
	FrameRate string // 例: 30/1（有理数表記そのもの）
}

// ProbeStreamInfo は ffprobe -count_frames でデコード実フレーム数等を取得する。
// nb_frames メタデータはコンテナによって欠落するため、実デコード数を採用する。
func ProbeStreamInfo(t testing.TB, ctx context.Context, videoPath string) StreamInfo {
	t.Helper()
	ffprobePath, err := toolbin.Resolve("ffprobe")
	if err != nil {
		t.Skipf("ffprobe unavailable (%v)", err)
	}
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-select_streams", "v:0", "-count_frames",
		"-show_entries", "stream=nb_read_frames,pix_fmt,r_frame_rate",
		"-of", "json", videoPath).Output()
	if err != nil {
		t.Fatalf("ffprobe failed: %v", err)
	}
	var parsed struct {
		Streams []struct {
			NbReadFrames string `json:"nb_read_frames"`
			PixFmt       string `json:"pix_fmt"`
			RFrameRate   string `json:"r_frame_rate"`
		} `json:"streams"`
	}
	if uerr := json.Unmarshal(out, &parsed); uerr != nil || len(parsed.Streams) == 0 {
		t.Fatalf("unexpected ffprobe output: %s (%v)", out, uerr)
	}
	s := parsed.Streams[0]
	frames, perr := strconv.ParseInt(s.NbReadFrames, 10, 64)
	if perr != nil {
		t.Fatalf("invalid nb_read_frames %q", s.NbReadFrames)
	}
	return StreamInfo{Frames: frames, PixFmt: s.PixFmt, FrameRate: s.RFrameRate}
}

// CountKeyFrames は動画内のキーフレーム総数を数える。
// キーフレーム方針（先頭IDR必須＋以降はエンコーダー適応）の検証に使う。
func CountKeyFrames(t testing.TB, ctx context.Context, videoPath string) int {
	t.Helper()
	ffprobePath, err := toolbin.Resolve("ffprobe")
	if err != nil {
		t.Skipf("ffprobe unavailable (%v)", err)
	}
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "frame=key_frame",
		"-of", "csv=p=0", videoPath).Output()
	if err != nil {
		t.Fatalf("ffprobe (keyframes) failed: %v", err)
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		// ffprobe出力はCRLF＋csvフィールドによって末尾カンマ付き（実測: 先頭フレーム行は"1,\r\n"）
		v := strings.Trim(strings.TrimSpace(line), ",")
		if v == "1" {
			count++
		}
	}
	return count
}

// FirstFrameIsKey は動画の最初の映像フレームがキーフレームかどうかを返す。
// 「チャンク先頭IDR必須」（ストリームコピー結合とシークの前提）の検証に使う。
func FirstFrameIsKey(t testing.TB, ctx context.Context, videoPath string) bool {
	t.Helper()
	ffprobePath, err := toolbin.Resolve("ffprobe")
	if err != nil {
		t.Skipf("ffprobe unavailable (%v)", err)
	}
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "frame=key_frame",
		"-of", "csv=p=0",
		"-read_intervals", "%+#1", videoPath).Output()
	if err != nil {
		t.Fatalf("ffprobe (first keyframe) failed: %v", err)
	}
	v := strings.Trim(strings.TrimSpace(string(out)), ",")
	return v == "1"
}

// HasFFmpegEncoder は同梱ffmpegが指定エンコーダを持つかどうかを実測で返す。
// ビルド変種（essentials/full）による差異の吸収に使う。
func HasFFmpegEncoder(t testing.TB, encoder string) bool {
	t.Helper()
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg unavailable (%v)", err)
	}
	out, err := exec.Command(ffmpegPath, "-hide_banner", "-encoders").Output()
	if err != nil {
		t.Fatalf("listing encoders failed: %v", err)
	}
	return strings.Contains(string(out), " "+encoder+" ")
}

// GenerateSampleVideoWithAudio は GenerateSampleVideo と同一映像（180フレーム・
// カット60/120）にステレオAACのサイン音声を付けた入力を生成する。
// 音声ミックス（--audio copy/opus/aac）の統合テスト・E2E用。
func GenerateSampleVideoWithAudio(t testing.TB, dir string) string {
	t.Helper()
	video := GenerateSampleVideo(t, dir)
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg unavailable (%v)", err)
	}
	out := filepath.Join(dir, "sample_audio.mp4")
	cmd := exec.Command(ffmpegPath,
		"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-i", video,
		"-f", "lavfi", "-i", "sine=frequency=440:duration=6",
		"-map", "0:v:0", "-map", "1:a:0",
		"-c:v", "copy",
		"-c:a", "aac", "-b:a", "96k", "-ac", "2",
		"-shortest", out)
	if b, cerr := cmd.CombinedOutput(); cerr != nil {
		t.Fatalf("generating sample with audio failed: %v\n%s", cerr, b)
	}
	return out
}

// AudioStreamInfo は音声ストリーム検証のための最小限の情報。
type AudioStreamInfo struct {
	CodecName string
	Channels  int
}

// ProbeAudioStreams は全音声ストリームの codec_name / channels を返す。
func ProbeAudioStreams(t testing.TB, ctx context.Context, mediaPath string) []AudioStreamInfo {
	t.Helper()
	ffprobePath, err := toolbin.Resolve("ffprobe")
	if err != nil {
		t.Skipf("ffprobe unavailable (%v)", err)
	}
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-select_streams", "a",
		"-show_entries", "stream=codec_name,channels",
		"-of", "json", mediaPath).Output()
	if err != nil {
		t.Fatalf("ffprobe (audio) failed: %v", err)
	}
	var parsed struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
			Channels  int    `json:"channels"`
		} `json:"streams"`
	}
	if uerr := json.Unmarshal(out, &parsed); uerr != nil {
		t.Fatalf("unexpected ffprobe output: %s (%v)", out, uerr)
	}
	streams := make([]AudioStreamInfo, 0, len(parsed.Streams))
	for _, s := range parsed.Streams {
		streams = append(streams, AudioStreamInfo{CodecName: s.CodecName, Channels: s.Channels})
	}
	return streams
}
