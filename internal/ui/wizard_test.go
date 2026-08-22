package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"engram-opt/internal/domain"
	"engram-opt/internal/engine"
)

// testWizardModel はsetupステージの検証用Modelを返す。
func testWizardModel(t *testing.T) Model {
	t.Helper()
	m := newModel(Options{
		Codec:  domain.CodecH264,
		Preset: "medium",
		Target: 95.0,
		Audio:  string(domain.AudioCopy),
	}, stageSetup)
	m.sessCtx = context.Background()
	m.workDir = t.TempDir()
	return m
}

func keyEnter() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }
func keyTab() tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyTab} }
func keyLeft() tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyLeft} }
func keyRight() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRight} }
func keyDown() tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyDown} }
func keyRunes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// 選択系フィールドの←→は循環する（端で止まらない）。
func TestWizardSelectCycling(t *testing.T) {
	m := testWizardModel(t)

	// codec: h264 → hevc → av1 → (wrap) h264 → (wrap back) av1
	m, _ = step(t, m, keyTab()) // focus: input -> codec
	if m.wiz.focus != fCodec {
		t.Fatalf("focus = %d, want fCodec", m.wiz.focus)
	}
	m, _ = step(t, m, keyRight())
	if got := m.wiz.codec(); got != domain.CodecHEVC {
		t.Fatalf("codec after right = %s, want hevc", got)
	}
	m, _ = step(t, m, keyRight())
	if got := m.wiz.codec(); got != domain.CodecAV1 {
		t.Fatalf("codec = %s, want av1", got)
	}
	m, _ = step(t, m, keyRight())
	if got := m.wiz.codec(); got != domain.CodecH264 {
		t.Fatalf("codec wrap = %s, want h264", got)
	}
	m, _ = step(t, m, keyLeft())
	if got := m.wiz.codec(); got != domain.CodecAV1 {
		t.Fatalf("codec left-wrap = %s, want av1", got)
	}

	// preset: medium(既定) → 高速(fast) → (wrap) slow → (wrap) fast
	m, _ = step(t, m, keyDown()) // focus -> preset
	m, _ = step(t, m, keyRight())
	if m.wiz.presetIdx != 2 {
		t.Fatalf("preset idx = %d, want 2", m.wiz.presetIdx)
	}
	m, _ = step(t, m, keyRight())
	if m.wiz.presetIdx != 0 {
		t.Fatalf("preset wrap = %d, want 0", m.wiz.presetIdx)
	}
	m, _ = step(t, m, keyLeft())
	if m.wiz.presetIdx != 2 {
		t.Fatalf("preset left-wrap = %d, want 2", m.wiz.presetIdx)
	}

	// audio: copy → opus → aac → none → (wrap) copy
	m, _ = step(t, m, keyDown()) // focus -> audio
	for i, want := range []domain.AudioMode{domain.AudioOpus, domain.AudioAAC, domain.AudioNone, domain.AudioCopy} {
		m, _ = step(t, m, keyRight())
		if got := m.wiz.audio(); got != want {
			t.Fatalf("audio[%d] = %s, want %s", i, got, want)
		}
	}
}

// フォーカス移動はフィールド数で循環する。
func TestWizardFocusWrapsAround(t *testing.T) {
	m := testWizardModel(t)
	if m.wiz.focus != fInput {
		t.Fatalf("initial focus = %d", m.wiz.focus)
	}
	// fieldCount 回の down で元に戻る
	for range fieldCount {
		m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.wiz.focus != fInput {
		t.Fatalf("focus after full cycle = %d, want fInput", m.wiz.focus)
	}
	// up で後方へ循環（input の前は output）
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.wiz.focus != fOutput {
		t.Fatalf("focus after up = %d, want fOutput", m.wiz.focus)
	}
}

// q キーはテキスト入力中は文字、選択フィールドでは終了。esc は常に終了。
func TestWizardQuitKeySemantics(t *testing.T) {
	m := testWizardModel(t)

	// テキスト入力中の q は文字として入る（終了しない）
	next, cmd := m.Update(keyRunes("q"))
	m = next.(Model)
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Fatal("q while editing input must not quit")
		}
	}
	if !strings.Contains(m.wiz.input.Value(), "q") {
		t.Fatalf("input value = %q, want it to contain 'q'", m.wiz.input.Value())
	}

	// 選択フィールドでの q は終了
	m, _ = step(t, m, keyTab()) // focus -> codec
	_, cmd = m.Update(keyRunes("q"))
	if cmd == nil {
		t.Fatal("q on select field should quit")
	}
	if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
		t.Fatal("cmd should be a quit command")
	}

	// esc はテキスト入力中でも常に終了
	m2 := testWizardModel(t)
	_, cmd = m2.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("esc should always quit")
	}
}

// 確定時の検証: 空パス・存在しないファイルは起動せずインラインエラー。
func TestWizardConfirmValidationBlocksStart(t *testing.T) {
	dir := t.TempDir()
	m := testWizardModel(t)
	m.factory = func(string, string, domain.SearchConfig, domain.AudioMode) (PreparedPipeline, error) {
		t.Fatal("factory must not be called for invalid config")
		return PreparedPipeline{}, nil
	}

	// 空入力
	next, cmd := m.Update(keyEnter())
	m = next.(Model)
	if cmd != nil || m.wiz.formErr == "" {
		t.Fatalf("empty input: cmd=%v formErr=%q", cmd, m.wiz.formErr)
	}

	// 存在しないファイル
	missing := filepath.Join(dir, "nope.mp4")
	m.wiz.input.SetValue(missing)
	next, cmd = m.Update(keyEnter())
	m = next.(Model)
	if cmd != nil || !strings.Contains(m.wiz.formErr, missing) {
		t.Fatalf("missing file: cmd=%v formErr=%q", cmd, m.wiz.formErr)
	}
}

// 検証通過時はファクトリが呼ばれ、確定メッセージ経由で実行ステージへ遷移する。
func TestWizardConfirmInvokesFactoryAndTransitions(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.mp4")
	if err := os.WriteFile(in, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var (
		gotIn, gotOut  string
		gotCfg         domain.SearchConfig
		gotAudio       domain.AudioMode
		factoryCalls   int
		resolvedOutput = filepath.Join(dir, "resolved.opt.mkv")
	)
	m := testWizardModel(t)
	m.send = func(tea.Msg) {} // 起動goroutineの通知を受け捨てる
	m.factory = func(in, out string, cfg domain.SearchConfig, audio domain.AudioMode) (PreparedPipeline, error) {
		factoryCalls++
		gotIn, gotOut, gotCfg, gotAudio = in, out, cfg, audio
		return PreparedPipeline{
			Orchestrator: &engine.Orchestrator{},
			InputPath:    in,
			OutputPath:   resolvedOutput,
		}, nil
	}

	m.wiz.input.SetValue(in)

	next, cmd := m.Update(keyEnter())
	m = next.(Model)
	if cmd == nil {
		t.Fatal("valid confirm should return a start command")
	}
	if !m.wiz.starting {
		t.Fatal("starting flag should latch after confirm")
	}
	// starting中の二重Enterは無視される（パイプライン二重起動防止）
	_, dup := m.Update(keyEnter())
	if dup != nil {
		t.Fatal("double Enter must not spawn a second pipeline")
	}

	msg := cmd()
	started, ok := msg.(pipelineStartedMsg)
	if !ok {
		t.Fatalf("cmd msg = %T, want pipelineStartedMsg", msg)
	}
	if started.input != in || started.output != resolvedOutput {
		t.Fatalf("started = %+v", started)
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls = %d", factoryCalls)
	}
	if gotIn != in || gotOut != "" {
		t.Fatalf("factory args: in=%q out=%q", gotIn, gotOut) // 既定名解決はcli側ファクトリの責務
	}
	wantCfg := domain.SearchConfig{
		Codec:       domain.CodecH264,
		MinCRF:      domain.DefaultMinCRF,
		MaxCRF:      domain.DefaultMaxCRF,
		TargetScore: 95.0,
		Preset:      "medium",
	}
	if gotCfg != wantCfg || gotAudio != domain.AudioCopy {
		t.Fatalf("factory cfg/audio = %+v / %s", gotCfg, gotAudio)
	}

	// 起動メッセージで実行ステージへ遷移し、ヘッダ表示用パスが更新される
	next, _ = m.Update(started)
	m = next.(Model)
	if m.stage != stageRun {
		t.Fatalf("stage after start = %v, want run", m.stage)
	}
	if m.opts.InputPath != in || m.opts.OutputPath != resolvedOutput {
		t.Fatalf("opts paths = %q / %q", m.opts.InputPath, m.opts.OutputPath)
	}
}

// ファクトリ失敗（出力先不適合等）はインラインエラーになり再挑戦できる。
func TestWizardFactoryErrorIsInlineAndRecoverable(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.mp4")
	if err := os.WriteFile(in, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	m := testWizardModel(t)
	m.factory = func(string, string, domain.SearchConfig, domain.AudioMode) (PreparedPipeline, error) {
		calls++
		if calls == 1 {
			return PreparedPipeline{}, errors.New("output path is inside the temp dir")
		}
		return PreparedPipeline{Orchestrator: &engine.Orchestrator{}, InputPath: in, OutputPath: "o.mkv"}, nil
	}
	m.wiz.input.SetValue(in)

	next, cmd := m.Update(keyEnter())
	m = next.(Model)
	msg := cmd()
	if _, ok := msg.(setupErrorMsg); !ok {
		t.Fatalf("msg = %T, want setupErrorMsg", msg)
	}
	next, _ = m.Update(msg)
	m = next.(Model)
	if m.wiz.formErr == "" || m.wiz.starting {
		t.Fatalf("after failure: formErr=%q starting=%v", m.wiz.formErr, m.wiz.starting)
	}
	// 再挑戦で成功する
	_, cmd = m.Update(keyEnter())
	if cmd == nil {
		t.Fatal("retry after failure should return a new start command")
	}
}

// setup画面の描画が壊れないことと、固定仕様が明記されていること。
func TestSetupViewRendersFixedSpecNotice(t *testing.T) {
	out := testWizardModel(t).View()
	for _, want := range []string{"入力ファイル", "コーデック", "プリセット", "音声", "出力先", "95.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("view missing %q:\n%s", want, out)
		}
	}
}
