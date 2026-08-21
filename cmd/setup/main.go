// setup コマンド: 開発環境・CI共通の依存関係セットアップ。
//
// 実行方法（リポジトリルートから）:
//
//	go run ./cmd/setup/main.go
//
// やること:
//   - build/bin/ へ FFmpeg / ffprobe の配置（バージョンpin + SHA256検証付き）
//   - build/bin/ へ av-scenechange の配置（ソースtarball pin + cargo build）
//   - 配置後、各バイナリの動作確認と vmaf フィルタの存在確認
//
// シェルを一切経由せず net/http / os/exec / mholt/archives だけで動作するため、
// ローカルとCIで同一コマンドが使える（Dev-CI Parity）。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mholt/archives"
)

// ===== 依存関係のバージョンpin定義 =====

// ffmpegSource は1プラットフォーム分のFFmpeg静的ビルドの入手先を表す。
type ffmpegSource struct {
	url    string
	sha256 string // 公式 .sha256 ファイルの値（小文字）
}

// URLとハッシュは不変。新しいOS/archへ対応するときはエントリを追加するだけ。
var ffmpegSources = map[string]ffmpegSource{
	"windows/amd64": {
		url:    "https://www.gyan.dev/ffmpeg/builds/packages/ffmpeg-8.1.2-essentials_build.zip",
		sha256: "db580001caa24ac104c8cb856cd113a87b0a443f7bdf47d8c12b1d740584a2ec",
	},
}

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

// ===== エントリポイント =====

func main() {
	if err := run(); err != nil {
		log.Fatalf("[setup] error: %v", err)
	}
}

func run() error {
	root, err := findRepoRoot()
	if err != nil {
		return err
	}
	binDir := filepath.Join(root, "build", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", binDir, err)
	}

	if err := installFFmpeg(binDir); err != nil {
		return fmt.Errorf("ffmpeg: %w", err)
	}
	if err := installSceneChange(binDir); err != nil {
		return fmt.Errorf("av-scenechange: %w", err)
	}

	log.Printf("[setup] all dependencies are ready: %s", binDir)
	return nil
}

// ===== FFmpeg =====

func installFFmpeg(binDir string) error {
	key := runtime.GOOS + "/" + runtime.GOARCH
	src, ok := ffmpegSources[key]
	if !ok {
		return fmt.Errorf("unsupported platform %q (supported: %v)", key, keysOf(ffmpegSources))
	}
	log.Printf("[setup] platform: %s", key)

	ffmpegPath := filepath.Join(binDir, toolName("ffmpeg"))
	ffprobePath := filepath.Join(binDir, toolName("ffprobe"))

	// 冪等性: 正常に動くバイナリが既にあれば再ダウンロードしない
	if fileExists(ffmpegPath) && fileExists(ffprobePath) {
		log.Printf("[setup] FFmpeg is already installed; verifying...")
		if verr := verifyTools(binDir); verr == nil {
			log.Printf("[setup] OK (existing install)")
			return nil
		} else {
			log.Printf("[setup] existing install is broken (%v); reinstalling...", verr)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	tmpDir, err := os.MkdirTemp("", "engram-setup-ffmpeg-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, filepath.Base(src.url))
	log.Printf("[setup] downloading FFmpeg...")
	if err := downloadFile(ctx, src.url, archivePath); err != nil {
		return err
	}
	if err := verifySHA256(archivePath, src.sha256); err != nil {
		return err
	}

	log.Printf("[setup] extracting to build/bin/ ...")
	if err := extractFFmpegTools(ctx, archivePath, binDir); err != nil {
		return err
	}

	if err := verifyTools(binDir); err != nil {
		return err
	}
	log.Printf("[setup] done: %s", binDir)
	return nil
}

// extractFFmpegTools はアーカイブから ffmpeg.exe / ffprobe.exe のみを binDir へ取り出す。
// 出力先はこちらで固定するため、アーカイブ内パスに依存しない（Zip Slip耐性あり）。
func extractFFmpegTools(ctx context.Context, archivePath, binDir string) error {
	format, reader, closer, err := openArchive(ctx, archivePath)
	if err != nil {
		return err
	}
	defer closer()

	wanted := []struct {
		suffix string
		out    string
	}{
		// アーカイブ内部は常に '/' 区切り。gyan.dev のzipは bin/ 配下に配置される
		{"/bin/" + toolName("ffmpeg"), toolName("ffmpeg")},
		{"/bin/" + toolName("ffprobe"), toolName("ffprobe")},
	}
	found := make(map[string]bool)

	extractor, ok := format.(archives.Extractor)
	if !ok {
		return fmt.Errorf("format %T does not support extraction", format)
	}
	err = extractor.Extract(ctx, reader, func(_ context.Context, info archives.FileInfo) error {
		if info.IsDir() {
			return nil
		}
		for _, w := range wanted {
			if !strings.HasSuffix(info.NameInArchive, w.suffix) {
				continue
			}
			if err := copyArchiveEntry(info, filepath.Join(binDir, w.out)); err != nil {
				return err
			}
			found[w.out] = true
			log.Printf("[setup]   extracted %s", w.out)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, w := range wanted {
		if !found[w.out] {
			return fmt.Errorf("%s not found in archive %s", w.suffix, filepath.Base(archivePath))
		}
	}
	return nil
}

// verifyTools は配置済みFFmpegバイナリの動作確認を行う。
// 特に vmaf フィルタの存在確認は重要（libvmaf を含まない配布物の検出用）。
func verifyTools(binDir string) error {
	ffmpegPath := filepath.Join(binDir, toolName("ffmpeg"))
	ffprobePath := filepath.Join(binDir, toolName("ffprobe"))

	if err := checkVersion(ffmpegPath, "ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg: %w", err)
	}
	log.Printf("[setup] ffmpeg runs OK")
	if err := checkVersion(ffprobePath, "ffprobe"); err != nil {
		return fmt.Errorf("ffprobe: %w", err)
	}
	log.Printf("[setup] ffprobe runs OK")
	if err := checkVMAFFilter(ffmpegPath); err != nil {
		return err
	}
	log.Printf("[setup] vmaf filter found in ffmpeg")
	return nil
}

// ===== av-scenechange =====

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

// ===== 共通ユーティリティ =====

// findRepoRoot は cwd から親ディレクトリを辿り、go.mod のある場所（=リポジトリルート）を返す。
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repository root not found; run from inside the repo (go.mod not found)")
		}
		dir = parent
	}
}

// downloadFile は url を dest へ保存する。curl/wget等を使わず net/http で直接取得し、
// シェル差異を排除する。進捗は20MBごとにログ出力する。
func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "engram-opt-setup")

	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w (%s)", err, url)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s (%s)", resp.Status, url)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	const chunk = 1 << 20         // 1 MiB
	const progressUnit = 20 << 20 // 20 MiB ごとに進捗表示
	var total int64
	buf := make([]byte, chunk)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			prev := total
			total += int64(n)
			if prev/progressUnit != total/progressUnit {
				log.Printf("[setup]   %.0f MB downloaded...", float64(total)/(1<<20))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	log.Printf("[setup]   %.1f MB downloaded", float64(total)/(1<<20))
	return f.Sync()
}

// verifySHA256 はファイルのSHA256が期待値と一致することを確認する。
func verifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != strings.ToLower(want) {
		return fmt.Errorf("sha256 mismatch for %s:\n  got:  %s\n  want: %s", filepath.Base(path), got, want)
	}
	log.Printf("[setup] sha256 OK")
	return nil
}

// openArchive はアーカイブを開いて形式を自動判定し、抽出用リーダーを返す。
func openArchive(ctx context.Context, archivePath string) (archives.Format, io.Reader, func(), error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, nil, nil, err
	}
	format, reader, err := archives.Identify(ctx, filepath.Base(archivePath), f)
	if err != nil {
		f.Close()
		return nil, nil, nil, fmt.Errorf("identifying archive format: %w", err)
	}
	return format, reader, func() { f.Close() }, nil
}

// extractAll はアーカイブ全体を destDir へ展開する。
// アーカイブ内パスを正規化し、destDir の外へ書き込まれないよう防ぐ（Zip Slip対策）。
func extractAll(ctx context.Context, archivePath, destDir string) error {
	format, reader, closer, err := openArchive(ctx, archivePath)
	if err != nil {
		return err
	}
	defer closer()

	extractor, ok := format.(archives.Extractor)
	if !ok {
		return fmt.Errorf("format %T does not support extraction", format)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	return extractor.Extract(ctx, reader, func(_ context.Context, info archives.FileInfo) error {
		// アーカイブ内部は '/' 区切りなので先に正規化してから安全確認する
		name := path.Clean(strings.TrimPrefix(info.NameInArchive, "/"))
		if name == "." || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("unsafe path in archive: %q", info.NameInArchive)
		}
		target := filepath.Join(destDir, filepath.FromSlash(name))
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyArchiveEntry(info, target)
	})
}

// copyArchiveEntry はアーカイブ内エントリの中身を destPath へ書き込む。
func copyArchiveEntry(info archives.FileInfo, destPath string) error {
	src, err := info.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(destPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	// Unix系では実行権限を付与（Windowsでは不要）
	if runtime.GOOS != "windows" {
		return os.Chmod(destPath, 0o755)
	}
	return nil
}

// copyFile はファイルをコピーする（Unix系では実行権限を付与）。
func copyFile(srcPath, destPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(destPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return os.Chmod(destPath, 0o755)
	}
	return nil
}

func checkVersion(exePath, expectPrefix string) error {
	out, err := exec.Command(exePath, "-hide_banner", "-version").Output()
	if err != nil {
		return fmt.Errorf("-version failed: %w", err)
	}
	if !strings.Contains(string(out), expectPrefix+" version") {
		return fmt.Errorf("unexpected output: %q", firstLine(string(out)))
	}
	return nil
}

func checkVMAFFilter(ffmpegPath string) error {
	out, err := exec.Command(ffmpegPath, "-hide_banner", "-filters").Output()
	if err != nil {
		return fmt.Errorf("-filters failed: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		// filters 一覧の形式: " <flags> <name> <io> <description>"
		// フィルタ名は FFmpeg 8系で vmaf → libvmaf に改名されたため両方を受け入れる
		if len(fields) >= 2 && (fields[1] == "libvmaf" || fields[1] == "vmaf") {
			return nil
		}
	}
	return fmt.Errorf("vmaf filter not found; this build may lack libvmaf")
}

// toolName はホストOSに応じた実行バイナリ名を返す（Windowsは .exe 付き）。
func toolName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func tail(s string, maxLines int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func keysOf[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
