package setup

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// cargoSource は「配布バイナリが存在しないためローカルビルドするツール」の入手先を表す。
type cargoSource struct {
	url     string // ソースtarball（codeload.github.com のタグpin URL）
	sha256  string // tarballのSHA256
	rootDir string // アーカイブ内のトップディレクトリ
}

var sceneChangeSource = cargoSource{
	url:     "https://codeload.github.com/rust-av/av-scenechange/tar.gz/refs/tags/v0.24.1",
	sha256:  "e8a99d7be1741bafe634206599a249d6cf54d489958c6096a95fe6bc48f8f2f4",
	rootDir: "av-scenechange-0.24.1",
}

func installSceneChange(binDir string) error {
	exePath := filepath.Join(binDir, toolName("av-scenechange"))

	// 冪等性: 正常に動くバイナリが既にあれば再ビルドしない
	if fileExists(exePath) {
		log.Printf("[setup] av-scenechange is already installed; verifying...")
		if err := checkHelp(exePath); err == nil {
			log.Printf("[setup] OK (existing install)")
			return nil
		} else {
			log.Printf("[setup] existing install is broken (%v); rebuilding...", err)
		}
	}

	cargoPath, err := exec.LookPath("cargo")
	if err != nil {
		return fmt.Errorf("cargo not found: Rust toolchain is required to build av-scenechange (https://rustup.rs)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	tmpDir, err := os.MkdirTemp("", "engram-setup-asc-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, "av-scenechange-src.tar.gz")
	log.Printf("[setup] downloading av-scenechange source...")
	if err := downloadFile(ctx, sceneChangeSource.url, archivePath); err != nil {
		return err
	}
	if err := verifySHA256(archivePath, sceneChangeSource.sha256); err != nil {
		return err
	}

	srcRoot := filepath.Join(tmpDir, "src")
	log.Printf("[setup] extracting source...")
	if err := extractAll(ctx, archivePath, srcRoot); err != nil {
		return err
	}
	projectDir := filepath.Join(srcRoot, filepath.FromSlash(sceneChangeSource.rootDir))
	if !dirExists(projectDir) {
		return fmt.Errorf("directory %q not found in source archive", sceneChangeSource.rootDir)
	}

	log.Printf("[setup] building av-scenechange with cargo (this may take a few minutes)...")
	build := exec.CommandContext(ctx, cargoPath, "build", "--release")
	build.Dir = projectDir
	out, err := build.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cargo build failed: %w\n%s", err, tail(string(out), 20))
	}

	builtBin := filepath.Join(projectDir, "target", "release", toolName("av-scenechange"))
	if !fileExists(builtBin) {
		return fmt.Errorf("built binary not found: %s", builtBin)
	}
	if err := copyFile(builtBin, exePath); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(exePath, 0o755); err != nil {
			return err
		}
	}

	if err := checkHelp(exePath); err != nil {
		return err
	}
	log.Printf("[setup] av-scenechange runs OK")
	return nil
}

// checkHelp は av-scenechange の動作確認を行う。
// --version が未実装のため、--help の成否で検証する。
func checkHelp(exePath string) error {
	out, err := exec.Command(exePath, "--help").CombinedOutput()
	if err != nil {
		return fmt.Errorf("--help failed: %w (%s)", err, firstLine(string(out)))
	}
	if !strings.Contains(string(out), "Usage") && !strings.Contains(string(out), "USAGE") {
		return fmt.Errorf("unexpected --help output: %q", firstLine(string(out)))
	}
	return nil
}
