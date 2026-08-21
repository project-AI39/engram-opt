// Package libvmaf は domain.QualityEvaluator の FFmpeg 内蔵 libvmaf 実装。
//
// 実測（FFmpeg 8.1.2 / libvmaf v3系）に基づく実装上の要点:
//   - フィルタ名は 8系で `libvmaf` に改名されている（旧 `vmaf` も一応受容）。
//   - モデル指定は `model=version=vmaf_v1.0.16_3d0h`。CAMBI特徴量を含むため、
//     入力が低解像度だと `no feature 'cambi_hrs_1080_...'` エラーで失敗する。
//     → 両入力を必ず 1920x1080 へリサイズしてから投入する（解像度ガード）。
//   - フォールバックモデルは `vmaf_v0.6.1neg`。
//   - JSONログの pooled_metrics.vmaf に min / mean / harmonic_mean が揃っており、
//     合否判定（harmonic_mean）に必要な値はすべてここから取れる。
//   - log_path に Windows の絶対パス（C:\...）を渡すとフィルタオプション区切りの
//     「:」と衝突して壊れる。→ 作業ディレクトリへ相対パスで書き出し、cmd.Dir を
//     そのディレクトリに設定することで回避する。
package libvmaf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"engram-opt/internal/domain"
	"engram-opt/internal/toolbin"
)

const (
	primaryModel  = "version=vmaf_v1.0.16_3d0h" // 主力モデル（CAMBI含む・要1080p）
	fallbackModel = "version=vmaf_v0.6.1neg"    // フォールバック（低解像度でも動作）
	logFileName   = "vmaf_report.json"
)

// Evaluator は domain.QualityEvaluator の実装。
type Evaluator struct{}

// New は Evaluator を生成する。
func New() *Evaluator { return &Evaluator{} }

// Name は実装名を返す。
func (e *Evaluator) Name() string { return "libvmaf" }

// Evaluate 元動画の該当シーン区間とエンコード済みチャンクを比較評価する。
func (e *Evaluator) Evaluate(ctx context.Context, originalPath string, scene domain.Scene, encodedChunkPath string) (domain.QualityMetrics, error) {
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		return domain.QualityMetrics{}, err
	}
	ffprobePath, err := toolbin.Resolve("ffprobe")
	if err != nil {
		return domain.QualityMetrics{}, err
	}
	// フレームインデックスベースのタイムスタンプ正規化のために入力fps（整数比）を取得する。
	// コンテナ間の時間基準差（mkv 1/1000 など）による丸めで framesync のペアリングが
	// ずれる問題への根本対処（実測: 対策なしではPSNR 28dBまで崩壊）。
	fpsNum, fpsDen, err := probeFrameRate(ctx, ffprobePath, originalPath)
	if err != nil {
		return domain.QualityMetrics{}, fmt.Errorf("probing frame rate: %w", err)
	}

	// ログ出力先は作業ディレクトリ内の相対パス（Windows絶対パスのコロン問題回避）
	workDir, err := os.MkdirTemp("", "engram-vmaf-")
	if err != nil {
		return domain.QualityMetrics{}, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	metrics, primaryErr := e.evaluateWithModel(ctx, ffmpegPath, workDir, originalPath, scene, encodedChunkPath, primaryModel, fpsNum, fpsDen)
	if primaryErr != nil {
		// フォールバックモデルで再試行（固定仕様）
		m2, fbErr := e.evaluateWithModel(ctx, ffmpegPath, workDir, originalPath, scene, encodedChunkPath, fallbackModel, fpsNum, fpsDen)
		if fbErr != nil {
			return domain.QualityMetrics{}, fmt.Errorf(
				"vmaf evaluation failed (primary %s: %v / fallback %s: %w)",
				primaryModel, primaryErr, fallbackModel, fbErr)
		}
		metrics = m2
	}
	return metrics, nil
}

func (e *Evaluator) evaluateWithModel(ctx context.Context, ffmpegPath, workDir, originalPath string, scene domain.Scene, chunkPath, model string, fpsNum, fpsDen int64) (domain.QualityMetrics, error) {
	// 入力0: エンコード済みチャンク（main=劣化側）
	// 入力1: 元動画の該当シーン区間（reference=参照側）
	//
	// タイムスタンプ正規化（重要）:
	//   コンテナ間で時間基準が違うと（例: mkv 1/1000 vs mp4 1/15360）、framesync の
	//   ペアリングが丸めずれし、全く別フレーム同士を比較してスコアが壊滅する
	//   （実測: 対策なしではPSNR/VMAFが28dB/20点台まで崩壊した）。
	//   そこで両入力とも settb=1/{fpsNum} で共通の細かい時間基準へ揃え、
	//   第kフレームのPTSを整数刻み {fpsDen}*k で打ち直す。
	//   （frame duration = fpsDen/fpsNum 秒 = ちょうど fpsDen tick。任意の有理数fpsで
	//     厳密に整数になるため誤差ゼロ。）
	//   select 後の setpts の N は「選択通過フレーム」の連番になるため部分区間でも正しい。
	stamp := fmt.Sprintf("settb=1/%d,setpts=%d*N", fpsNum, fpsDen)
	refChain := fmt.Sprintf("select='between(n,%d,%d)',%s,scale=%d:%d",
		scene.StartFrame, scene.EndFrame, stamp, vmafWidth, vmafHeight)
	distChain := fmt.Sprintf("%s,scale=%d:%d", stamp, vmafWidth, vmafHeight)
	graph := fmt.Sprintf("[1:v]%s[r];[0:v]%s[d];[d][r]libvmaf=log_fmt=json:log_path=%s:model='%s':shortest=1:eof_action=endall",
		refChain, distChain, logFileName, model)

	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-hide_banner", "-nostdin", "-loglevel", "error",
		"-i", chunkPath,
		"-i", originalPath,
		"-filter_complex", graph,
		"-f", "null", "-")
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return domain.QualityMetrics{}, fmt.Errorf("libvmaf run failed (%s): %w\n%s",
			model, err, tail(string(out), 20))
	}

	raw, err := os.ReadFile(filepath.Join(workDir, logFileName))
	if err != nil {
		return domain.QualityMetrics{}, fmt.Errorf("reading vmaf log: %w", err)
	}
	return parseReport(raw, scene)
}

// vmaf 解像度ガード: CAMBI特徴量は1080p前提のため両入力を強制スケールする。
const (
	vmafWidth  = 1920
	vmafHeight = 1080
)

// probeFrameRate は元動画のフレームレートを有理数（num/den、ともに整数）で取得する。
// タイムスタンプ正規化式にそのまま埋め込むため、浮動小数点には落とさない。
func probeFrameRate(ctx context.Context, ffprobePath, inputPath string) (int64, int64, error) {
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=r_frame_rate",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputPath).Output()
	if err != nil {
		return 0, 0, err
	}
	s := strings.TrimSpace(string(out))
	numStr, denStr, ok := strings.Cut(s, "/")
	if !ok {
		numStr, denStr = s, "1"
	}
	num, err1 := strconv.ParseInt(numStr, 10, 64)
	den, err2 := strconv.ParseInt(denStr, 10, 64)
	if err1 != nil || err2 != nil || num <= 0 || den <= 0 {
		return 0, 0, fmt.Errorf("invalid r_frame_rate %q", s)
	}
	return num, den, nil
}

// ===== レポート解析 =====

type vmafLog struct {
	Frames []struct {
		FrameNum int `json:"frameNum"`
	} `json:"frames"`
	PooledMetrics map[string]struct {
		Min          float64 `json:"min"`
		Max          float64 `json:"max"`
		Mean         float64 `json:"mean"`
		HarmonicMean float64 `json:"harmonic_mean"`
	} `json:"pooled_metrics"`
}

// parseReport は libvmaf JSONログから合否判定に必要な代表値を取り出す。
// 参照側とチャンク側のフレーム数一致もここで検証する（評価ズレの早期発見）。
func parseReport(raw []byte, scene domain.Scene) (domain.QualityMetrics, error) {
	var log vmafLog
	if err := json.Unmarshal(raw, &log); err != nil {
		return domain.QualityMetrics{}, fmt.Errorf("parsing vmaf json log: %w", err)
	}
	if got, want := len(log.Frames), int(scene.FrameCount()); got != want {
		return domain.QualityMetrics{}, fmt.Errorf("frame count mismatch in evaluation: got %d frames, want %d", got, want)
	}
	pooled, ok := log.PooledMetrics["vmaf"]
	if !ok {
		return domain.QualityMetrics{}, errors.New("vmaf json log has no pooled metric \"vmaf\"")
	}
	return domain.QualityMetrics{
		HarmonicMean: pooled.HarmonicMean,
		Mean:         pooled.Mean,
		Min:          pooled.Min,
	}, nil
}

// tail は文字列の末尾 maxLines 行のみを返す（エラーメッセージ用の切り詰め）。
func tail(s string, maxLines int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}
