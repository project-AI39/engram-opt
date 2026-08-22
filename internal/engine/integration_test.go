package engine

// 実バイナリによるキャンセル経路の統合検証。
// cancel_test.go（フェイク）が二分探索ループの中断ロジックを担保するのに対し、
// 本テストは実ffmpeg / av-scenechange を起動した状態でctxキャンセルした際に、
// exec.CommandContext 経由の子プロセス停止がパイプライン全体で機能すること
// （孤児プロセスが出ないこと）を確認する。AGENTS.md テスト方針の「統合テスト」枠。

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"engram-opt/internal/detector/avscenechange"
	"engram-opt/internal/domain"
	ffenc "engram-opt/internal/encoder/ffmpeg"
	"engram-opt/internal/evaluator/libvmaf"
	"engram-opt/internal/testutil"
)

func TestOrchestratorCancelTerminatesChildren(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("orphan-process assertion relies on tasklist (windows-only)")
	}
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe", "av-scenechange")

	dir := t.TempDir()
	video := testutil.GenerateSampleVideo(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	canceled := false
	orch := &Orchestrator{
		Detector:  avscenechange.New(),
		Encoder:   ffenc.New(),
		Evaluator: libvmaf.New(),
	}
	cfg := domain.SearchConfig{Codec: domain.CodecH264, MinCRF: 15, MaxCRF: 36, TargetScore: 90, Preset: "medium"}

	// 最初の試行開始と同時にキャンセルする。逐次処理のため、この時点では
	// シーン0のEncodeChunk用ffmpegが稼働中のはず（kill対象の確実化）。
	_, runErr := orch.Run(ctx, video, filepath.Join(dir, "out.mkv"),
		filepath.Join(dir, "job"), cfg, ProgressCallbacks{
			OnTrial: func(Trial) {
				if !canceled {
					canceled = true
					cancel()
				}
			},
		})

	if runErr == nil {
		t.Fatal("pipeline completed despite cancellation")
	}
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled chain", runErr)
	}

	// 子プロセス残留のポーリング確認（即時にはSIGKILL伝播を待つ必要があるため）。
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := orphanToolProcessCount(t)
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d orphan child process(es) remain after cancellation", n)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// orphanToolProcessCount は同梱外部バイナリ（ffmpeg / av-scenechange）の
// 残留プロセス数を数える。tasklist のCSV出力にイメージ名が含まれるかどうかで判定する。
func orphanToolProcessCount(t testing.TB) int {
	t.Helper()
	out, err := exec.Command("tasklist", "/FO", "CSV", "/NH").Output()
	if err != nil {
		t.Fatalf("tasklist failed: %v", err)
	}
	n := 0
	for _, name := range []string{"ffmpeg.exe", "av-scenechange.exe"} {
		if strings.Contains(string(out), `"`+name+`"`) {
			n++
		}
	}
	return n
}
