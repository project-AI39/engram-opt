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
