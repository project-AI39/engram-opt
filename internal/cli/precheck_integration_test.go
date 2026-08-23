package cli

// precheck層（起動時事前チェック）の実機回帰テスト。
// 単体テストでは純粋関数のみを検証しており、ffprobe連動の挙動はここで担保する。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"engram-opt/internal/testutil"
	"engram-opt/internal/toolbin"
)

// TestTooShortInputRejectedIntegration は単一フレーム入力が
// 「探索を始めてからシーン検出で死ぬ」のではなく、起動時に分かりやすい文言で拒否されることを保証する。
func TestTooShortInputRejectedIntegration(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe")
	exe := buildTestBinary(t)
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg unavailable (%v)", err)
	}

	dir := t.TempDir()
	one := filepath.Join(dir, "oneframe.mp4")
	b, cerr := exec.Command(ffmpegPath, "-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=red:s=320x240:d=0.033:r=30",
		"-frames:v", "1", "-c:v", "libx264", "-crf", "30", "-pix_fmt", "yuv420p",
		one).CombinedOutput()
	if cerr != nil {
		t.Fatalf("generating single-frame fixture: %v\n%s", cerr, toolbin.Tail(string(b), 5))
	}

	res := runBinary(t, exe, one, "--out", filepath.Join(dir, "o.mkv"))
	if res.code == 0 {
		t.Fatal("single-frame input must be rejected")
	}
	if !strings.Contains(res.stderr, "too short") {
		t.Fatalf("stderr = %q, want too-short guidance", res.stderr)
	}
}

// TestCorruptInputShowsProbeDiagnosticsIntegration は偽装・破損ファイルが
// ffprobeのstderr要約付きで失敗すること（原因即判明契約）を保証する。
func TestCorruptInputShowsProbeDiagnosticsIntegration(t *testing.T) {
	testutil.RequireBinaries(t, "ffmpeg", "ffprobe")
	exe := buildTestBinary(t)

	dir := t.TempDir()
	fake := filepath.Join(dir, "fake.mp4")
	if err := os.WriteFile(fake, []byte("this is not a video"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := runBinary(t, exe, fake, "--out", filepath.Join(dir, "o.mkv"))
	if res.code == 0 {
		t.Fatal("corrupt input must be rejected")
	}
	if !strings.Contains(res.stderr, "ffprobe (dims)") {
		t.Fatalf("stderr = %q, want probe failure context", res.stderr)
	}
	if !(strings.Contains(res.stderr, "Invalid data") || strings.Contains(res.stderr, "moov atom")) {
		t.Fatalf("stderr = %q, want ffprobe diagnostic tail", res.stderr)
	}
}
