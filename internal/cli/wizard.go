package cli

// ウィザード起動ランチャー（memo.md「TUIウィザード化」）。
// CLIフラグ値を初期値として設定ウィザードを開き、[Enter] 確定時に
// ファクトリ経由で Orchestrator を組み立てて実行へ移行する。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"engram-opt/internal/domain"
	"engram-opt/internal/engine"
	"engram-opt/internal/toolbin"
	"engram-opt/internal/ui"
)

// launchWizardMode は設定ウィザードセッションを実行する。
// input/output はフラグ由来の初期値（通常どちらも空）。
func launchWizardMode(ctx context.Context, input, output string, cfg domain.SearchConfig, audio domain.AudioMode, logSink io.Writer) error {
	tmpRoot, err := toolbin.TempRoot()
	if err != nil {
		return err
	}
	jobDir := newJobDir(tmpRoot)

	opts := ui.Options{
		InputPath:  input,
		OutputPath: output,
		Codec:      cfg.Codec,
		Preset:     cfg.Preset,
		Target:     cfg.TargetScore,
		MinCRF:     cfg.MinCRF,
		MaxCRF:     cfg.MaxCRF,
		BitDepth:   cfg.BitDepth,
		Metric:     string(cfg.EffectiveMetric()),
		Audio:      string(audio),
		LogMirror:  logSink,
	}

	// ファクトリは [Enter] 確定時に呼ばれる。既定出力名の解決と
	// 一時領域との整合チェック（ensureOutside）もこのタイミングで行う。
	usedInput := ""
	factory := func(in, out string, _ domain.SearchConfig, amode domain.AudioMode) (ui.PreparedPipeline, error) {
		if in == "" {
			return ui.PreparedPipeline{}, fmt.Errorf("input file is empty")
		}
		if out == "" {
			out = defaultOutputPathIfEmpty(out, in)
		}
		// 元動画保護（出力=入力上書き防止）をパイプライン起動前に早期検証
		if derr := engine.RequireDistinctPaths(in, out); derr != nil {
			return ui.PreparedPipeline{}, derr
		}
		if aerr := ensureOutside(jobDir, out); aerr != nil {
			return ui.PreparedPipeline{}, aerr
		}
		usedInput = in
		log.Printf("[optimize] starting: %s -> %s", in, out)
		return ui.PreparedPipeline{
			Orchestrator: newOrchestrator(amode),
			InputPath:    in,
			OutputPath:   out,
		}, nil
	}

	rep, werr := ui.RunWizard(ctx, jobDir, opts, factory)
	if werr != nil {
		// ウィザードを閉じただけ（実行前中断）は正常終了扱い
		if errors.Is(werr, context.Canceled) {
			log.Printf("[optimize] wizard closed without running")
			return nil
		}
		return werr
	}
	printSummary(usedInput, rep)
	return nil
}

// printSummary は完了サマリ（達成率・サイズ削減・出力先）を出す。
func printSummary(input string, r *engine.PipelineReport) {
	met := 0
	for _, res := range r.Results {
		if res.MetTarget {
			met++
		}
	}
	metric := r.Metric
	if metric == "" {
		metric = domain.MetricHarmonic
	}
	log.Printf("[optimize] %d/%d shot(s) met target score (metric=%s)", met, len(r.Results), metric)

	var inSize, outSize int64
	if st, err := os.Stat(input); err == nil {
		inSize = st.Size()
	}
	if st, err := os.Stat(r.OutputPath); err == nil {
		outSize = st.Size()
	}
	if inSize > 0 && outSize > 0 {
		delta := 100 * (1 - float64(outSize)/float64(inSize))
		sign := "-"
		if delta < 0 {
			// 再エンコードで大きくなった場合は増加として正直に表示する
			sign = "+"
			delta = -delta
		}
		log.Printf("[optimize] size: %.2f MB -> %.2f MB (%s%.1f%%)",
			float64(inSize)/(1<<20), float64(outSize)/(1<<20), sign, delta)
	}
	log.Printf("[optimize] output: %s (total trials=%d)", r.OutputPath, r.TotalTrials)
}
