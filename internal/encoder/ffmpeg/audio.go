package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"engram-opt/internal/domain"
	"engram-opt/internal/toolbin"
)

// AudioStream は元動画の先頭音声ストリームの要約。
// Channels == 0 は音声ストリームが存在しないことを意味する。
type AudioStream struct {
	CodecName string // 例: aac / ac3 / flac（copyモードでそのまま引き継がれる）
	Channels  int
}

// MuxAudio 完成映像（videoPath・音声なし）へ元動画の音声を付与して outputPath へ書き出す。
//
// 設計（memo.md「音声処理」）: 音声はシーン分割の対象外。ここで初めて1回だけ処理し、
// 映像は常にストリームコピー（-c:v copy）するため再エンコードは発生しない。
//   - copy : 元音声をそのままコピー（無劣化）
//   - opus/aac: チャンネル数から自動判定したビットレートで再圧縮
//   - 元動画に音声が無い場合: 全モード共通で映像のみを出力へ書き出す
func (e *Encoder) MuxAudio(ctx context.Context, videoPath string, originalPath string, mode domain.AudioMode, outputPath string) error {
	switch mode {
	case domain.AudioCopy, domain.AudioOpus, domain.AudioAAC:
		// OK
	default:
		return fmt.Errorf("audio muxing does not support mode %q (only copy/opus/aac)", mode)
	}

	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		return err
	}
	ffprobePath, err := toolbin.Resolve("ffprobe")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	audio, err := probeFirstAudioStream(ctx, ffprobePath, originalPath)
	if err != nil {
		return err
	}

	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y"}
	if audio.Channels == 0 {
		// 音声なし入力: 映像のみをリマックスして完了（全モード共通の挙動）
		args = append(args, "-i", videoPath, "-map", "0:v:0", "-c", "copy", outputPath)
	} else {
		args = append(args,
			"-i", videoPath, "-i", originalPath,
			"-map", "0:v:0", "-map", "1:a:0",
			"-c:v", "copy",
		)
		switch mode {
		case domain.AudioCopy:
			args = append(args, "-c:a", "copy")
		case domain.AudioOpus:
			args = append(args, "-c:a", "libopus", "-b:a",
				strconv.Itoa(domain.TargetBitrateKbps(audio.Channels, mode))+"k")
		case domain.AudioAAC:
			args = append(args, "-c:a", "aac", "-b:a",
				strconv.Itoa(domain.TargetBitrateKbps(audio.Channels, mode))+"k")
		}
		args = append(args, outputPath)
	}

	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("audio mux failed (mode=%s): %w\n%s", mode, err, tail(string(out), 20))
	}
	return nil
}

// probeFirstAudioStream は元動画の先頭音声ストリーム情報を取得する。
// ストリームが無い場合は Channels==0 を返す（エラーにしない）。
func probeFirstAudioStream(ctx context.Context, ffprobePath, inputPath string) (AudioStream, error) {
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=codec_name,channels",
		"-of", "json", inputPath).Output()
	if err != nil {
		return AudioStream{}, fmt.Errorf("probing audio stream: %w", err)
	}
	var parsed struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
			Channels  int    `json:"channels"`
		} `json:"streams"`
	}
	if uerr := json.Unmarshal(out, &parsed); uerr != nil {
		return AudioStream{}, fmt.Errorf("parsing audio probe output: %w", uerr)
	}
	if len(parsed.Streams) == 0 {
		return AudioStream{}, nil
	}
	s := parsed.Streams[0]
	return AudioStream{CodecName: s.CodecName, Channels: s.Channels}, nil
}
