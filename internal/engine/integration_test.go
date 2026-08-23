package engine

// 実バイナリによるキャンセル経路の統合検証。
// cancel_test.go（フェイク）が二分探索ループの中断ロジックを担保するのに対し、
// 本テストは実ffmpeg / av-scenechange を起動した状態でctxキャンセルした際に、
// exec.CommandContext 経由の子プロセス停止がパイプライン全体で機能すること
// （孤児プロセスが出ないこと）を確認する。AGENTS.md テスト方針の「統合テスト」枠。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
	deadline := time.Now().Add(20 * time.Second)
	for {
		n := orphanToolProcessCount(t, dir)
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d orphan child process(es) remain after cancellation", n)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// orphanToolProcessCount は、marker（本テスト専用の一時ディレクトリパス）を
// コマンドラインに含む同梱外部バイナリの残留プロセス数を数える。
// イメージ名でのシステム全体カウントは、go test のパッケージ並列実行において
// 他パッケージのテストが起こす正当なffmpegまで数えて誤検出するため
// （2026-08 CI初回実行で発見）、起動元パイプラインへの帰属を
// コマンドラインで判定する。
func orphanToolProcessCount(t testing.TB, marker string) int {
	t.Helper()
	wql := "Name='ffmpeg.exe' OR Name='ffprobe.exe' OR Name='av-scenechange.exe'"
	script := fmt.Sprintf(
		"(Get-CimInstance Win32_Process -Filter %q | "+
			"Where-Object { $_.CommandLine -like '*%s*' } | Measure-Object).Count",
		wql, marker)
	out, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
	if err != nil {
		t.Fatalf("process query failed: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("unexpected process query output: %q", string(out))
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
