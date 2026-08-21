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

// Run はパイプラインをTUIダッシュボード付きで実行する。
//
// - engine.Orchestrator の進捗コールバックを tea.Msg へ変換して描画に反映する
// - stdlib log の出力は画面破壊防止のためUIログ欄へ迂回させる（復元はdefer）
// - ユーザーが [q] で抜けると context を cancel し、進行中パイプラインを停止させる
func Run(ctx context.Context, orch *engine.Orchestrator, inputPath, outputPath, workDir string, cfg domain.SearchConfig, opts Options) (*engine.PipelineReport, error) {
	// パイプ/リダイレクト先では描画できない。呼び出し側へフォールバックを促す。
	fi, serr := os.Stdout.Stat()
	if serr != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		return nil, ErrNoTTY
	}

	prog := tea.NewProgram(NewModel(opts), tea.WithAltScreen())

	router := &logRouter{send: prog.Send, mirror: opts.LogMirror}
	prevOut := log.Writer()
	log.SetOutput(router)
	defer log.SetOutput(prevOut)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		report, rerr := orch.Run(runCtx, inputPath, outputPath, workDir, cfg, engine.ProgressCallbacks{
			OnDetectionDone: func(scenes []domain.Scene) {
				prog.Send(detectionDoneMsg(scenes))
			},
			OnSceneStart: func(i, _ int) {
				prog.Send(sceneStartMsg{index: i})
			},
			OnTrial: func(t engine.Trial) {
				prog.Send(trialMsg(t))
			},
			OnSceneDone: func(i, _ int, r *engine.Result) {
				prog.Send(sceneDoneMsg{index: i, result: r})
			},
		})
		if rerr != nil {
			prog.Send(pipelineErrMsg{err: rerr})
			return
		}
		prog.Send(pipelineDoneMsg(report))
	}()

	final, perr := prog.Run()
	if perr != nil {
		return nil, fmt.Errorf("tui: %w", perr)
	}
	m, ok := final.(Model)
	if !ok {
		return nil, fmt.Errorf("tui: unexpected final model type %T", final)
	}
	if m.err != nil {
		return nil, m.err // パイプライン本体の失敗
	}
	if !m.finished {
		return nil, context.Canceled // 完了前に中断された
	}
	return m.report, nil
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
