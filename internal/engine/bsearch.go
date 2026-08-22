// Package engine はPer-Shot最適化の中核ロジックを提供する。
// 単一シーンのCRF二分探索（bsearch.go）と、全体フロー制御（orchestrator.go・Phase 4）からなる。
package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"engram-opt/internal/domain"
)

// Trial は二分探索の1試行分の結果。
type Trial struct {
	Scene     domain.Scene
	CRF       int
	Metrics   domain.QualityMetrics
	MetTarget bool
}

// Observer は進捗通知のためのコールバック。engineがUIやロガーに依存しないための疎結合ポイント。
// nil の場合は通知しない。
type Observer func(Trial)

// Result は単一シーンの二分探索の確定結果。
type Result struct {
	Scene         domain.Scene
	CRF           int                   // 採用CRF
	BestChunkPath string                // 採用試行の出力ファイル（再エンコード不要）
	Metrics       domain.QualityMetrics // 採用CRF時のスコア
	MetTarget     bool                  // 目標スコアを達成したか（false時はMinCRF採用のベストエフォート）
	Trials        int                   // 実施した試行回数
}

// BisectScene は単一シーンに対し整数CRFの二分探索を行い、
// 「harmonic_mean >= TargetScore を満たす最大CRF」を特定する。
//
// 前提: 同一preset内でCRFを上げるほど画質が下がる（単調性）。これにより
// 「目標未達になる最小CRF」の境界探索として二分探索が成立する。
//
// 試行出力は workDir に書かれ、採用された1つを残して破棄する。
// 採用ファイルを Result.BestChunkPath として返すため、呼び出し側は最終チャンクを
// 再エンコードせずそのまま使える。
func BisectScene(ctx context.Context, enc domain.VideoEncoder, ev domain.QualityEvaluator,
	inputPath string, scene domain.Scene, cfg domain.SearchConfig,
	workDir string, observe Observer) (*Result, error) {

	if cfg.MinCRF > cfg.MaxCRF {
		return nil, fmt.Errorf("invalid CRF range: min=%d max=%d", cfg.MinCRF, cfg.MaxCRF)
	}
	// 出力ビット深度の正規化（0は「未指定」＝既定10）。旧テストのリテラル互換のため。
	paramsBitDepth := cfg.EffectiveBitDepth()
	if err := scene.Validate(); err != nil {
		return nil, fmt.Errorf("invalid scene: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 全試行の出力パスを記録し、確定後に採用1つだけ残して掃除する
	trialPaths := make([]string, 0, 8)

	tryCRF := func(crf int) (domain.QualityMetrics, string, bool, error) {
		out := filepath.Join(workDir, fmt.Sprintf("scene%04d_crf%02d.mkv", scene.Index, crf))
		params := domain.EncodeParams{
			Codec:    cfg.Codec,
			CRF:      crf,
			Preset:   cfg.Preset,
			BitDepth: paramsBitDepth,
		}
		if err := enc.EncodeChunk(ctx, inputPath, scene, params, out); err != nil {
			_ = os.Remove(out) // 失敗試行の断片を掃除
			return domain.QualityMetrics{}, "", false, fmt.Errorf("encode crf=%d: %w", crf, err)
		}
		metrics, err := ev.Evaluate(ctx, inputPath, scene, out, workDir)
		if err != nil {
			_ = os.Remove(out)
			return domain.QualityMetrics{}, "", false, fmt.Errorf("evaluate crf=%d: %w", crf, err)
		}
		trialPaths = append(trialPaths, out)
		met := metrics.TargetMet(cfg.TargetScore)
		if observe != nil {
			observe(Trial{Scene: scene, CRF: crf, Metrics: metrics, MetTarget: met})
		}
		return metrics, out, met, nil
	}

	trials := 0

	// 最良ケースの早期決定: 上限CRFで目標達成なら探索不要（サイズ最小）。
	mHi, pHi, metHi, err := tryCRF(cfg.MaxCRF)
	if err != nil {
		return nil, err
	}
	trials++
	if metHi {
		cleanupExcept(pHi, trialPaths)
		return &Result{Scene: scene, CRF: cfg.MaxCRF, BestChunkPath: pHi, Metrics: mHi, MetTarget: true, Trials: trials}, nil
	}

	// 探索幅ゼロ（Min==Max）なら上限試行をそのまま採用する（同一CRFの再エンコード回避）。
	if cfg.MinCRF == cfg.MaxCRF {
		log.Printf("[engine] scene %d: single-point range (CRF %d, harmonic_mean=%.2f); adopting it as best effort",
			scene.Index, cfg.MaxCRF, mHi.HarmonicMean)
		cleanupExcept(pHi, trialPaths)
		return &Result{Scene: scene, CRF: cfg.MaxCRF, BestChunkPath: pHi, Metrics: mHi, MetTarget: false, Trials: trials}, nil
	}

	// 下限CRFでも未達なら目標不可能。仕様どおり MinCRF をベストエフォートで採用する。
	mLo, pLo, metLo, err := tryCRF(cfg.MinCRF)
	if err != nil {
		return nil, err
	}
	trials++
	result := &Result{Scene: scene, CRF: cfg.MinCRF, BestChunkPath: pLo, Metrics: mLo, MetTarget: metLo, Trials: trials}
	if !metLo {
		log.Printf("[engine] scene %d: target %.2f unreachable even at CRF %d (harmonic_mean=%.2f); adopting minimum CRF",
			scene.Index, cfg.TargetScore, cfg.MinCRF, mLo.HarmonicMean)
		cleanupExcept(result.BestChunkPath, trialPaths)
		return result, nil
	}

	// 不変条件: lo=達成済み最大CRF / hi=未達が保証された最小CRF。境界が隣接するまで絞る。
	lo, hi := cfg.MinCRF, cfg.MaxCRF
	for hi-lo > 1 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mid := (lo + hi) / 2
		mMid, pMid, metMid, err := tryCRF(mid)
		if err != nil {
			return nil, err
		}
		trials++
		if metMid {
			lo = mid
			result.CRF, result.Metrics, result.BestChunkPath = mid, mMid, pMid
		} else {
			hi = mid
		}
	}

	result.Trials = trials // ループ中の増分を最終結果へ反映
	cleanupExcept(result.BestChunkPath, trialPaths)
	return result, nil
}

// cleanupExcept は keep を残して trials 内の他のファイルを破棄する（ベストエフォート）。
func cleanupExcept(keep string, trials []string) {
	for _, p := range trials {
		if p == keep {
			continue
		}
		_ = os.Remove(p)
	}
}
