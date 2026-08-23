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

// ProbeDurationSeconds は入力動画の再生時間（秒）を返す。
// formatメタデータから取るためフレーム数カウントのような全デコードは発生しない。
// 極端に短い入力（単一フレーム等）を探索開始前に拒否するための事前チェック用。
func ProbeDurationSeconds(ctx context.Context, inputPath string) (float64, error) {
	ffprobePath, err := toolbin.Resolve("ffprobe")
	if err != nil {
		return 0, err
	}
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-show_entries", "format=duration",
		"-of", "csv=p=0", inputPath).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe (duration) failed: %w", err)
	}
	d, perr := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if perr != nil || d < 0 {
		return 0, fmt.Errorf("invalid duration in ffprobe output: %q", strings.TrimSpace(string(out)))
	}
	return d, nil
}

// ProbeStreamNotes は「黙って落とす」可能性のある入力構成を検出して注意文を返す。
// 対象: 複数音声トラック（先頭1本のみ使用）と字幕ストリーム（出力に含めない）。
// 情報提供が目的のため、取得失敗時は空スライスを返す（後段の正式なprobeがエラーを出す）。
func ProbeStreamNotes(ctx context.Context, inputPath string) []string {
	ffprobePath, err := toolbin.Resolve("ffprobe")
	if err != nil {
		return nil
	}
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-show_entries", "stream=codec_type",
		"-of", "csv=p=0", inputPath).Output()
	if err != nil {
		return nil
	}
	var audioCount, subtitleCount int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		switch strings.TrimSpace(line) {
		case "audio":
			audioCount++
		case "subtitle":
			subtitleCount++
		}
	}
	var notes []string
	if audioCount > 1 {
		notes = append(notes, fmt.Sprintf("input has %d audio streams; only the first is used (others are dropped)", audioCount))
	}
	if subtitleCount > 0 {
		notes = append(notes, fmt.Sprintf("input has %d subtitle stream(s); they are dropped in the output", subtitleCount))
	}
	return notes
}
