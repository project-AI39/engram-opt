// Package libvmaf は domain.QualityEvaluator の FFmpeg 内蔵 libvmaf 実装。
//
// 実測（FFmpeg 8.1.2 / libvmaf v3系）に基づく実装上の要点:
//   - フィルタ名は 8系で `libvmaf` に改名されている（旧 `vmaf` も一応受容）。
//   - 評価は domain.EvalProfile（アルゴリズム×評価解像度）に完全に従う。
//     両入力をプロファイル解像度へ正規化してから比較する。モデルの切替は行わない——
//     失敗時はエラーとして即座に表面化させる（フェイルファスト。暗黙フォールバック禁止）。
//   - JSONログの pooled_metrics.vmaf に min / mean / harmonic_mean が揃っており、
//     合否判定に必要な値はすべてここから取れる。
//   - log_path に Windows の絶対パス（C:\...）を渡すとフィルタオプション区切りの
//     「:」と衝突して壊れる。→ 作業ディレクトリへ相対パスで書き出し、cmd.Dir を
//     そのディレクトリに設定することで回避する。
package libvmaf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"engram-opt/internal/domain"
	"engram-opt/internal/media"
	"engram-opt/internal/toolbin"
)

const logFileName = "vmaf_report.json"

// Evaluator は domain.QualityEvaluator の実装。
type Evaluator struct{}

// New は Evaluator を生成する。
func New() *Evaluator { return &Evaluator{} }

// Name は実装名を返す。
func (e *Evaluator) Name() string { return "libvmaf" }

// Evaluate 元動画の該当シーン区間とエンコード済みチャンクを比較評価する。
// workDir には評価ログ（vmaf_report.json）の書き出し先ディレクトリを受け取る
// （ジョブ一時領域配下。AGENTS.md「tmpも同一base直下」規約）。
// profile で指定されたモデル・解像度のみを使用し、失敗時は代替へ切り替えない。
func (e *Evaluator) Evaluate(ctx context.Context, originalPath string, scene domain.Scene, encodedChunkPath string, workDir string, profile domain.EvalProfile) (metrics domain.QualityMetrics, retErr error) {
	// 他モジュール（engine/encoder）と同様、フレームSSOT不変条件を入口で検証する。
	// 不正区間のまま filter_complex を組むと select が空になり評価が無意味になるため。
	if err := scene.Validate(); err != nil {
		return domain.QualityMetrics{}, fmt.Errorf("invalid scene: %w", err)
	}
	if err := profile.Validate(); err != nil {
		return domain.QualityMetrics{}, fmt.Errorf("invalid eval profile: %w", err)
	}
	if profile.Algorithm != "libvmaf" {
		return domain.QualityMetrics{}, fmt.Errorf("unsupported evaluation algorithm %q (this evaluator handles libvmaf only)", profile.Algorithm)
	}
	if workDir == "" {
		return domain.QualityMetrics{}, fmt.Errorf("evaluation requires a work directory")
	}
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
	fpsNum, fpsDen, err := media.ProbeFrameRate(ctx, ffprobePath, originalPath)
	if err != nil {
		return domain.QualityMetrics{}, fmt.Errorf("probing frame rate: %w", err)
	}

	// 参照側（元動画）もキーフレームアンカー事前シークする（memo.md §4.2）。
	// 評価は試行ごとに走るため、先頭からの全デコード回避効果がエンコード側と同等に大きい。
	refSeek, offset, err := media.PlanSeek(ctx, ffprobePath, originalPath, scene.StartFrame)
	if err != nil {
		return domain.QualityMetrics{}, err
	}
	if scene.StartFrame > 0 && offset == 0 {
		log.Printf("[vmaf] no usable keyframe before frame %d; decoding reference from start", scene.StartFrame)
	}

	// ログ出力先は作業ディレクトリ配下の専用サブディレクトリ内の相対パス
	// （Windows絶対パスのコロン問題回避）。
	// 呼び出し側所有の workDir は決して削除せず、自分が作った一時サブディレクトリだけを掃除する。
	// 成功時のみ削除し、失敗時は vmaf_report.json を調査証拠として残す。
	evalDir, err := os.MkdirTemp(workDir, "vmaf-")
	if err != nil {
		return domain.QualityMetrics{}, fmt.Errorf("creating evaluation dir: %w", err)
	}
	defer func() {
		if retErr == nil {
			os.Remove(evalDir)
		} else {
			log.Printf("[vmaf] kept report for inspection: %s", filepath.Join(evalDir, logFileName))
		}
	}()

	var err2 error
	metrics, err2 = e.evaluateWithProfile(ctx, ffmpegPath, evalDir, originalPath, scene, encodedChunkPath, profile, fpsNum, fpsDen, refSeek, offset)
	if err2 != nil {
		return domain.QualityMetrics{}, err2 // フェイルファスト: 暗黙のモデル切替はしない
	}
	return metrics, nil
}

func (e *Evaluator) evaluateWithProfile(ctx context.Context, ffmpegPath, workDir, originalPath string, scene domain.Scene, chunkPath string, profile domain.EvalProfile, fpsNum, fpsDen int64, refSeek []string, offset int64) (domain.QualityMetrics, error) {
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
	graph := buildEvalGraph(scene, profile, fpsNum, fpsDen, offset)

	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	// 入力0: エンコード済みチャンク（main=劣化側。チャンクは区間済みのためシーク不要）
	args = append(args, "-i", chunkPath)
	// 入力1: 元動画の該当シーン区間（reference=参照側。アンカー事前シークを先頭に挿入）
	args = append(args, refSeek...)
	args = append(args, "-i", originalPath,
		"-filter_complex", graph,
		"-f", "null", "-")
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return domain.QualityMetrics{}, fmt.Errorf("libvmaf run failed (profile=%s model=%s): %w\n%s",
			profile.Name, profile.Model, err, toolbin.Tail(string(out), 20))
	}

	raw, err := os.ReadFile(filepath.Join(workDir, logFileName))
	if err != nil {
		return domain.QualityMetrics{}, fmt.Errorf("reading vmaf log: %w", err)
	}
	return parseReport(raw, scene)
}

// buildEvalGraph は評価用の filter_complex グラフ文字列を組み立てる純関数。
//
// 評価の一貫性（この関数の契約）:
//   - 参照側[1:v]/劣化側[0:v]の**両方**を profile.Width×profile.Height へ
//     リサイズしてから libvmaf へ入力する。評価アルゴリズムが前提とする
//     解像度へ正規化しないとスコアがずれ、正しいCRF境界を見つけられないため。
//   - 両側が同一変形を受けるため、比較の公平性は保たれる。
//   - select→タイムスタンプ正規化→scale の順で、部分区間でもPTS整合を保つ。
//
// offset>0 のとき参照側入力はキーフレームアンカー（絶対フレームoffset）へ
// 入力シーク済みのため、select範囲はアンカー起点へ平行移動した値を使う
// （出力されるフレーム群はフルデコードとビット一致。memo.md §4.2）。
func buildEvalGraph(scene domain.Scene, profile domain.EvalProfile, fpsNum, fpsDen int64, offset int64) string {
	stamp := fmt.Sprintf("settb=1/%d,setpts=%d*N", fpsNum, fpsDen)
	refChain := fmt.Sprintf("select='between(n,%d,%d)',%s,scale=%d:%d",
		scene.StartFrame-offset, scene.EndFrame-offset, stamp, profile.Width, profile.Height)
	distChain := fmt.Sprintf("%s,scale=%d:%d", stamp, profile.Width, profile.Height)
	return fmt.Sprintf("[1:v]%s[r];[0:v]%s[d];[d][r]libvmaf=log_fmt=json:log_path=%s:model='version=%s':shortest=1:eof_action=endall",
		refChain, distChain, logFileName, profile.Model)
}

// probeFrameRate は元動画のフレームレートを有理数（num/den、ともに整数）で取得する。
// タイムスタンプ正規化式にそのまま埋め込むため、浮動小数点には落とさない。
// （共通実装は internal/media.ProbeFrameRate へ一元。）

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
