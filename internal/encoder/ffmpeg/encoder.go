// Package ffmpeg は domain.VideoEncoder の FFmpeg 実装。
//
// フレーム完全一致の方針:
//   - チャンク抽出は select フィルタに整数フレーム番号を渡して行う
//     （-ss の浮動小数点秒指定は丸め誤差でVMAF評価を壊すため禁止）。
//   - select は先頭からデコードするため後方シーンほど前処理コストがかかるが、
//     正確性を優先する。高速化は必要になってから検討する。
//
// キーフレーム方針: チャンク先頭は必ずIDR（ストリームコピー結合の前提）。
// 以降の適応的キーフレーム挿入はエンコーダー判断に委ねる（圧縮効率・品質優先）。
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
// 出力ピクセルフォーマットは params.BitDepth 由来（既定10-bit yuv420p10le、8-bitならyuv420p）。
func (e *Encoder) EncodeChunk(ctx context.Context, inputPath string, scene domain.Scene, params domain.EncodeParams, outputPath string) error {
	if !toolbin.FileExists(inputPath) {
		return fmt.Errorf("input file not found: %s", inputPath)
	}
	if err := scene.Validate(); err != nil {
		return fmt.Errorf("invalid scene: %w", err)
	}
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		return err
	}

	gop := strconv.FormatInt(scene.FrameCount(), 10)
	crf := strconv.Itoa(params.CRF)
	pixFmt, err := pixFmtFor(params.BitDepth)
	if err != nil {
		return err
	}

	// 共通引数: シーン区間のみを選択（整数フレーム番号）し、タイムスタンプを0起点へ正規化。
	// 出力リサイズ指定時（--out-res）は select の末尾へ scale を連結する
	// （フレーム番号はリサイズ前の元動画基準のため、順序の入れ替えは不可）。
	//
	// -g（GOP長上限=シーン長）の役割:
	//   - 先頭フレームは常にIDRになる（全エンコーダ共通の保証）
	//   - GOP長の上限をシーン長に固定することで、長尺静止シーンでも周期IDRが
	//     チャンク内に侵入しない（周期IDRは必ずチャンク終端以降に落ちる）
	args := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-i", inputPath,
		"-vf", buildSelectVF(scene, params.OutWidth, params.OutHeight),
		"-frames:v", gop,
		"-pix_fmt", pixFmt,
		"-g", gop,
	}

	switch params.Codec {
	case domain.CodecH264:
		// 適応的キーフレームはエンコーダー既定（scenecut=40）に委ねる。
		// 誤爆被害は x264 既定の min-keyint（自動 ≈ keyint/10）が下限間隔として抑える。
		args = append(args, "-c:v", "libx264", "-preset", params.Preset, "-crf", crf)
	case domain.CodecHEVC:
		// 注意: -sc_threshold は libx264 専用のAVOptionで libx265 には無効（黙って無視される）。
		// x265の適応キーフレームも既定（scenecut=40 + 自動min-keyint）に委ねるため指定なし。
		args = append(args, "-c:v", "libx265", "-preset", params.Preset, "-crf", crf)
	case domain.CodecAV1:
		sp, perr := svtPreset(params.Preset)
		if perr != nil {
			return perr
		}
		// SVT-AV1も同方針（scd既定ON）。実測では小さいフィクスチャのハードカットで
		// 追加キーフレームは入っていないが、入っても仕様上問題ない（結合・評価は成立）。
		args = append(args, "-c:v", "libsvtav1", "-preset", sp, "-crf", crf)
	default:
		return fmt.Errorf("unsupported codec: %q", params.Codec)
	}
	args = append(args, "-an", outputPath)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("encode failed (codec=%s crf=%d): %w\n%s",
			params.Codec, params.CRF, err, toolbin.Tail(string(out), 20))
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

	// --out に未存在ディレクトリを指定された場合でも難解なffmpegエラーにならないよう、
	// 出力先の親ディレクトリをここで用意する。
	if err := os.MkdirAll(filepath.Dir(finalOutputPath), 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	// リストファイルはツール一時領域（<base>/tmp）へ出す（AGENTS.md tmp規約準拠）。
	// -safe 0 により絶対パス参照を許可している。
	tmpBase, err := toolbin.TempRoot()
	if err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(tmpBase, "concat-")
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
		return fmt.Errorf("concat failed: %w\n%s", err, toolbin.Tail(string(out), 20))
	}
	return nil
}

// pixFmtFor はビット深度をffmpegのpix_fmtへ対応させる（memo.md「パラメータ一覧」A-6）。
func pixFmtFor(bitDepth int) (string, error) {
	switch bitDepth {
	case 10:
		return "yuv420p10le", nil
	case 8:
		return "yuv420p", nil
	default:
		return "", fmt.Errorf("unsupported bit depth %d (use 8 or 10)", bitDepth)
	}
}

// svtPreset は x264/x265 流儀の preset 名を SVT-AV1 の数値プリセットへ変換する。
// SVT-AV1 は数値のみを受け付けるための解決層（値は一般的な対応表に基づく近似）。
// 数値文字列はそのまま透過し、それ以外の未知名は黙って代替せずエラーにする
// （ユーザーの指定ミスを早期に表面化させるため）。
func svtPreset(preset string) (string, error) {
	m := map[string]string{
		"veryslow": "1", "slower": "2", "slow": "4", "medium": "6",
		"fast": "8", "faster": "10", "veryfast": "12", "superfast": "13",
	}
	if v, ok := m[preset]; ok {
		return v, nil
	}
	// 数値が直接来た場合はそのまま透過させる
	if _, err := strconv.Atoi(preset); err == nil {
		return preset, nil
	}
	return "", fmt.Errorf("unknown preset %q for av1/libsvtav1: use an x264-style name (veryslow..superfast) or a numeric preset", preset)
}

// buildSelectVF はチャンク切り出し用の -vf 値を組み立てる。
// outWidth/outHeight が両方正の値の場合のみ select/setpts の末尾へ scale を連結する
// （0は「ソース解像度維持」。片側だけの指定はValidateが拒否する前提の防御）。
func buildSelectVF(scene domain.Scene, outWidth, outHeight int) string {
	vf := fmt.Sprintf("select='between(n,%d,%d)',setpts=PTS-STARTPTS", scene.StartFrame, scene.EndFrame)
	if outWidth > 0 && outHeight > 0 {
		vf += fmt.Sprintf(",scale=%d:%d", outWidth, outHeight)
	}
	return vf
}

// concatEscape は concat リスト用にパス中のシングルクォートをエスケープする。
// ffmpegの引用ルールでは、引用終了 + バックスラッシュ付きクォート + 再開 の
// 4文字並びでリテラルのクォートを表現する。
func concatEscape(p string) string {
	return strings.ReplaceAll(p, "'", `'\''`)
}

// tail は文字列の末尾 maxLines 行のみを返す（エラーメッセージ用の切り詰め）。
