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

// audioStream は元動画の先頭音声ストリームの要約。
// Channels == 0 は音声ストリームが存在しないことを意味する。
type audioStream struct {
	Channels int
}

// audioCodecArgs は再圧縮モードごとの ffmpeg エンコーダ指定。
var audioCodecArgs = map[domain.AudioMode]string{
	domain.AudioOpus: "libopus",
	domain.AudioAAC:  "aac",
}

// MuxAudio 完成映像（videoPath・音声なし）へ元動画の音声を付与して outputPath へ書き出す。
//
// 設計（memo.md「音声処理」）: 音声はシーン分割の対象外。ここで初めて1回だけ処理し、
// 映像は常にストリームコピー（-c:v copy）するため再エンコードは発生しない。
//   - copy : 元音声をそのままコピー（無劣化）。音声が無ければ映像のみ出力
//   - opus/aac: チャンネル数から自動判定したビットレートで再圧縮。
//     音声なし入力への明示指定は「映像のみ」へ黙って縮退させずエラーとする
//     （フェイルファスト。意図的に音声を付けない場合は --audio none を使う）
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
		// 音声なし入力。copyは「元の構成を維持」なので映像のみで妥当だが、
		// opus/aacの明示指定は要求を満たせないため黙って縮退せずエラーにする
		if mode != domain.AudioCopy {
			return fmt.Errorf("input has no audio stream; --audio %s cannot be applied (use --audio none)", mode)
		}
		args = append(args, "-i", videoPath, "-map", "0:v:0", "-c", "copy", outputPath)
	} else if mode == domain.AudioCopy {
		args = append(args,
			"-i", videoPath, "-i", originalPath,
			"-map", "0:v:0", "-map", "1:a:0",
			"-c:v", "copy", "-c:a", "copy",
			outputPath,
		)
	} else {
		// 再圧縮: ビットレートはチャンネル数から決める（domain.TargetBitrateKbps）
		args = append(args,
			"-i", videoPath, "-i", originalPath,
			"-map", "0:v:0", "-map", "1:a:0",
			"-c:v", "copy",
			"-c:a", audioCodecArgs[mode], "-b:a",
			strconv.Itoa(domain.TargetBitrateKbps(audio.Channels, mode))+"k",
			outputPath,
		)
	}

	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("audio mux failed (mode=%s): %w\n%s", mode, err, toolbin.Tail(string(out), 20))
	}
	return nil
}

// probeFirstAudioStream は元動画の先頭音声ストリームのチャンネル数を取得する。
// ストリームが無い場合は Channels==0 を返す（エラーにしない）。
func probeFirstAudioStream(ctx context.Context, ffprobePath, inputPath string) (audioStream, error) {
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=channels",
		"-of", "json", inputPath).CombinedOutput()
	if err != nil {
		return audioStream{}, fmt.Errorf("probing audio stream: %w\n%s", err, toolbin.Tail(string(out), 20))
	}
	var parsed struct {
		Streams []struct {
			Channels int `json:"channels"`
		} `json:"streams"`
	}
	if uerr := json.Unmarshal(out, &parsed); uerr != nil {
		return audioStream{}, fmt.Errorf("parsing audio probe output: %w", uerr)
	}
	if len(parsed.Streams) == 0 {
		return audioStream{}, nil
	}
	return audioStream{Channels: parsed.Streams[0].Channels}, nil
}
