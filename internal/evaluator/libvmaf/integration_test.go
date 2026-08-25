package libvmaf

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"engram-opt/internal/domain"
	ffenc "engram-opt/internal/encoder/ffmpeg"
	"engram-opt/internal/media"
	"engram-opt/internal/testutil"
	"engram-opt/internal/toolbin"
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

// genKeyedSource はキーフレーム位置を既知（-g 25）にした検証用ソースを生成する。
func genKeyedSource(t testing.TB, dir string, seconds int) string {
	t.Helper()
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg unavailable (%v)", err)
	}
	out := filepath.Join(dir, "keyed.mp4")
	cmd := exec.Command(ffmpegPath,
		"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=320x240:rate=30:duration=%d", seconds),
		"-c:v", "libx264", "-g", "25", "-crf", "18", "-pix_fmt", "yuv420p",
		out)
	if b, cerr := cmd.CombinedOutput(); cerr != nil {
		t.Fatalf("generating keyed source failed: %v\n%s", cerr, b)
	}
	return out
}

// TestEvaluateAnchoredSeekParity:
// 参照側のキーフレームアンカー事前シーク経路（現行 Evaluate）が、最適化前の
// フルデコード相当（offset=0 のグラフ＋シーク無し）と同一スコアを返すことを担保する。
// アンカーは同期点IDRに限られるため参照フレーム列は完全一致し、スコアも一致するはずである
// （memo.md §4.2/§8.1）。区間 [90..149]（直前キー75、残差15フレーム）で検証する。
func TestEvaluateAnchoredSeekParity(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe")
	ctx := context.Background()

	src := genKeyedSource(t, t.TempDir(), 5) // 150フレーム・キーは25間隔
	scene := domain.Scene{Index: 3, StartFrame: 90, EndFrame: 149}

	// 比較対象チャンク: 最適化前経路（フルデコードselect）で作る
	chunkDir := t.TempDir()
	chunk := filepath.Join(chunkDir, "chunk.mkv")
	if err := ffenc.New().EncodeChunk(ctx, src,
		domain.Scene{Index: 0, StartFrame: 0, EndFrame: 59}, // 60フレームを丸ごと（中身は[90..149]と同型の独立ファイルでよい）
		domain.EncodeParams{Codec: domain.CodecH264, CRF: 18, Preset: "medium", BitDepth: 10}, chunk); err != nil {
		t.Fatalf("preparing chunk failed: %v", err)
	}

	// 現行（アンカー有効）のスコア
	newDir := filepath.Join(t.TempDir(), "w")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gotNew, err := New().Evaluate(ctx, src, scene, chunk, newDir, domain.DefaultEvalProfile())
	if err != nil {
		t.Fatalf("Evaluate (anchored) failed: %v", err)
	}

	// 最適化前経路の再現: シーク無し・offset=0 グラフの手組みffmpeg実行
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg unavailable: %v", err)
	}
	ffprobePath, err := toolbin.Resolve("ffprobe")
	if err != nil {
		t.Fatalf("ffprobe unavailable: %v", err)
	}
	fpsNum, fpsDen, err := media.ProbeFrameRate(ctx, ffprobePath, src)
	if err != nil {
		t.Fatalf("ProbeFrameRate failed: %v", err)
	}
	legacyDir := filepath.Join(t.TempDir(), "w")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	graph := buildEvalGraph(scene, domain.DefaultEvalProfile(), fpsNum, fpsDen, 0)
	cmd2 := exec.Command(ffmpegPath,
		"-hide_banner", "-nostdin", "-loglevel", "error",
		"-i", chunk,
		"-i", src,
		"-filter_complex", graph,
		"-f", "null", "-")
	cmd2.Dir = legacyDir
	if b, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("legacy evaluation failed: %v\n%s", err, b)
	}
	raw, err := os.ReadFile(filepath.Join(legacyDir, logFileName))
	if err != nil {
		t.Fatalf("reading legacy vmaf log: %v", err)
	}
	gotOld, err := parseReport(raw, scene)
	if err != nil {
		t.Fatalf("parsing legacy vmaf log: %v", err)
	}

	if gotNew.HarmonicMean != gotOld.HarmonicMean {
		t.Fatalf("harmonic_mean mismatch: anchored=%.10f legacy=%.10f", gotNew.HarmonicMean, gotOld.HarmonicMean)
	}
	if gotNew.Mean != gotOld.Mean || gotNew.Min != gotOld.Min {
		t.Fatalf("metrics mismatch: anchored=%+v legacy=%+v", gotNew, gotOld)
	}
}
