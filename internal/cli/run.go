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
// 単一目的ツールのため入力はrootの位置引数で直接受ける（optimizeサブコマンドは廃止済み）。
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
		evalProf string
		outRes   string
		encArgs  string
		tui      bool
		headless bool
		logFile  string
	)

	root.Args = cobra.MaximumNArgs(1)
	root.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		var input string
		if len(args) > 0 {
			input = args[0]
		}
		// 相対パス正規化（CLI境界）: --shot等のOrchestrator非経由パスや、
		// cmd.Dirを変える子プロセスからも参照できるようにする。
		// Orchestrator内にも同種の防御がある（多層防御）。
		if input != "" {
			if abs, err := filepath.Abs(input); err == nil {
				input = abs
			}
		}
		if output != "" {
			if abs, err := filepath.Abs(output); err == nil {
				output = abs
			}
		}

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
		if mode == launchHelp {
			// 非対話環境での裸起動（パイプ/CI/リダイレクト）。実行せずヘルプのみ表示する
			return cmd.Help()
		}

		tmpRoot, terr := toolbin.TempRoot()
		if terr != nil {
			return terr
		}
		// 同一秒起動の別プロセスとjobDirを共有しないようPID接尾辞を付す
		jobDir := newJobDir(tmpRoot)
		sweepStaleJobs(tmpRoot)

		outW, outH, oerr := domain.ParseOutRes(outRes)
		if oerr != nil {
			return oerr
		}
		if outW == 0 && input != "" {
			// 未指定=入力動画と同じ解像度。実寸へ解決してログとウィザード初期値に使う
			dw, dh, derr := ffenc.ProbeVideoDims(ctx, input)
			if derr != nil {
				return fmt.Errorf("resolving output resolution from input: %w", derr)
			}
			outW, outH = dw, dh
			log.Printf("[optimize] --out-res empty: following input resolution %dx%d", outW, outH)
		}

		cfg, err := buildSearchConfig(codec, preset, metric, evalProf, outW, outH, encArgs)
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
			if input == "" {
				return fmt.Errorf("--shot requires an input video path")
			}
			return runShotDebug(ctx, input, shot, cfg, jobDir)
		}

		switch mode {
		case launchWizard:
			// ウィザード（裸起動＋端末）。フラグ値は各項目の初期値として反映される。
			// ただし--codec未指定時はウィザード側の既定（AV1）に委ねるため空を渡す。
			seed := cfg
			if !cmd.Flags().Changed("codec") {
				seed.Codec = ""
			}
			return launchWizardMode(ctx, input, output, seed, audioMode, logSink)

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
	f.StringVar(&codec, "codec", string(domain.CodecH264), "encode codec: h264 | hevc | av1 (aliases: libx264 | libx265 | libsvtav1)")
	f.StringVar(&preset, "preset", "medium", "encoder preset (identical across all trials)")
	f.StringVar(&metric, "metric", string(domain.MetricHarmonic), "target score basis: harmonic_mean | mean | min")
	f.StringVar(&audio, "audio", string(domain.DefaultAudioMode), "final audio track: copy | opus | aac | none")
	f.BoolVar(&headless, "headless", false, "never show any interactive UI (plain logs only)")
	f.BoolVar(&tui, "tui", false, "show interactive dashboard (falls back to plain logs when stdout is not a terminal)")
	f.StringVar(&logFile, "log-file", "", "append log output to this file (for unattended runs)")
	f.StringVar(&evalProf, "eval-profile", domain.DefaultEvalProfileName, "evaluation algorithm+resolution set: vmaf_v1.0.16_3d0h (3d0h@1080p) | vmaf_4k_v0.6.1 (4K@2160p)")
	f.StringVar(&outRes, "out-res", "", "output resolution in px (e.g. 1920x1080). empty = same as input video")
	f.StringVar(&encArgs, "enc-args", "", `extra ffmpeg output options for encode trials (e.g. "-tune film"). managed options like -crf are rejected`)
}

// ===== 起動モード判定（memo.md「TUIウィザード化」） =====

// launchMode はランタイムCLI（engram-opt）の実行形態。
type launchMode int

const (
	launchPlain  launchMode = iota // 平文ログで即実行
	launchTUI                      // 実行ダッシュボード付き（--tui 明示時）
	launchWizard                   // 設定ウィザードから開始（引数なし＋端末）
	launchHelp                     // ヘルプ表示のみ（引数なし・非対話環境）
)

// decideLaunch は引数有無・TTY・フラグから起動モードを決定する。
//
// ルール:
//   - --headless は常に平文ログ（--tui とは排他）。入力必須
//   - 端末かつ入力なし → ウィザード（ダブルクリック/裸起動の正規フロー）
//   - 端末以外（パイプ/CI/リダイレクト）では対話UIを出さない。入力なしの裸起動はヘルプ表示
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
		return launchHelp, nil
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

// newJobDir / sweepStaleJobs / jobTimestampOf は jobdir.go へ抽出した
// （jobdir_test.go と対になる配置）。

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
func buildSearchConfig(codecName, preset, metricName, evalProfileName string, outW, outH int, encArgsText string) (domain.SearchConfig, error) {
	// コーデックは短名とffmpegエンコーダ実名（libx264等）の両方を受ける
	c, cerr := domain.ResolveVideoCodec(codecName)
	if cerr != nil {
		return domain.SearchConfig{}, cerr
	}
	var metric domain.ScoreMetric
	if metricName != "" {
		m, err := domain.ParseScoreMetric(metricName)
		if err != nil {
			return domain.SearchConfig{}, err
		}
		metric = m
	}
	evalProfile := domain.DefaultEvalProfile()
	if evalProfileName != "" && evalProfileName != domain.DefaultEvalProfile().Name {
		p, err := domain.ResolveEvalProfile(evalProfileName)
		if err != nil {
			return domain.SearchConfig{}, err
		}
		evalProfile = p
	}
	extraArgs, aerr := domain.ParseExtraArgs(encArgsText)
	if aerr != nil {
		return domain.SearchConfig{}, aerr
	}
	cfg := domain.SearchConfig{
		Codec:       c,
		MinCRF:      domain.DefaultMinCRF,
		MaxCRF:      domain.DefaultMaxCRF,
		TargetScore: domain.DefaultTargetScore,
		Preset:      preset,
		BitDepth:    domain.DefaultBitDepth,
		Metric:      metric,
		Eval:        evalProfile,
		ExtraArgs:   extraArgs,
		OutWidth:    outW,
		OutHeight:   outH,
	}
	if err := cfg.Validate(); err != nil {
		return domain.SearchConfig{}, err
	}
	return cfg, nil
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
