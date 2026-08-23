package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"engram-opt/internal/toolbin"
)

// runFFProbe は ffprobe を実行し stdout を返す。失敗時はstderr末尾を添えて
// 原因を即座に判明させる（偽装ファイル・未対応codec等の診断に必要）。
// 成功時のstderrは無視する（警告が混入してもstdout解析を汚さないため分離取得）。
func runFFProbe(ctx context.Context, label string, args ...string) ([]byte, error) {
	ffprobePath, err := toolbin.Resolve("ffprobe")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, ffprobePath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe (%s) failed: %w\n%s", label, err, toolbin.Tail(stderr.String(), 5))
	}
	return out, nil
}

// ProbeVideoDims は映像ストリームの解像度を返す。
// --out-res 未指定時に「入力動画と同じ解像度」を具体値へ解決するために使う。
func ProbeVideoDims(ctx context.Context, inputPath string) (width, height int, err error) {

	out, err := runFFProbe(ctx, "dims",
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=p=0", inputPath)
	if err != nil {
		return 0, 0, err
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

// ProbeDurationSeconds は入力動画の再生時間（秒）と既知性を返す。
// formatメタデータから取るためフレーム数カウントのような全デコードは発生しない。
// 極端に短い入力（単一フレーム等）を探索開始前に拒否するための事前チェック用。
// エレメンタリストリーム等 duration を持たない入力では ok=false を返す
// （メタデータ欠落は入力の不正ではないため、呼び出し側はチェックをスキップする）。
func ProbeDurationSeconds(ctx context.Context, inputPath string) (seconds float64, ok bool) {

	out, err := runFFProbe(ctx, "duration",
		"-v", "error", "-show_entries", "format=duration",
		"-of", "csv=p=0", inputPath)
	if err != nil {
		return 0, false
	}
	d, perr := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if perr != nil || d < 0 {
		return 0, false // "N/A" 等。後段の正式な処理に判断を委ねる
	}
	return d, true
}

// ProbeStreamNotes は「黙って落とす」可能性のある入力構成を検出して注意文を返す。
// 対象: 複数音声トラック（先頭1本のみ使用）と字幕ストリーム（出力に含めない）。
// 情報提供が目的のため、取得失敗時は空スライスを返す（後段の正式なprobeがエラーを出す）。
func ProbeStreamNotes(ctx context.Context, inputPath string) []string {

	out, err := runFFProbe(ctx, "streams",
		"-v", "error", "-show_entries", "stream=codec_type",
		"-of", "csv=p=0", inputPath)
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
