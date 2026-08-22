package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"engram-opt/internal/domain"
	"engram-opt/internal/engine"
)

// ErrNoTTY はstdoutが端末でないためTUIを表示できないことを示す。
// 呼び出し側はこのエラーを平文ログモードへのフォールバック合図として扱う。
var ErrNoTTY = errors.New("stdout is not a terminal; TUI unavailable")

// IsTerminal はstdoutが端末に接続されているかどうかを返す。
// stdlibのみで判定する（mattn/go-isatty 等の依存は導入しない方針）。
// CLIの起動モード判定（memo.md「TUIウィザード化」）と、TUI起動前のガードの双方で使う。
//
// 注意: ModeCharDevice はNULデバイス等のキャラクタデバイスも端末扱いする近似判定。
// リダイレクト（ファイル/パイプ）は確実に false になるため実用上は十分だが、
// 厳密なTTY同定が必要になった場合はOS固有APIの導入を検討する。
func IsTerminal() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Run はパイプラインを実行ダッシュボード付きで走らせる（--tui 直指定の経路）。
//
// - engine.Orchestrator の進捗コールバックを tea.Msg へ変換して描画に反映する
// - stdlib log の出力は画面破壊防止のためUIログ欄へ迂回させる（復元はdefer）
// - 完了後はサマリー画面でキー待ちし、[q]/[Enter] で抜けると終了する
func Run(ctx context.Context, orch *engine.Orchestrator, inputPath, outputPath, workDir string, cfg domain.SearchConfig, opts Options) (*engine.PipelineReport, error) {
	if !IsTerminal() {
		return nil, ErrNoTTY
	}
	m := newModel(opts, stageRun)
	m.workDir = workDir
	return runSession(ctx, m, func(s *sessionHooks) {
		spawnPipeline(s.send, s.ctx, PreparedPipeline{
			Orchestrator: orch,
			InputPath:    inputPath,
			OutputPath:   outputPath,
		}, cfg, workDir)
	})
}

// RunWizard は設定ウィザードから始まるフルセッション（setup → run → summary）を実行する。
//
// ユーザーが [Enter] で設定を確定した時点で factory が呼ばれ、戻った Orchestrator で
// パイプラインが開始される。factory のエラーはウィザードへインライン表示される。
// ユーザーがウィザードを Esc 等で閉じた場合は context.Canceled を返す。
func RunWizard(ctx context.Context, workDir string, opts Options, factory PipelineFactory) (*engine.PipelineReport, error) {
	if !IsTerminal() {
		return nil, ErrNoTTY
	}
	m := newModel(opts, stageSetup)
	m.workDir = workDir
	m.factory = factory
	return runSession(ctx, m, nil)
}

// ===== セッション共通部 =====

// sessionHooks はパイプライン起動に必要な実行時ハンドル。
type sessionHooks struct {
	ctx  context.Context // q押下時にcancelされる
	send func(tea.Msg)
}

// sender は tea.Program 生成前に Model へ埋め込める送信口。
// Program は初期モデルのコピーを保持するため、prog.Send を直接持たせるのではなく
// ポインタレシーバの間接層を経由させる（生成後にfnを差し込めるようにするため）。
type sender struct {
	fn func(tea.Msg)
}

func (s *sender) Send(m tea.Msg) {
	if s.fn != nil {
		s.fn(m)
	}
}

// runSession はTUIプログラムの共通ライフサイクル（ログ迂回・ctx管理・結果解釈）を担う。
// kick は直実行モード（--tui）でパイプラインを即座に始めたい場合に渡す。ウィザードではnil。
func runSession(ctx context.Context, m Model, kick func(*sessionHooks)) (*engine.PipelineReport, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	hooks := &sessionHooks{}
	sd := &sender{}
	hooks.send = sd.Send
	m.send = hooks.send
	m.sessCtx = runCtx

	prog := tea.NewProgram(m, tea.WithAltScreen())
	sd.fn = prog.Send // 以降、どのタイミングのSendも有効

	// stdlib log をUIログ欄へ迂回（復元はdeferで保証）
	router := &logRouter{send: hooks.send, mirror: m.opts.LogMirror}
	prevOut := log.Writer()
	log.SetOutput(router)
	defer log.SetOutput(prevOut)

	if kick != nil {
		hooks.ctx = runCtx
		kick(hooks)
	}

	final, perr := prog.Run()
	if perr != nil {
		return nil, fmt.Errorf("tui: %w", perr)
	}
	fm, ok := final.(Model)
	if !ok {
		return nil, fmt.Errorf("tui: unexpected final model type %T", final)
	}
	if fm.err != nil {
		return nil, fm.err // パイプライン本体の失敗
	}
	if !fm.finished {
		return nil, context.Canceled // 完了前に中断された
	}
	return fm.report, nil
}

// spawnPipeline は Orchestrator 実行goroutineを立て、進捗を tea.Msg として送る。
func spawnPipeline(send func(tea.Msg), runCtx context.Context, prep PreparedPipeline, cfg domain.SearchConfig, workDir string) {
	go func() {
		report, rerr := prep.Orchestrator.Run(runCtx, prep.InputPath, prep.OutputPath, workDir, cfg, engine.ProgressCallbacks{
			OnDetectionDone: func(scenes []domain.Scene) {
				send(detectionDoneMsg(scenes))
			},
			OnSceneStart: func(i, _ int) {
				send(sceneStartMsg{index: i})
			},
			OnTrial: func(t engine.Trial) {
				send(trialMsg(t))
			},
			OnSceneDone: func(i, _ int, r *engine.Result) {
				send(sceneDoneMsg{index: i, result: r})
			},
		})
		if rerr != nil {
			send(pipelineErrMsg{err: rerr})
			return
		}
		send(pipelineDoneMsg(report))
	}()
}

// logRouter は log パッケージの出力を1行ずつ tea.Msg 化してUIへ転送する。
// mirror（--log-file の書き込み先など）が設定されている場合は、生バイトを
// そのまま複製する。TUI表示中もファイルへの二重化を維持するための仕組みで、
// stderr へは出さない（画面破壊防止）。
// log.Logger は書き込みを直列化するため1 Write が1完全行で来るのが通常だが、
// 複数Writeに分かれた場合に備えて内部バッファで行組み立てを行う。
type logRouter struct {
	send   func(tea.Msg)
	mirror io.Writer // nil許容。TUI中も複製したい出力先
	buf    []byte
	mu     sync.Mutex
}

func (r *logRouter) Write(p []byte) (int, error) {
	r.mu.Lock()
	if r.mirror != nil {
		// ミラー先の書き込みエラーは握り潰す（stdlib log もWriteエラーは無視する仕様）
		_, _ = r.mirror.Write(p)
	}
	r.buf = append(r.buf, p...)
	var pending []string
	for {
		i := bytes.IndexByte(r.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(r.buf[:i]), "\r")
		r.buf = r.buf[i+1:]
		if line != "" {
			pending = append(pending, line)
		}
	}
	r.mu.Unlock()

	for _, l := range pending {
		r.send(logLineMsg(l))
	}
	return len(p), nil
}
