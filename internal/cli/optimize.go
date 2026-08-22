package cli

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"engram-opt/internal/detector/avscenechange"
	"engram-opt/internal/domain"
	ffenc "engram-opt/internal/encoder/ffmpeg"
	"engram-opt/internal/engine"
	"engram-opt/internal/evaluator/libvmaf"
	"engram-opt/internal/toolbin"
	"engram-opt/internal/ui"
)

// registerRun はルートコマンドへ実行系（位置引数 input ＋フラグ＋RunE）を組み込む。
// optimizeサブコマンドは廃止した（単一目的ツールのため、入力はrootの位置引数で直接受ける）。
//
// パイプライン:
//
//  1. scene detection (av-scenechange via FFmpeg Y4M pipe)
//  2. per-shot CRF bisection: find the largest CRF whose VMAF
//     harmonic_mean reaches the target score
//  3. lossless concatenation of the chosen chunks + audio muxing
//
// 起動モード（memo.md「TUIウィザード化」）:
//   - interactive terminal without <input>: opens the setup wizard
//   - interactive terminal with <input>: runs immediately (plain logs)
//   - pipes / CI / redirects: always plain logs (--tui is ignored)
//   - --headless: never show any interactive UI
func registerRun(root *cobra.Command) {
	var (
		output   string
		shot     int
		codec    string
		preset   string
		metric   string
		audio    string
		tui      bool
		headless bool
		logFile  string
	)

	root.Args = cobra.MaximumNArgs(1)
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			// 裸起動: 端末ならウィザード、非端末ならヘルプ
			return bareRunE(cmd)
		}
		ctx := cmd.Context()
		input := args[0]

		// --log-file: 無人実行向けにログをファイルへも二重化する
		var logSink io.Writer // --tui 時は ui.Options.LogMirror に渡し、表示中も二重化を維持する
		if logFile != "" {
			f, lerr := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if lerr != nil {
				return fmt.Errorf("opening log file: %w", lerr)
			}
			defer f.Close()
			logSink = f
			prev := log.Writer()
			log.SetOutput(io.MultiWriter(prev, f))
			defer log.SetOutput(prev)
		}

		mode, merr := decideLaunch(len(args) > 0, ui.IsTerminal(), tui, headless)
		if merr != nil {
			return merr
		}

		tmpRoot, terr := toolbin.TempRoot()
		if terr != nil {
			return terr
		}
		// 同一秒起動の別プロセスとjobDirを共有しないようPID接尾辞を付す
		jobDir := newJobDir(tmpRoot)
		sweepStaleJobs(tmpRoot)

		cfg, err := buildSearchConfig(codec, preset, metric)
		if err != nil {
			return err
		}
		audioMode, aerr := domain.ParseAudioMode(audio)
		if aerr != nil {
			return aerr
		}
		log.Printf("[optimize] audio mode: %s", audioMode)

		// デバッグモード: 単一シーンの二分探索のみ（結合しない）。フラグ専用でウィザード対象外。
		if shot >= 0 {
			return runShotDebug(ctx, input, shot, cfg, jobDir)
		}

		switch mode {
		case launchWizard:
			return launchWizardMode(ctx, input, output, cfg, audioMode, logSink)

		case launchTUI:
			outPath := defaultOutputPathIfEmpty(output, input)
			if err := ensureOutside(jobDir, outPath); err != nil {
				return err
			}
			rep, uerr := ui.Run(ctx, newOrchestrator(audioMode), input, outPath, jobDir, cfg, ui.Options{
				InputPath:  input,
				OutputPath: outPath,
				Codec:      cfg.Codec,
				Preset:     preset,
				Target:     cfg.TargetScore,
				Audio:      string(audioMode),
				LogMirror:  logSink,
			})
			if uerr != nil {
				return uerr
			}
			printSummary(input, rep)
			return nil

		default: // launchPlain
			outPath := defaultOutputPathIfEmpty(output, input)
			if err := ensureOutside(jobDir, outPath); err != nil {
				return err
			}
			report, perr := orchRun(ctx, input, outPath, jobDir, cfg, audioMode)
			if perr != nil {
				return perr
			}
			printSummary(input, report)
			return nil
		}
	}

	f := root.Flags()
	f.StringVarP(&output, "out", "o", "", "final output path (default: <input>.opt.mkv)")
	f.IntVar(&shot, "shot", -1, "debug: run CRF search on this scene index only")
	f.StringVar(&codec, "codec", string(domain.CodecH264), "encode codec: h264 | hevc | av1")
	f.StringVar(&preset, "preset", "medium", "encoder preset (identical across all trials)")
	f.StringVar(&metric, "metric", string(domain.MetricHarmonic), "target score basis: harmonic | mean | min")
	f.StringVar(&audio, "audio", string(domain.DefaultAudioMode), "final audio track: copy | opus | aac | none")
	f.BoolVar(&headless, "headless", false, "never show any interactive UI (plain logs only)")
	f.BoolVar(&tui, "tui", false, "show interactive dashboard (falls back to plain logs when stdout is not a terminal)")
	f.StringVar(&logFile, "log-file", "", "append log output to this file (for unattended runs)")
}

// ===== 起動モード判定（memo.md「TUIウィザード化」） =====

// launchMode は optimize の実行形態。
type launchMode int

const (
	launchPlain  launchMode = iota // 平文ログで即実行
	launchTUI                      // 実行ダッシュボード付き（--tui 明示時）
	launchWizard                   // 設定ウィザードから開始（引数なし＋端末）
)

// decideLaunch は引数有無・TTY・フラグから起動モードを決定する。
//
// ルール:
//   - --headless は常に平文ログ（--tui とは排他）。入力必須
//   - 端末かつ入力なし → ウィザード（ダブルクリック/裸起動の正規フロー）
//   - 端末以外（パイプ/CI/リダイレクト）では対話UIを出さない
//   - 入力あり＋端末での --tui のみダッシュボード。それ以外の入力ありは即実行
func decideLaunch(hasInput, tty, tuiFlag, headless bool) (launchMode, error) {
	switch {
	case headless && tuiFlag:
		return launchPlain, fmt.Errorf("--tui and --headless are mutually exclusive")
	case headless:
		if !hasInput {
			return launchPlain, fmt.Errorf("--headless requires an input video path")
		}
		return launchPlain, nil
	case !hasInput && tty:
		return launchWizard, nil
	case !hasInput:
		return launchPlain, fmt.Errorf("input video is required in non-interactive sessions")
	case tuiFlag && tty:
		return launchTUI, nil
	default:
		return launchPlain, nil
	}
}

// newOrchestrator は実コンポーネントを配線した司令塔を組み立てる。
func newOrchestrator(audio domain.AudioMode) *engine.Orchestrator {
	return &engine.Orchestrator{
		Detector:  avscenechange.New(),
		Encoder:   ffenc.New(),
		Evaluator: libvmaf.New(),
		Muxer:     ffenc.New(),
		Audio:     audio,
	}
}

// newJobDir はジョブ専用の一時ディレクトリパスを返す。
// PID接尾辞により同一秒起動の別プロセスとの衝突（同名チャンクの相互上書き）を防ぐ。
// 平文/TUI/ウィザード全起動経路で必ずこれを使うこと。
func newJobDir(tmpRoot string) string {
	return filepath.Join(tmpRoot,
		fmt.Sprintf("%s-p%d", time.Now().Format("20060102-150405"), os.Getpid()))
}

// sweepStaleJobs は tmpRoot 内で72時間より古いジョブディレクトリを掃除する（ベストエフォート）。
// クラッシュ等で失敗時に残したtmpが無人長時間運用中に単調増加するのを防ぐ。
func sweepStaleJobs(tmpRoot string) {
	entries, err := os.ReadDir(tmpRoot)
	if err != nil {
		return // tmpが未作成など。致命的ではない
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ts, ok := jobTimestampOf(e.Name())
		if !ok || time.Since(ts) < 72*time.Hour {
			continue
		}
		p := filepath.Join(tmpRoot, e.Name())
		if err := os.RemoveAll(p); err != nil {
			log.Printf("[optimize] warning: failed to sweep stale temp dir: %v", err)
		} else {
			log.Printf("[optimize] swept stale temp dir (older than 72h): %s", p)
		}
	}
}

// jobTimestampOf はジョブディレクトリ名先頭の "20060102-150405" 接頭辞を解釈する。
func jobTimestampOf(name string) (time.Time, bool) {
	if len(name) < 15 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("20060102-150405", name[:15], time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// defaultOutputPathIfEmpty は出力未指定時に <入力>.opt.mkv へ解決する。
func defaultOutputPathIfEmpty(output, input string) string {
	if output != "" {
		return output
	}
	return strings.TrimSuffix(input, filepath.Ext(input)) + ".opt.mkv"
}

// orchRun は平文ログモードでのパイプライン実行（進捗を log へ出す）。
func orchRun(ctx context.Context, input, outPath, jobDir string, cfg domain.SearchConfig, audio domain.AudioMode) (*engine.PipelineReport, error) {
	return newOrchestrator(audio).Run(ctx, input, outPath, jobDir, cfg, engine.ProgressCallbacks{
		OnDetectionDone: func(scenes []domain.Scene) {
			log.Printf("[optimize] detected %d scene(s)", len(scenes))
		},
		OnSceneStart: func(i, total int) {
			log.Printf("[optimize] shot %d/%d start", i+1, total)
		},
		OnTrial: trialLogger(cfg),
		OnSceneDone: func(i, _ int, r *engine.Result) {
			log.Printf("[optimize] shot %d done: crf=%d met=%v trials=%d",
				i, r.CRF, r.MetTarget, r.Trials)
		},
	})
}

// trialLogger は試行1回分の平文進捗ログ（orchRun / runShotDebug 共用）。
// 基準指標の値を先頭に、参考として min も併記する。
func trialLogger(cfg domain.SearchConfig) engine.Observer {
	metric := cfg.EffectiveMetric()
	return func(tr engine.Trial) {
		status := "MISS"
		if tr.MetTarget {
			status = "HIT "
		}
		log.Printf("[optimize] shot %d trial crf=%2d %s=%6.2f (min=%6.2f) [%s]",
			tr.Scene.Index, tr.CRF, metric, tr.Metrics.Score(metric), tr.Metrics.Min, status)
	}
}

// buildSearchConfig はCLIフラグ値から探索設定を構築する。
// metricName は "harmonic" | "mean" | "min"（空は既定harmonic）。
func buildSearchConfig(codecName, preset, metricName string) (domain.SearchConfig, error) {
	c := domain.VideoCodec(codecName)
	switch c {
	case domain.CodecH264, domain.CodecHEVC, domain.CodecAV1:
		// OK
	default:
		return domain.SearchConfig{}, fmt.Errorf("unsupported codec %q (use h264 | hevc | av1)", codecName)
	}
	var metric domain.ScoreMetric
	if metricName != "" && metricName != string(domain.MetricHarmonic) {
		m, err := domain.ParseScoreMetric(metricName)
		if err != nil {
			return domain.SearchConfig{}, err
		}
		metric = m
	}
	return domain.SearchConfig{
		Codec:       c,
		MinCRF:      domain.DefaultMinCRF,
		MaxCRF:      domain.DefaultMaxCRF,
		TargetScore: domain.DefaultTargetScore,
		Preset:      preset,
		BitDepth:    domain.DefaultBitDepth,
		Metric:      metric,
	}, nil
}

// runShotDebug は指定シーン1本だけ二分探索を実行するデバッグ用パス。
// 結合は行わず、勝った試行チャンクを残す（調査・品質確認用）。
func runShotDebug(ctx context.Context, input string, idx int, cfg domain.SearchConfig, jobDir string) error {
	scenes, err := avscenechange.New().Detect(ctx, input)
	if err != nil {
		return err
	}
	log.Printf("[optimize] detected %d scene(s)", len(scenes))
	if idx >= len(scenes) {
		return fmt.Errorf("--shot %d out of range (0..%d)", idx, len(scenes)-1)
	}
	sc := scenes[idx]

	shotDir := filepath.Join(jobDir, fmt.Sprintf("shot%04d", sc.Index))
	if err := os.MkdirAll(shotDir, 0o755); err != nil {
		return fmt.Errorf("creating shot dir: %w", err)
	}

	res, err := engine.BisectScene(ctx, ffenc.New(), libvmaf.New(), input, sc, cfg, shotDir, trialLogger(cfg))
	if err != nil {
		return err
	}
	metric := cfg.EffectiveMetric()
	log.Printf("[optimize] shot %d RESULT: crf=%d met=%v trials=%d %s=%.2f",
		sc.Index, res.CRF, res.MetTarget, res.Trials, metric, res.Metrics.Score(metric))
	log.Printf("[optimize] best chunk kept at: %s", res.BestChunkPath)
	return nil
}

// ensureOutside は output が jobDir 配下でないことを確認する。
// 成功時には jobDir を丸ごと削除するため、配下にあると成果物も消えてしまう。
// Windowsではパス大小文字を同一視する（C:\a と c:\A は同一）。
func ensureOutside(jobDir, output string) error {
	absJob, err := filepath.Abs(jobDir)
	if err != nil {
		return err
	}
	absOut, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		absJob, absOut = strings.ToLower(absJob), strings.ToLower(absOut)
	}
	rel, err := filepath.Rel(absJob, absOut)
	if err == nil && !strings.HasPrefix(rel, "..") {
		return fmt.Errorf("output path %q must be outside the temp dir %q", output, jobDir)
	}
	return nil
}
