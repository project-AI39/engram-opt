package cli

// バイナリレベルの起動フロー検証。decideLaunch の単体テストでは担保できない
// RunE最終配線（cobra→RunE→モード分岐→exit code）を、テスト内で実バイナリを
// go build して確認する。外部バイナリ（ffmpeg等）に依存しない経路のみ扱う。

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"engram-opt/internal/toolbin"
)

// buildTestBinary は検証用に本体をビルドし、出力exeのパスを返す。
// バージョン埋め込みも同時に検証できるよう -ldflags で vtest を注入する。
func buildTestBinary(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	root, err := toolbin.RepoRoot()
	if err != nil {
		t.Skipf("repository root not found: %v", err)
	}
	exe := filepath.Join(t.TempDir(), toolbin.ToolName("engram-opt"))
	cmd := exec.Command("go", "build", "-ldflags", "-X main.version=vtest", "-o", exe, "./cmd/engram-opt")
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building test binary: %v\n%s", err, toolbin.Tail(string(b), 15))
	}
	return exe
}

type runResult struct {
	stdout string
	stderr string
	code   int
}

func runBinary(t *testing.T, exe string, args ...string) runResult {
	t.Helper()
	cmd := exec.Command(exe, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	res := runResult{stdout: outBuf.String(), stderr: errBuf.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		res.code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s %v: %v", exe, args, err)
	}
	return res
}

// --version はZip名と同一形式のバージョンを1行で出す（ldflags注入の検証兼ねる）。
func TestBinaryVersionFlag(t *testing.T) {
	exe := buildTestBinary(t)
	res := runBinary(t, exe, "--version")
	if res.code != 0 {
		t.Fatalf("exit = %d, stderr: %s", res.code, res.stderr)
	}
	if got := strings.TrimSpace(res.stdout); got != "engram-opt vtest" {
		t.Fatalf("version output = %q, want %q", got, "engram-opt vtest")
	}
}

// 非対話環境での裸起動はヘルプ表示で正常終了する（launchHelp経路の最終配線）。
func TestBinaryBareLaunchOutsideTerminalShowsHelp(t *testing.T) {
	exe := buildTestBinary(t)
	res := runBinary(t, exe) // stdout/stderr をパイプへ => 非TTY確定
	if res.code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "Usage:") {
		t.Fatalf("help should be printed to stdout:\n%s", res.stdout)
	}
}

// 裸起動＋--headless は入力必須エラーで異常終了する（旧bareRunEバイパスの回帰防止）。
func TestBinaryBareHeadlessRequiresInput(t *testing.T) {
	exe := buildTestBinary(t)
	res := runBinary(t, exe, "--headless")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s", res.code, res.stdout)
	}
	if !strings.Contains(res.stderr, "--headless requires an input video path") {
		t.Fatalf("stderr missing expected error:\n%s", res.stderr)
	}
}

// --tui と --headless の同時指定は全起動経路で排他エラーになる。
func TestBinaryTuiHeadlessExclusive(t *testing.T) {
	exe := buildTestBinary(t)
	res := runBinary(t, exe, "--tui", "--headless")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s", res.code, res.stdout)
	}
	if !strings.Contains(res.stderr, "mutually exclusive") {
		t.Fatalf("stderr missing exclusivity error:\n%s", res.stderr)
	}
}
