package libvmaf

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"engram-opt/internal/domain"
	ffenc "engram-opt/internal/encoder/ffmpeg"
	"engram-opt/internal/testutil"
)

// 実バイナリ統合テスト: 低解像度入力（320x240）でもCAMBI要件の1080pリサイズガードを
// 経由してスコアが取得できること。主力モデル vmaf_v1.0.16_3d0h で成功する。
func TestEvaluateIntegrationLowResInput(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe")
	ctx := context.Background()

	video := testutil.GenerateSampleVideo(t, t.TempDir())
	scene := domain.Scene{Index: 0, StartFrame: 0, EndFrame: 59}
	chunk := filepath.Join(t.TempDir(), "chunk.mkv")

	if err := ffenc.New().EncodeChunk(ctx, video, scene,
		domain.EncodeParams{Codec: domain.CodecH264, CRF: 18, Preset: "medium", BitDepth: 10}, chunk); err != nil {
		t.Fatalf("EncodeChunk failed: %v", err)
	}

	m, err := New().Evaluate(ctx, video, scene, chunk, t.TempDir(), domain.DefaultEvalProfile())
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if m.HarmonicMean <= 0 || m.HarmonicMean > 100 {
		t.Fatalf("harmonic_mean out of sane range: %.2f", m.HarmonicMean)
	}
	if m.Min > m.HarmonicMean || m.Mean < m.Min {
		t.Fatalf("inconsistent metrics: %+v", m)
	}
}

// 評価フレーム数の整合検証（fail-fast仕様）: チャンクが参照区間より短い場合、
// スコアを出さずにエラーになること。
func TestEvaluateIntegrationFrameMismatchDetection(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe")
	ctx := context.Background()

	video := testutil.GenerateSampleVideo(t, t.TempDir())

	// 30フレームのチャンクを用意し、60フレームのシーンとして評価させる
	shortScene := domain.Scene{Index: 0, StartFrame: 0, EndFrame: 29}
	chunk := filepath.Join(t.TempDir(), "short_chunk.mkv")
	if err := ffenc.New().EncodeChunk(ctx, video, shortScene,
		domain.EncodeParams{Codec: domain.CodecH264, CRF: 18, Preset: "medium", BitDepth: 10}, chunk); err != nil {
		t.Fatalf("EncodeChunk failed: %v", err)
	}

	longScene := domain.Scene{Index: 1, StartFrame: 0, EndFrame: 59}
	_, err := New().Evaluate(ctx, video, longScene, chunk, t.TempDir(), domain.DefaultEvalProfile())
	if err == nil {
		t.Fatal("frame count mismatch must be detected as an error (fail-fast spec)")
	}
	if !strings.Contains(err.Error(), "frame count mismatch") {
		t.Fatalf("error should mention frame count mismatch: %v", err)
	}
}
