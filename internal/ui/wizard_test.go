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

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// Codecの←→は実値リストを循環する。
func TestWizardCodecCycling(t *testing.T) {
	m := testWizardModel(t)

	m, _ = step(t, m, keyTab()) // focus: input -> codec
	if m.wiz.focus != fCodec {
		t.Fatalf("focus = %d, want fCodec", m.wiz.focus)
	}
	for i, want := range []domain.VideoCodec{domain.CodecHEVC, domain.CodecAV1, domain.CodecH264, domain.CodecHEVC} {
		m, _ = step(t, m, keyRight())
		if got := codecChoices[m.wiz.codecIdx]; got != want {
			t.Fatalf("codec[%d] = %s, want %s", i, got, want)
		}
	}
}

// コーデック切替時、Presetは当該コーデックの実値リストへ再構築され既定へ戻る。
func TestWizardCodecSwitchResetsPresetList(t *testing.T) {
	m := testWizardModel(t)
	if got := m.wiz.preset(); got != "medium" {
		t.Fatalf("initial preset = %s, want medium", got)
	}

	m, _ = step(t, m, keyTab())   // focus -> codec
	m, _ = step(t, m, keyRight()) // -> hevc
	if len(m.wiz.presetList) != 9 || m.wiz.preset() != "medium" {
		t.Fatalf("hevc presets = %v idx=%d", m.wiz.presetList, m.wiz.presetIdx)
	}
	m, _ = step(t, m, keyRight()) // -> av1
	if m.wiz.presetList[0] != "1" || m.wiz.presetList[12] != "13" || len(m.wiz.presetList) != 13 {
		t.Fatalf("av1 presets should be numeric 1..13, got %v", m.wiz.presetList)
	}
	if got := m.wiz.preset(); got != "6" {
		t.Fatalf("preset after switch to av1 = %s, want \"6\"", got)
	}
	m, _ = step(t, m, keyLeft()) // -> hevc へ戻す
	if got := m.wiz.preset(); got != "medium" {
		t.Fatalf("preset after back to hevc = %s, want medium", got)
	}
}

// Bit Depth / Audio の循環。
func TestWizardDepthAndAudioCycling(t *testing.T) {
	m := testWizardModel(t)

	m, _ = step(t, m, keyTab()) // codec
	m, _ = step(t, m, keyTab()) // preset
	m, _ = step(t, m, keyTab()) // min crf
	m, _ = step(t, m, keyTab()) // max crf
	m, _ = step(t, m, keyTab()) // target
	if m.wiz.focus != fTarget {
		t.Fatalf("focus = %d, want fTarget", m.wiz.focus)
	}
	m, _ = step(t, m, keyTab()) // metric
	if got := m.wiz.metric(); got != domain.MetricHarmonic {
		t.Fatalf("initial metric = %s, want harmonic", got)
	}
	for i, want := range []domain.ScoreMetric{domain.MetricMean, domain.MetricMin, domain.MetricHarmonic} {
		m, _ = step(t, m, keyRight())
		if got := m.wiz.metric(); got != want {
			t.Fatalf("metric[%d] = %s, want %s", i, got, want)
		}
	}
	m, _ = step(t, m, keyTab()) // depth
	if m.wiz.depth() != 10 {
		t.Fatalf("initial depth = %d, want 10", m.wiz.depth())
	}
	m, _ = step(t, m, keyRight())
	if m.wiz.depth() != 8 {
		t.Fatalf("depth after right = %d, want 8", m.wiz.depth())
	}
	m, _ = step(t, m, keyRight()) // wrap
	if m.wiz.depth() != 10 {
		t.Fatalf("depth wrap = %d, want 10", m.wiz.depth())
	}

	m, _ = step(t, m, keyTab()) // depth -> audio
	for i, want := range []domain.AudioMode{domain.AudioOpus, domain.AudioAAC, domain.AudioNone, domain.AudioCopy} {
		m, _ = step(t, m, keyRight())
		if got := m.wiz.audio(); got != want {
			t.Fatalf("audio[%d] = %s, want %s", i, got, want)
		}
	}
}

// 数値フィールドは←→でステップ加減算される（CRF±1、VMAF±0.5）。
func TestWizardNumericArrowStepping(t *testing.T) {
	m := testWizardModel(t)

	// Min CRF へ移動: input->codec->preset->mincrf
	m, _ = step(t, m, keyTab())
	m, _ = step(t, m, keyTab())
	m, _ = step(t, m, keyTab())
	if m.wiz.focus != fMinCRF {
		t.Fatalf("focus = %d, want fMinCRF", m.wiz.focus)
	}
	m, _ = step(t, m, keyRight())
	if got := m.wiz.minCRF.Value(); got != "16" {
		t.Fatalf("minCRF after right = %q, want 16", got)
	}
	m, _ = step(t, m, keyLeft())
	m, _ = step(t, m, keyLeft())
	if got := m.wiz.minCRF.Value(); got != "14" {
		t.Fatalf("minCRF after left x2 = %q, want 14", got)
	}

	// Target VMAF は ±0.5 ステップ（minCRFから2進む: maxCRF, target）
	for range 2 {
		m, _ = step(t, m, keyTab())
	}
	if m.wiz.focus != fTarget {
		t.Fatalf("focus = %d, want fTarget", m.wiz.focus)
	}
	m, _ = step(t, m, keyLeft())
	if got := m.wiz.target.Value(); got != "94.5" {
		t.Fatalf("target after left = %q, want 94.5", got)
	}
}

// フォーカス移動はフィールド数で循環する。
func TestWizardFocusWrapsAround(t *testing.T) {
	m := testWizardModel(t)
	if m.wiz.focus != fInput {
		t.Fatalf("initial focus = %d", m.wiz.focus)
	}
	for range fieldCount {
		m, _ = step(t, m, keyDown())
	}
	if m.wiz.focus != fInput {
		t.Fatalf("focus after full cycle = %d, want fInput", m.wiz.focus)
	}
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.wiz.focus != fOutput {
		t.Fatalf("focus after up = %d, want fOutput", m.wiz.focus)
	}
}

// q キーはテキスト入力中は文字、選択フィールドでは終了。esc は常に終了。
func TestWizardQuitKeySemantics(t *testing.T) {
	m := testWizardModel(t)

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

	m, _ = step(t, m, keyTab()) // focus -> codec
	_, cmd = m.Update(keyRunes("q"))
	if cmd == nil {
		t.Fatal("q on select field should quit")
	}
	if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
		t.Fatal("cmd should be a quit command")
	}

	m2 := testWizardModel(t)
	_, cmd = m2.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("esc should always quit")
	}
}

// 確定時の検証: 空パス・存在しないファイル・不正な数値は起動せずインラインエラー。
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

	// 引用符付きパス（D&D等で貼付される形）は剥いてから存在判定されること。
	// 実在ファイル＋意図的な数値不正で、存在チェックを通過した（=引用符が剥れた）ことだけを見る
	m.wiz.input.SetValue(`"` + createTempFileForTest(t, dir) + `"`)
	m.wiz.minCRF.SetValue("abc")
	next, cmd = m.Update(keyEnter())
	m = next.(Model)
	if cmd != nil || !strings.Contains(m.wiz.formErr, "Min CRF") {
		t.Fatalf("quoted path must be stripped: cmd=%v formErr=%q", cmd, m.wiz.formErr)
	}

	// 数値パース失敗
	m.wiz.input.SetValue(createTempFileForTest(t, dir))
	m.wiz.minCRF.SetValue("abc")
	next, cmd = m.Update(keyEnter())
	m = next.(Model)
	if cmd != nil || !strings.Contains(m.wiz.formErr, "Min CRF") {
		t.Fatalf("bad number: cmd=%v formErr=%q", cmd, m.wiz.formErr)
	}

	// ドメイン検証（min > max）
	m.wiz.minCRF.SetValue("40")
	m.wiz.maxCRF.SetValue("20")
	next, cmd = m.Update(keyEnter())
	m = next.(Model)
	if cmd != nil || m.wiz.formErr == "" {
		t.Fatalf("min>max: cmd=%v formErr=%q", cmd, m.wiz.formErr)
	}
}

func createTempFileForTest(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "in.mp4")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// 検証通過時は編集済み全パラメータがファクトリへ渡り、実行ステージへ遷移する。
func TestWizardConfirmInvokesFactoryAndTransitions(t *testing.T) {
	dir := t.TempDir()
	in := createTempFileForTest(t, dir)

	var (
		gotCfg       domain.SearchConfig
		gotAudio     domain.AudioMode
		factoryCalls int
		resolved     = filepath.Join(dir, "resolved.opt.mkv")
	)
	m := testWizardModel(t)
	m.send = func(tea.Msg) {}
	m.factory = func(_, _ string, cfg domain.SearchConfig, audio domain.AudioMode) (PreparedPipeline, error) {
		factoryCalls++
		gotCfg, gotAudio = cfg, audio
		return PreparedPipeline{
			Orchestrator: &engine.Orchestrator{},
			InputPath:    in,
			OutputPath:   resolved,
		}, nil
	}

	// 実値を編集: CRF 15-36→18-32、目標95→92.5、深度10→8、プリセット medium→slow、指標 harmonic→mean
	m.wiz.input.SetValue(in)
	m.wiz.output.SetValue("")
	m.wiz.minCRF.SetValue("18")
	m.wiz.maxCRF.SetValue("32")
	m.wiz.target.SetValue("92.5")
	m.wiz.presetIdx = 6 // slow
	m.wiz.metricIdx = indexOf(metricLabels(), string(domain.MetricMean))

	next, cmd := m.Update(keyEnter())
	m = next.(Model)
	if cmd == nil {
		t.Fatal("valid confirm should return a start command")
	}
	if !m.wiz.starting {
		t.Fatal("starting flag should latch after confirm")
	}
	_, dup := m.Update(keyEnter())
	if dup != nil {
		t.Fatal("double Enter must not spawn a second pipeline")
	}

	msg := cmd()
	started, ok := msg.(pipelineStartedMsg)
	if !ok {
		t.Fatalf("cmd msg = %T, want pipelineStartedMsg", msg)
	}
	if started.input != in || started.output != resolved {
		t.Fatalf("started = %+v", started)
	}
	wantCfg := domain.SearchConfig{
		Codec:       domain.CodecH264,
		MinCRF:      18,
		MaxCRF:      32,
		TargetScore: 92.5,
		Preset:      "slow",
		BitDepth:    10,
		Metric:      domain.MetricMean,
	}
	if gotCfg != wantCfg || gotAudio != domain.AudioCopy {
		t.Fatalf("factory cfg/audio = %+v / %s, want %+v / copy", gotCfg, gotAudio, wantCfg)
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls = %d", factoryCalls)
	}

	next, _ = m.Update(started)
	m = next.(Model)
	if m.stage != stageRun {
		t.Fatalf("stage after start = %v, want run", m.stage)
	}
	if m.opts.InputPath != in || m.opts.OutputPath != resolved {
		t.Fatalf("opts paths = %q / %q", m.opts.InputPath, m.opts.OutputPath)
	}
}

// ファクトリ失敗（出力先不適合等）はインラインエラーになり再挑戦できる。
func TestWizardFactoryErrorIsInlineAndRecoverable(t *testing.T) {
	dir := t.TempDir()
	in := createTempFileForTest(t, dir)

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
	_, cmd = m.Update(keyEnter())
	if cmd == nil {
		t.Fatal("retry after failure should return a new start command")
	}
}

// Options（CLIフラグ由来）の入出力パスは textinput の初期値として反映される。
// 裸起動＋--out 等のフラグ併用時に、値が黙って捨てられないことを保証する。
func TestWizardFormSeedsPathsFromOptions(t *testing.T) {
	w := newWizardForm(Options{
		InputPath:  `C:\Videos\in.mp4`,
		OutputPath: `D:\Out\res.opt.mkv`,
		Codec:      domain.CodecH264,
		Preset:     "medium",
		Audio:      string(domain.AudioCopy),
	})
	if got := w.input.Value(); got != `C:\Videos\in.mp4` {
		t.Fatalf("input seed = %q, want flag value", got)
	}
	if got := w.output.Value(); got != `D:\Out\res.opt.mkv` {
		t.Fatalf("output seed = %q, want flag value", got)
	}

	// 空指定時はプレースホルダ表示のまま（値が入らない）
	w2 := newWizardForm(Options{Codec: domain.CodecH264, Preset: "medium", Audio: string(domain.AudioCopy)})
	if w2.input.Value() != "" || w2.output.Value() != "" {
		t.Fatalf("empty options must not seed paths: %q / %q", w2.input.Value(), w2.output.Value())
	}
}

// setup画面の描画が壊れず、全パラメータがラベル付きで見えること。
func TestSetupViewRendersAllParameters(t *testing.T) {
	out := testWizardModel(t).View()
	for _, want := range []string{
		"入力ファイル", "Codec:", "Preset:", "Min CRF:", "Max CRF:",
		"Target VMAF:", "Bit Depth:", "yuv420p10le", "Audio:", "Output:",
		"< medium >", "< copy >", "95.0",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("view missing %q:\n%s", want, out)
		}
	}
}
