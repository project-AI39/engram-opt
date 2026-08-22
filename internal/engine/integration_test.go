package engine

// 実バイナリによるキャンセル経路の統合検証。
// cancel_test.go（フェイク）が二分探索ループの中断ロジックを担保するのに対し、
// 本テストは実ffmpeg / av-scenechange を起動した状態でctxキャンセルした際に、
// exec.CommandContext 経由の子プロセス停止がパイプライン全体で機能すること
// （孤児プロセスが出ないこと）を確認する。AGENTS.md テスト方針の「統合テスト」枠。

import (
	"context"
	"errors"
	"os"
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
	"engram-opt/internal/toolbin"
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

// 相対パス指定の入出力でも完走する（実動画検証で発見した回帰の恒久化）。
// libvmaf評価は子ffmpegを cmd.Dir=評価作業領域 で起動するため、engine境界で
// 絶対パスへ正規化していないと「入力が開けない」失敗をしていた。
// 注意: プロセスCWDは変更しない（toolbinのリポジトリ検出がCWD依存のため）。
// リポジトリ内 build/ 配下（.gitignore済み）に相対パスで触れるファイルを置く。
func TestOrchestratorRelativePathsIntegration(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe", "av-scenechange")

	root, err := toolbin.RepoRoot()
	if err != nil {
		t.Skipf("repository root not found: %v", err)
	}
	dir := filepath.Join(root, "build", "relpathtest")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	videoAbs := testutil.GenerateSampleVideo(t, dir)
	outAbs := filepath.Join(dir, "out.mkv")
	// 相対パスはプロセスCWD（go testではパッケージdir）基準で解決される
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	videoRel, err := filepath.Rel(cwd, videoAbs)
	if err != nil {
		t.Fatal(err)
	}
	outRel, err := filepath.Rel(cwd, outAbs)
	if err != nil {
		t.Fatal(err)
	}

	orch := &Orchestrator{
		Detector:  avscenechange.New(),
		Encoder:   ffenc.New(),
		Evaluator: libvmaf.New(),
	}
	cfg := domain.SearchConfig{Codec: domain.CodecH264, MinCRF: 15, MaxCRF: 36, TargetScore: 90, Preset: "medium"}

	jobAbs := filepath.Join(dir, "job")
	rep, runErr := orch.Run(context.Background(), videoRel, outRel, jobAbs, cfg, ProgressCallbacks{})
	if runErr != nil {
		t.Fatalf("run with relative paths: %v", runErr)
	}
	if len(rep.Results) == 0 {
		t.Fatal("no scene results")
	}
	if _, serr := os.Stat(filepath.Join(cwd, outRel)); serr != nil {
		t.Fatalf("relative output missing: %v", serr)
	}
}
