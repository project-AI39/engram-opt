// Package ffmpeg は domain.VideoEncoder の FFmpeg 実装。
//
// フレーム完全一致の方針:
//   - チャンク抽出は select フィルタに整数フレーム番号を渡して行う
//     （-ss の浮動小数点秒指定は丸め誤差でVMAF評価を壊すため禁止）。
//   - select は先頭からデコードするため後方シーンほど前処理コストがかかるが、
//     正確性を優先する。高速化は必要になってから検討する。
//
// IDRキーフレーム: GOP長をシーン長と等しく設定し、中間IDRを抑制する
// （チャンク先頭のみIDRという固定仕様）。
package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"engram-opt/internal/domain"
	"engram-opt/internal/toolbin"
)

// Encoder は domain.VideoEncoder の実装。
type Encoder struct{}

// New は Encoder を生成する。
func New() *Encoder { return &Encoder{} }

// Name は実装名を返す。
func (e *Encoder) Name() string { return "ffmpeg" }

// EncodeChunk 元動画の特定シーン区間を指定パラメータでエンコードし outputPath へ出力する。
// 出力は常に10-bit（yuv420p10le）固定。
func (e *Encoder) EncodeChunk(ctx context.Context, inputPath string, scene domain.Scene, params domain.EncodeParams, outputPath string) error {
	if !toolbin.FileExists(inputPath) {
		return fmt.Errorf("input file not found: %s", inputPath)
	}
	if scene.FrameCount() <= 0 {
		return fmt.Errorf("invalid scene frame count: %d", scene.FrameCount())
	}
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		return err
	}

	gop := strconv.FormatInt(scene.FrameCount(), 10)
	crf := strconv.Itoa(params.CRF)

	// 共通引数: シーン区間のみを選択（整数フレーム番号）し、タイムスタンプを0起点へ正規化
	args := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-i", inputPath,
		"-vf", fmt.Sprintf("select='between(n,%d,%d)',setpts=PTS-STARTPTS", scene.StartFrame, scene.EndFrame),
		"-frames:v", gop,
		"-pix_fmt", "yuv420p10le",
		"-g", gop,
	}

	switch params.Codec {
	case domain.CodecH264:
		args = append(args, "-c:v", "libx264", "-preset", params.Preset, "-crf", crf,
			"-sc_threshold", "0") // シーンカット自動キーフレームを無効化（先頭IDRのみを保証）
	case domain.CodecHEVC:
		args = append(args, "-c:v", "libx265", "-preset", params.Preset, "-crf", crf,
			"-sc_threshold", "0")
	case domain.CodecAV1:
		args = append(args, "-c:v", "libsvtav1", "-preset", svtPreset(params.Preset), "-crf", crf)
	default:
		return fmt.Errorf("unsupported codec: %q", params.Codec)
	}
	args = append(args, "-an", outputPath)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("encode failed (codec=%s crf=%d): %w\n%s",
			params.Codec, params.CRF, err, tail(string(out), 20))
	}
	return nil
}

// ConcatChunks 確定済みチャンク列を concat demuxer + ストリームコピーで無劣化結合する。
func (e *Encoder) ConcatChunks(ctx context.Context, chunkPaths []string, finalOutputPath string) error {
	if len(chunkPaths) == 0 {
		return fmt.Errorf("no chunks to concatenate")
	}
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		return err
	}

	// リストファイルはユーザーの出力先を汚さないよう一時ディレクトリへ出す。
	// -safe 0 により絶対パス参照を許可している。
	tmpDir, err := os.MkdirTemp("", "engram-concat-")
	if err != nil {
		return fmt.Errorf("creating concat temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	listPath := filepath.Join(tmpDir, "list.txt")
	var b strings.Builder
	for _, p := range chunkPaths {
		b.WriteString(fmt.Sprintf("file '%s'\n", concatEscape(p)))
	}
	if err := os.WriteFile(listPath, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("writing concat list: %w", err)
	}

	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-f", "concat", "-safe", "0", "-i", listPath,
		"-c", "copy", finalOutputPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("concat failed: %w\n%s", err, tail(string(out), 20))
	}
	return nil
}

// svtPreset は x264/x265 流儀の preset 名を SVT-AV1 の数値プリセットへ変換する。
// SVT-AV1 は数値のみを受け付けるための解決層（値は一般的な対応表に基づく近似）。
func svtPreset(preset string) string {
	m := map[string]string{
		"veryslow": "1", "slower": "2", "slow": "4", "medium": "6",
		"fast": "8", "faster": "10", "veryfast": "12", "superfast": "13",
	}
	if v, ok := m[preset]; ok {
		return v
	}
	// 数値が直接来た場合はそのまま透過させる
	if _, err := strconv.Atoi(preset); err == nil {
		return preset
	}
	return "6" // 不明な場合は medium 相当へフォールバック
}

// concatEscape は concat リスト用にパス中のシングルクォートをエスケープする。
// ffmpegの引用ルールでは、引用終了 + バックスラッシュ付きクォート + 再開 の
// 4文字並びでリテラルのクォートを表現する。
func concatEscape(p string) string {
	return strings.ReplaceAll(p, "'", `'\''`)
}

// tail は文字列の末尾 maxLines 行のみを返す（エラーメッセージ用の切り詰め）。
func tail(s string, maxLines int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}
