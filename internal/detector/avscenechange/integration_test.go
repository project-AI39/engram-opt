package avscenechange

import (
	"context"
	"testing"

	"engram-opt/internal/testutil"
)

// 実バイナリ統合テスト: FFmpeg Y4Mパイプ → av-scenechange → []domain.Scene 変換。
//
// アサート方針（仕様照合）:
//   - memo.md「データ構造と管理基準」: フレームSSOT不変条件（先頭0・連続カバー・inclusive終端）
//   - 生成クリップのセグメント境界（60/120帧）は内容差が最大のため必ずカット検出される。
//     カット「位置」はこの仕様側の前提として固定し、検出器の細部挙動には依存しない。
func TestDetectIntegration(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe", "av-scenechange")
	ctx := context.Background()

	video := testutil.GenerateSampleVideo(t, t.TempDir())
	want := testutil.ProbeStreamInfo(t, ctx, video)
	if want.Frames != 180 {
		t.Fatalf("fixture spec mismatch: sample should be 180 frames, got %d", want.Frames)
	}

	scenes, err := New().Detect(ctx, video)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if len(scenes) < 3 {
		t.Fatalf("segment boundaries 60/120 should yield >=3 scenes, got %d: %+v", len(scenes), scenes)
	}

	// フレームSSOT不変条件
	if scenes[0].StartFrame != 0 {
		t.Fatalf("first scene must start at frame 0, got %d", scenes[0].StartFrame)
	}
	if last := scenes[len(scenes)-1].EndFrame; last != want.Frames-1 {
		t.Fatalf("last scene must end at %d, got %d", want.Frames-1, last)
	}
	var total int64
	for i, sc := range scenes {
		if sc.Index != i {
			t.Fatalf("scene[%d].Index = %d", i, sc.Index)
		}
		if err := sc.Validate(); err != nil {
			t.Fatalf("scene %+v invalid: %v", sc, err)
		}
		if i > 0 && scenes[i-1].EndFrame+1 != sc.StartFrame {
			t.Fatalf("scenes not contiguous: [%d].End=%d, [%d].Start=%d",
				i-1, scenes[i-1].EndFrame, i, sc.StartFrame)
		}
		total += sc.FrameCount()
	}
	if total != want.Frames {
		t.Fatalf("total coverage = %d, want %d", total, want.Frames)
	}

	// セグメント境界がカット点として現れていること
	starts := map[int64]bool{}
	for _, sc := range scenes {
		starts[sc.StartFrame] = true
	}
	for _, cut := range []int64{60, 120} {
		if !starts[cut] {
			t.Fatalf("hard cut at frame %d was not detected as a scene start: starts=%v", cut, starts)
		}
	}
}
