package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"engram-opt/internal/domain"
)

// fakeMuxer は MuxAudio 呼び出しを記録し、出力ファイルを作るだけの実装。
type fakeMuxer struct {
	calls []muxCall
}

type muxCall struct {
	videoPath    string
	originalPath string
	mode         domain.AudioMode
	outputPath   string
}

func (f *fakeMuxer) MuxAudio(_ context.Context, videoPath, originalPath string, mode domain.AudioMode, outputPath string) error {
	f.calls = append(f.calls, muxCall{videoPath, originalPath, mode, outputPath})
	return os.WriteFile(outputPath, []byte("muxed"), 0o644)
}

// 音声モード指定時: concatはjobDir内の中間ファイルへ行われ、最後にmuxが呼ばれる。
func TestOrchestratorAudioMuxFlow(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "job")
	output := filepath.Join(t.TempDir(), "final.mkv")

	enc := &fakeEncoder{}
	muxer := &fakeMuxer{}
	orch := &Orchestrator{
		Detector:  &fakeDetector{scenes: twoScenes()},
		Encoder:   enc,
		Evaluator: &fakeEvaluator{scoreAt: func(int) float64 { return 100 }},
		Muxer:     muxer,
		Audio:     domain.AudioOpus,
	}
	cfg := domain.SearchConfig{Codec: domain.CodecH264, MinCRF: 15, MaxCRF: 36, TargetScore: 90, Preset: "medium"}

	if _, err := orch.Run(context.Background(), "in.mp4", output, workDir, cfg, ProgressCallbacks{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// concat先は中間ファイル（ユーザー出力先を汚さず、成功時jobDirごと消える）
	if len(enc.concatTargets) != 1 {
		t.Fatalf("concat calls = %d", len(enc.concatTargets))
	}
	intermediate := filepath.Join(workDir, "concat_video.mkv")
	if enc.concatTargets[0] != intermediate {
		t.Fatalf("concat target = %q, want %q", enc.concatTargets[0], intermediate)
	}

	// muxは1回・引数は（中間映像, 元入力, モード, ステージング出力）。
	// 最終確定はステージング→renameで行われるため、muxの書き先は
	// 出力と同一ディレクトリの .part-<pid> 付き隠しファイルになる。
	if len(muxer.calls) != 1 {
		t.Fatalf("mux calls = %d", len(muxer.calls))
	}
	c := muxer.calls[0]
	wantStaged := filepath.Join(filepath.Dir(output), ".final.part-")
	if c.videoPath != intermediate || c.originalPath != "in.mp4" ||
		c.mode != domain.AudioOpus || !strings.HasPrefix(c.outputPath, wantStaged) {
		t.Fatalf("mux call = %+v (want staged prefix %q)", c, wantStaged)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("final output missing: %v", err)
	}
	// 成功時は中間ファイルごと一時領域が破棄される
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Fatalf("workDir should be removed on success: %v", err)
	}
}

// 音声なしモードではmuxを呼ばず、concatが直接出力を生成する（従来動作）。
func TestOrchestratorNoAudioSkipsMux(t *testing.T) {
	for _, mode := range []domain.AudioMode{"", domain.AudioNone} {
		workDir := filepath.Join(t.TempDir(), "job")
		output := filepath.Join(t.TempDir(), "final.mkv")
		muxer := &fakeMuxer{}
		orch := &Orchestrator{
			Detector:  &fakeDetector{scenes: twoScenes()},
			Encoder:   &fakeEncoder{},
			Evaluator: &fakeEvaluator{scoreAt: func(int) float64 { return 100 }},
			Muxer:     muxer,
			Audio:     mode,
		}
		cfg := domain.SearchConfig{Codec: domain.CodecH264, MinCRF: 15, MaxCRF: 36, TargetScore: 90, Preset: "medium"}

		if _, err := orch.Run(context.Background(), "in.mp4", output, workDir, cfg, ProgressCallbacks{}); err != nil {
			t.Fatalf("mode=%q: unexpected error: %v", mode, err)
		}
		if len(muxer.calls) != 0 {
			t.Fatalf("mode=%q: mux should not be called", mode)
		}
	}
}

// 音声モードが設定されているのにMuxer未接続は設定ミス。実行終盤ではなく即座に失敗させる。
func TestOrchestratorAudioModeRequiresMuxer(t *testing.T) {
	orch := &Orchestrator{
		Detector:  &fakeDetector{scenes: twoScenes()},
		Encoder:   &fakeEncoder{},
		Evaluator: &fakeEvaluator{scoreAt: func(int) float64 { return 100 }},
		Audio:     domain.AudioCopy, // Muxer nil
	}
	cfg := domain.SearchConfig{Codec: domain.CodecH264, MinCRF: 15, MaxCRF: 36, TargetScore: 90, Preset: "medium"}
	_, err := orch.Run(context.Background(), "in.mp4", filepath.Join(t.TempDir(), "o.mkv"),
		filepath.Join(t.TempDir(), "j"), cfg, ProgressCallbacks{})
	if err == nil || !strings.Contains(err.Error(), "AudioMuxer") {
		t.Fatalf("expected AudioMuxer config error, got %v", err)
	}
}

// 未対応の音声モード文字列も即座に失敗する。
func TestOrchestratorInvalidAudioMode(t *testing.T) {
	orch := &Orchestrator{
		Detector:  &fakeDetector{scenes: twoScenes()},
		Encoder:   &fakeEncoder{},
		Evaluator: &fakeEvaluator{scoreAt: func(int) float64 { return 100 }},
		Audio:     domain.AudioMode("mp3"),
		Muxer:     &fakeMuxer{},
	}
	cfg := domain.SearchConfig{Codec: domain.CodecH264, MinCRF: 15, MaxCRF: 36, TargetScore: 90, Preset: "medium"}
	_, err := orch.Run(context.Background(), "in.mp4", filepath.Join(t.TempDir(), "o.mkv"),
		filepath.Join(t.TempDir(), "j"), cfg, ProgressCallbacks{})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("invalid audio mode %q", "mp3")) {
		t.Fatalf("expected invalid audio mode error, got %v", err)
	}
}
