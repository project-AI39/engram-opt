package ffmpeg

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"engram-opt/internal/toolbin"
)

// ProbeVideoDims は映像ストリームの解像度を返す。
// --out-res 未指定時に「入力動画と同じ解像度」を具体値へ解決するために使う。
func ProbeVideoDims(ctx context.Context, inputPath string) (width, height int, err error) {
	ffprobePath, err := toolbin.Resolve("ffprobe")
	if err != nil {
		return 0, 0, err
	}
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=p=0", inputPath).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe (dims) failed: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("unexpected ffprobe output: %q", strings.TrimSpace(string(out)))
	}
	w, werr := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, herr := strconv.Atoi(strings.TrimSpace(parts[1]))
	if werr != nil || herr != nil {
		return 0, 0, fmt.Errorf("invalid dimensions in ffprobe output: %q", strings.TrimSpace(string(out)))
	}
	return w, h, nil
}
