package toolbin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// リポジトリ内実行ではルート（go.modの位置）が解決できる。
func TestRepoRootInsideRepository(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !FileExists(filepath.Join(root, "go.mod")) {
		t.Fatalf("repo root %s has no go.mod", root)
	}
}

// go.mod のない場所ではエラーになる（t.Chdir によりテスト後は元cwdへ復帰）。
func TestRepoRootOutsideErrors(t *testing.T) {
	outside := t.TempDir()
	nested := filepath.Join(outside, "deep", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	if _, err := RepoRoot(); err == nil {
		t.Fatal("expected error outside a repository")
	}
}

// ToolName はWindowsでのみ .exe を付ける（配布バイナリ名規約）。
func TestToolName(t *testing.T) {
	got := ToolName("ffmpeg")
	want := "ffmpeg"
	if runtime.GOOS == "windows" {
		want = "ffmpeg.exe"
	}
	if got != want {
		t.Fatalf("ToolName = %q, want %q", got, want)
	}
}
