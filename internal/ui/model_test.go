package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"engram-opt/internal/domain"
	"engram-opt/internal/engine"
)

func testModel() Model {
	return NewModel(Options{
		InputPath:  "in.mp4",
		OutputPath: "out.opt.mkv",
		Codec:      domain.CodecH264,
		Preset:     "medium",
		Target:     95.0,
	})
}

func scenes2() []domain.Scene {
	return []domain.Scene{
		{Index: 0, StartFrame: 0, EndFrame: 99},
		{Index: 1, StartFrame: 100, EndFrame: 199},
	}
}

func step(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func TestModelDetectionPopulatesScenes(t *testing.T) {
	m := testModel()
	m, _ = step(t, m, detectionDoneMsg(scenes2()))
	if m.phase != phaseOptimizing || m.total != 2 {
		t.Fatalf("phase=%v total=%d", m.phase, m.total)
	}
	for i := range 2 {
		st := m.shots[i]
		if st == nil || st.status != shotPending {
			t.Fatalf("shot %d not initialized pending: %+v", i, st)
		}
	}
}

func TestModelTrialAndSceneDone(t *testing.T) {
	m := testModel()
	m, _ = step(t, m, detectionDoneMsg(scenes2()))
	m, _ = step(t, m, sceneStartMsg{index: 0})

	tr := engine.Trial{
		Scene:     domain.Scene{Index: 0},
		CRF:       22,
		MetTarget: true,
	}
	m, _ = step(t, m, trialMsg(tr))
	if got := m.shots[0].last; got == nil || got.CRF != 22 {
		t.Fatalf("last trial = %+v", got)
	}
	if m.trialCount != 1 {
		t.Fatalf("trialCount = %d", m.trialCount)
	}
	if m.shots[0].status != shotRunning {
		t.Fatalf("shot status = %v", m.shots[0].status)
	}

	res := &engine.Result{
		Scene: domain.Scene{Index: 0}, CRF: 20, MetTarget: true, Trials: 3,
	}
	var hadProgressCmd bool
	next, cmd := m.Update(sceneDoneMsg{index: 0, result: res})
	m = next.(Model)
	hadProgressCmd = cmd != nil
	if !hadProgressCmd {
		t.Fatal("sceneDone should trigger progress update command")
	}
	if m.doneCount != 1 || m.shots[0].result != res || m.shots[0].status != shotDone {
		t.Fatalf("after done: done=%d state=%+v", m.doneCount, m.shots[0])
	}
}

func TestModelCompletionSwitchesToConcatThenDone(t *testing.T) {
	m := testModel()
	m, _ = step(t, m, detectionDoneMsg(scenes2()))

	// 2シーンとも確定して初めて結合フェーズへ移行する
	for i := range 2 {
		res := &engine.Result{Scene: domain.Scene{Index: i}, CRF: 18, MetTarget: true, Trials: 2}
		m, _ = step(t, m, sceneDoneMsg{index: i, result: res})
	}
	if m.phase != phaseConcat {
		t.Fatalf("phase after all shots = %v, want concat", m.phase)
	}
	if m.doneCount != m.total {
		t.Fatalf("doneCount = %d, want %d", m.doneCount, m.total)
	}

	report := &engine.PipelineReport{OutputPath: "out.opt.mkv"}
	next, cmd := m.Update(pipelineDoneMsg(report))
	m = next.(Model)
	// 完了では即quitせず、サマリー画面でキー待ちへ遷移する（memo.md「TUIウィザード化」）
	if cmd != nil {
		t.Fatal("done should NOT quit; it must wait on the summary screen")
	}
	if m.phase != phaseDone || !m.finished || m.report != report {
		t.Fatalf("final state: phase=%v finished=%v report=%+v", m.phase, m.finished, m.report)
	}
	if m.stage != stageSummary {
		t.Fatalf("stage after done = %v, want summary", m.stage)
	}
	// サマリーでのキー入力で初めて終了する
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("summary should quit on Enter")
	}
}

func TestModelFailureMarksRunningShotFailed(t *testing.T) {
	m := testModel()
	m, _ = step(t, m, detectionDoneMsg(scenes2()))
	m, _ = step(t, m, sceneStartMsg{index: 1})

	next, cmd := m.Update(pipelineErrMsg{err: errors.New("boom")})
	m = next.(Model)
	// 失敗内容を読む時間を確保するため、即quitしない（qで明示的に抜ける）
	if cmd != nil {
		t.Fatal("failure should NOT auto-quit; user reads the error and presses q")
	}
	if m.phase != phaseFailed || m.err == nil {
		t.Fatalf("phase=%v err=%v", m.phase, m.err)
	}
	if m.shots[1].status != shotFailed {
		t.Fatalf("running shot should be marked failed: %+v", m.shots[1])
	}
}

func TestModelLogRingBuffer(t *testing.T) {
	m := testModel()
	for _, l := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		m, _ = step(t, m, logLineMsg(l))
	}
	if len(m.logs) != m.logCap {
		t.Fatalf("log len = %d, want cap %d", len(m.logs), m.logCap)
	}
	if m.logs[len(m.logs)-1] != "i" || m.logs[0] != "b" {
		t.Fatalf("ring contents wrong: %v", m.logs)
	}
}

func TestLogRouterMirrorsAndForwardsLines(t *testing.T) {
	var lines []string
	var buf bytes.Buffer
	r := &logRouter{
		send:   func(m tea.Msg) { lines = append(lines, string(m.(logLineMsg))) },
		mirror: &buf,
	}

	n, err := r.Write([]byte("line1\npar"))
	if err != nil || n != len("line1\npar") {
		t.Fatalf("write1: n=%d err=%v", n, err)
	}
	n, err = r.Write([]byte("tial\n"))
	if err != nil || n != len("tial\n") {
		t.Fatalf("write2: n=%d err=%v", n, err)
	}

	// ミラーには生バイトがそのまま複製される
	if got := buf.String(); got != "line1\npartial\n" {
		t.Fatalf("mirror = %q", got)
	}
	// UIへは行組み立て後の2行が渡る
	if len(lines) != 2 || lines[0] != "line1" || lines[1] != "partial" {
		t.Fatalf("lines = %v", lines)
	}
}

func TestLogRouterNilMirror(t *testing.T) {
	var lines []string
	r := &logRouter{send: func(m tea.Msg) { lines = append(lines, string(m.(logLineMsg))) }}
	if _, err := r.Write([]byte("only ui\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(lines) != 1 || lines[0] != "only ui" {
		t.Fatalf("lines = %v", lines)
	}
}

// 極小/極大の端末幅でも進捗バーの幅がクランプされ、描画が壊れないこと。
func TestModelProgressWidthClamped(t *testing.T) {
	m := testModel()

	next, _ := m.Update(tea.WindowSizeMsg{Width: 5, Height: 10})
	m = next.(Model)
	if m.progress.Width != 10 {
		t.Fatalf("tiny window width = %d, want clamped to 10", m.progress.Width)
	}
	if v := m.View(); v == "" {
		t.Fatal("view should still render on tiny windows")
	}

	next, _ = m.Update(tea.WindowSizeMsg{Width: 500, Height: 10})
	m = next.(Model)
	if m.progress.Width != 60 {
		t.Fatalf("huge window width = %d, want capped at 60", m.progress.Width)
	}
}

func TestViewRendersStatesWithoutPanic(t *testing.T) {
	m := testModel()
	m.width = 100
	if v := m.View(); v == "" {
		t.Fatal("empty initial view")
	}

	m, _ = step(t, m, detectionDoneMsg(scenes2()))
	m, _ = step(t, m, sceneStartMsg{index: 0})
	m, _ = step(t, m, trialMsg(engine.Trial{Scene: domain.Scene{Index: 0}, CRF: 30, MetTarget: false}))
	v := m.View()
	for _, want := range []string{"optimizing", "MISS", "in.mp4"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view missing %q:\n%s", want, v)
		}
	}

	res := &engine.Result{
		Scene: domain.Scene{Index: 0}, CRF: 18, MetTarget: true, Trials: 5,
		Metrics: domain.QualityMetrics{HarmonicMean: 96.25},
	}
	m, _ = step(t, m, sceneDoneMsg{index: 0, result: res})
	v = m.View()
	// done行: STATUS=+ / CRF / harmonic / 試行回数
	for _, want := range []string{"+", "18", "96.25", "5"} {
		if !strings.Contains(v, want) {
			t.Fatalf("done view missing %q:\n%s", want, v)
		}
	}
}
