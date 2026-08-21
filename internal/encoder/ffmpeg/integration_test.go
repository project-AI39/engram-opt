package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"engram-opt/internal/domain"
	"engram-opt/internal/evaluator/libvmaf"
	"engram-opt/internal/testutil"
)

// 実バイナリ統合テスト: FFmpegチャンク切り出しの仕様遵守を検証する。
func TestEncodeChunkIntegration(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe")
	ctx := context.Background()

	video := testutil.GenerateSampleVideo(t, t.TempDir())
	scene := domain.Scene{Index: 0, StartFrame: 0, EndFrame: 59} // 60フレーム
	out := filepath.Join(t.TempDir(), "chunk.mkv")

	err := New().EncodeChunk(ctx, video, scene,
		domain.EncodeParams{Codec: domain.CodecH264, CRF: 18, Preset: "medium", BitDepth: 10}, out)
	if err != nil {
		t.Fatalf("EncodeChunk failed: %v", err)
	}

	info := testutil.ProbeStreamInfo(t, ctx, out)
	// フレーム完全一致仕様（select between(n,S,E)）: 出力はシーン長と一致する
	if info.Frames != scene.FrameCount() {
		t.Fatalf("frame-exactness violated: got %d frames, want %d", info.Frames, scene.FrameCount())
	}
	// 10-bit固定仕様
	if info.PixFmt != "yuv420p10le" {
		t.Fatalf("pix_fmt = %q, want yuv420p10le", info.PixFmt)
	}
	if info.FrameRate != "30/1" {
		t.Fatalf("frame_rate = %q, want 30/1", info.FrameRate)
	}
	// IDRキーフレーム先頭のみ仕様: GOP=シーン長 + sc_threshold無効化により中間IDRは発生しない
	if kf := testutil.CountKeyFrames(t, ctx, out); kf != 1 {
		t.Fatalf("keyframe count = %d, want exactly 1 (chunk head IDR only)", kf)
	}
}

// チャンク内容が指定区間と正しく対応していることの間接証明。
// 区間がずれていれば時間基準正規化後も別フレーム同士が比較され、
// VMAFスコアが崩壊する（Phase 3実測）。crf18のほぼ透明な再エンコードなら
// 正しく対応している場合に限り高スコアになる。
func TestEncodeChunkRangeCorrectnessViaVMAF(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe")
	ctx := context.Background()

	video := testutil.GenerateSampleVideo(t, t.TempDir())
	scene := domain.Scene{Index: 0, StartFrame: 0, EndFrame: 59}
	chunk := filepath.Join(t.TempDir(), "chunk.mkv")

	if err := New().EncodeChunk(ctx, video, scene,
		domain.EncodeParams{Codec: domain.CodecH264, CRF: 18, Preset: "medium", BitDepth: 10}, chunk); err != nil {
		t.Fatalf("EncodeChunk failed: %v", err)
	}

	m, err := libvmaf.New().Evaluate(ctx, video, scene, chunk)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if m.HarmonicMean < 90.0 {
		t.Fatalf("harmonic_mean = %.2f, want >= 90.0 (chunk content likely misaligned with source range)", m.HarmonicMean)
	}
}

// 無劣化結合: チャンク総フレーム数が結合後も保存されること（ストリームコピーの検証）。
func TestConcatChunksIntegration(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe")
	ctx := context.Background()
	dir := t.TempDir()
	video := testutil.GenerateSampleVideo(t, dir)

	scenes := []domain.Scene{
		{Index: 0, StartFrame: 0, EndFrame: 59},
		{Index: 1, StartFrame: 60, EndFrame: 119},
	}
	var chunks []string
	for _, sc := range scenes {
		out := filepath.Join(dir, fmt.Sprintf("chunk_%02d.mkv", sc.Index))
		if err := New().EncodeChunk(ctx, video, sc,
			domain.EncodeParams{Codec: domain.CodecH264, CRF: 18, Preset: "medium", BitDepth: 10}, out); err != nil {
			t.Fatalf("EncodeChunk(%+v) failed: %v", sc, err)
		}
		chunks = append(chunks, out)
	}

	final := filepath.Join(dir, "nested", "deep", "final.mkv")
	if err := New().ConcatChunks(ctx, chunks, final); err != nil {
		t.Fatalf("ConcatChunks failed: %v", err)
	}

	info := testutil.ProbeStreamInfo(t, ctx, final)
	wantTotal := scenes[0].FrameCount() + scenes[1].FrameCount()
	if info.Frames != wantTotal {
		t.Fatalf("concat frame count = %d, want %d (lossless copy should preserve all frames)", info.Frames, wantTotal)
	}
	if info.PixFmt != "yuv420p10le" {
		t.Fatalf("pix_fmt after concat = %q", info.PixFmt)
	}
}

// ctxがexec.CommandContextまで配線されていることの検証（キャンセルで子プロセス停止の担保）。
// 実行中プロセスのkillは os/exec の CommandContext 保証に依存する。
func TestEncodeChunkContextCancellationWiring(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 開始前キャンセル

	video := testutil.GenerateSampleVideo(t, t.TempDir())
	err := New().EncodeChunk(ctx, video,
		domain.Scene{Index: 0, StartFrame: 0, EndFrame: 59},
		domain.EncodeParams{Codec: domain.CodecH264, CRF: 18, Preset: "medium", BitDepth: 10},
		filepath.Join(t.TempDir(), "out.mkv"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// memo「依存ツール」の対応エンコーダ検証: libx265 / libsvtav1 経路が実際に動作し、
// どちらも10-bit固定仕様を満たすこと。AV1はpreset名→数値の解決経路も同時に通る。
func TestEncodeChunkCodecVariantsIntegration(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe")
	ctx := context.Background()
	video := testutil.GenerateSampleVideo(t, t.TempDir())
	scene := domain.Scene{Index: 0, StartFrame: 0, EndFrame: 59}

	cases := []struct {
		name   string
		codec  domain.VideoCodec
		preset string
	}{
		{"hevc", domain.CodecHEVC, "medium"},
		{"av1", domain.CodecAV1, "medium"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 実測: gyan essentials 8.1.2 には libsvtav1 が含まれない（AV1はlibaom-av1/HWのみ）。
			// fullビルド等へ移行した際にこの検証が自動的に有効化されるよう能力検出でガードする。
			if tc.codec == domain.CodecAV1 && !testutil.HasFFmpegEncoder(t, "libsvtav1") {
				t.Skip("current ffmpeg build lacks libsvtav1; see memo.md dependency notes")
			}
			out := filepath.Join(t.TempDir(), tc.name+".mkv")
			err := New().EncodeChunk(ctx, video, scene,
				domain.EncodeParams{Codec: tc.codec, CRF: 28, Preset: tc.preset, BitDepth: 10}, out)
			if err != nil {
				t.Fatalf("EncodeChunk(%s) failed: %v", tc.codec, err)
			}
			info := testutil.ProbeStreamInfo(t, ctx, out)
			if info.Frames != 60 || info.PixFmt != "yuv420p10le" {
				t.Fatalf("frames=%d pix_fmt=%s", info.Frames, info.PixFmt)
			}
		})
	}
}
