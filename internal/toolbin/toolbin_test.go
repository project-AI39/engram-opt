package toolbin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// stageDeployed は配布レイアウトを再現する: <base>/bin/ にffmpegのダミー実体を置く。
// 判定は存在確認のみなので中身は不要。OS一時領域配下でも配布扱いになること自体が
// 今回の設計要件（Zip解凍先は任意パス）である。
func stageDeployed(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	dummy := filepath.Join(bin, ToolName("ffmpeg"))
	if err := os.WriteFile(dummy, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	return base
}

// 本体の隣に bin/ がある → 配置済みレイアウトとしてその階層が base になる。
func TestDetectLayoutDeployed(t *testing.T) {
	base := stageDeployed(t)
	exe := filepath.Join(base, ToolName("engram-opt"))

	lay, err := detectLayoutFor(exe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lay.Dev {
		t.Fatal("layout should not be Dev when bin/ sits next to the executable")
	}
	if lay.Base != base {
		t.Fatalf("Base = %q, want %q", lay.Base, base)
	}
}

// 本体の隣に bin/ がない（go run / go test 相当）→ 開発レイアウトへフォールバックし、
// リポジトリルートの build/ が base になる。
func TestDetectLayoutDevFallback(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "engram-test-binary") // bin/ 無しの場所

	lay, err := detectLayoutFor(exe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !lay.Dev {
		t.Fatal("layout should be Dev without bundled tools next to the executable")
	}
	if want := filepath.Join(mustRepoRoot(t), "build"); lay.Base != want {
		t.Fatalf("Base = %q, want %q", lay.Base, want)
	}
}

// どちらの手がかりもない環境では、対処法（setup）を案内するエラーになる。
func TestDetectLayoutUnresolvable(t *testing.T) {
	outside := t.TempDir()
	exe := filepath.Join(outside, ToolName("engram-opt"))
	t.Chdir(outside) // go.mod 探索も失敗させる

	if _, err := detectLayoutFor(exe); err == nil || !strings.Contains(err.Error(), "setup") {
		t.Fatalf("expected setup-guiding error, got %v", err)
	}
}

// 配布レイアウトでは本体隣の bin/ から解決される。
func TestResolveDeployed(t *testing.T) {
	base := stageDeployed(t)
	dummyProbe := filepath.Join(base, "bin", ToolName("ffprobe"))
	if err := os.WriteFile(dummyProbe, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(base, ToolName("engram-opt"))

	got, err := resolveFor(exe, "ffprobe")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dummyProbe {
		t.Fatalf("Resolve = %q, want %q", got, dummyProbe)
	}
}

// 解決対象が無い場合は場所と対処法を示すエラーになる。
func TestResolveMissingGuidesSetup(t *testing.T) {
	base := stageDeployed(t) // ffmpeg はあるが ffprobe は無い状態
	exe := filepath.Join(base, ToolName("engram-opt"))

	_, err := resolveFor(exe, "ffprobe")
	if err == nil || !strings.Contains(err.Error(), "setup") {
		t.Fatalf("expected setup-guiding error, got %v", err)
	}
}

// TempRoot はレイアウトの base 直下の tmp/ を返す。
func TestTempRootFollowsLayout(t *testing.T) {
	// 配置済み
	base := stageDeployed(t)
	got, err := tempRootFor(filepath.Join(base, ToolName("engram-opt")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(base, "tmp"); got != want {
		t.Fatalf("deployed TempRoot = %q, want %q", got, want)
	}

	// 開発
	got, err = tempRootFor(filepath.Join(t.TempDir(), "engram-test-binary"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(mustRepoRoot(t), "build", "tmp"); got != want {
		t.Fatalf("dev TempRoot = %q, want %q", got, want)
	}
}

// TempRoot の基点ディレクトリは存在しなくても自動作成される。
// os.MkdirTemp は親を作らないため、新規クローン直後（build/tmpが無い状態）で
// concat等が失敗した実機不具合の回帰防止（2026-08 CI初回実行で発見）。
func TestTempRootCreatesMissingBase(t *testing.T) {
	base := stageDeployed(t) // bin/ のみ作成され tmp/ は無い
	root := filepath.Join(base, "tmp")
	if FileExists(root) {
		t.Fatalf("precondition: %s should not exist", root)
	}

	exe := filepath.Join(base, ToolName("engram-opt"))
	got, err := tempRootFor(exe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != root {
		t.Fatalf("TempRoot = %q, want %q", got, root)
	}
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		t.Fatalf("temp root was not created: %v", err)
	}
}

func mustRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}
