package ui

// ウィザード（setupステージ）の実装。
// Phase 8方針（memo.md「パラメータ一覧と露出方針」）: 抽象ラベルは使わず、
// 内部で実際に使われているパラメータ名と値をそのまま選択肢として出す。
// 全項目に最適な既定値を事前入力し、触らなければ従来の固定仕様と同じ挙動になる。

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"engram-opt/internal/domain"
	"engram-opt/internal/engine"
	"engram-opt/internal/toolbin"
)

// PreparedPipeline は設定確定後に組み立てられた実行準備済みパイプライン。
type PreparedPipeline struct {
	Orchestrator *engine.Orchestrator
	InputPath    string // 空不可（ウィザード側で存在検証済み）
	OutputPath   string // 既定名解決済み
}

// PipelineFactory は確定した設定から Orchestrator を組み立てる。
// 出力先の既定名解決や一時領域との整合チェック（ensureOutside等）もここで行う。
// 失敗時のエラーはウィザード画面へインライン表示される。
type PipelineFactory func(inputPath, outputPath string, cfg domain.SearchConfig, audio domain.AudioMode) (PreparedPipeline, error)

// ===== 選択肢テーブル（実値そのもの） =====

var codecChoices = []domain.VideoCodec{domain.CodecH264, domain.CodecHEVC, domain.CodecAV1}

// presetChoicesFor はコーデックごとの実プリセット値リストと既定インデックスを返す。
//
//   - h264/hevc: libx264/libx265 へそのまま渡す x264流9段階
//   - av1: libsvtav1 のネイティブな数値プリセット "1"〜"13"（小さいほど高品質・低速）。
//     svtPreset が数値を透過するため内部パラメータどおりの文字列を渡せる
func presetChoicesFor(c domain.VideoCodec) ([]string, int) {
	if c == domain.CodecAV1 {
		list := make([]string, 0, 13)
		for i := 1; i <= 13; i++ {
			list = append(list, strconv.Itoa(i))
		}
		return list, 5 // 既定 "6"（= medium相当）
	}
	return []string{
		"ultrafast", "superfast", "veryfast", "faster", "fast",
		"medium", "slow", "slower", "veryslow",
	}, 5 // 既定 medium
}

// depthChoices は出力ビット深度の実値（先頭が既定）。
var depthChoices = []int{domain.DefaultBitDepth, 8}

var depthLabels = map[int]string{
	10: "yuv420p10le",
	8:  "yuv420p",
}

var audioChoices = []domain.AudioMode{
	domain.AudioCopy,
	domain.AudioOpus,
	domain.AudioAAC,
	domain.AudioNone,
}

// フォーカス対象のフィールド番号
const (
	fInput = iota
	fCodec
	fPreset
	fMinCRF
	fMaxCRF
	fTarget
	fMetric
	fDepth
	fAudio
	fOutput
	fieldCount
)

// metricChoices は合否指標の実値（先頭が既定）。
var metricChoices = []domain.ScoreMetric{
	domain.MetricHarmonic,
	domain.MetricMean,
	domain.MetricMin,
}

// wizardForm はsetupフェーズのフォーム状態。
type wizardForm struct {
	focus int

	input  textinput.Model
	output textinput.Model
	minCRF textinput.Model
	maxCRF textinput.Model
	target textinput.Model

	codecIdx   int
	presetList []string // 現在のコーデックに応じた実値リスト（切替時に再構築）
	presetIdx  int
	metricIdx  int
	depthIdx   int
	audioIdx   int

	formErr  string
	starting bool // Enter連打による二重起動防止
}

func newNumericInput(placeholder string, value string) textinput.Model {
	t := textinput.New()
	t.Prompt = ""
	t.Placeholder = placeholder
	if value != "" {
		t.SetValue(value)
	}
	return t
}

func newWizardForm(opts Options) wizardForm {
	in := textinput.New()
	in.Placeholder = "例: C:\\Videos\\input.mp4 （D&Dでパスを貼り付け可）"
	in.Prompt = ""
	in.CharLimit = 260
	in.Focus()
	if opts.InputPath != "" {
		// フラグ由来の入力初期値を尊重する（現行の起動モード判定では通常空）
		in.SetValue(opts.InputPath)
	}

	out := textinput.New()
	out.Placeholder = "空欄= <入力名>.opt.mkv"
	out.Prompt = ""
	out.CharLimit = 260
	if opts.OutputPath != "" {
		// --out フラグ値を出力欄の初期値へ（memo.md「フラグ値はウィザード初期値に反映」）
		out.SetValue(opts.OutputPath)
	}

	// テキスト入力の見た目（値=明色 / プレースホルダ=薄色）
	for _, t := range []*textinput.Model{&in, &out} {
		t.TextStyle = valueStyle
		t.PlaceholderStyle = dimStyle
	}

	// 数値フィールドはOptions由来の初期値（0は未指定扱いでドメイン既定へ）
	minC := opts.MinCRF
	if minC == 0 {
		minC = domain.DefaultMinCRF
	}
	maxC := opts.MaxCRF
	if maxC == 0 {
		maxC = domain.DefaultMaxCRF
	}
	target := opts.Target
	if target == 0 {
		target = domain.DefaultTargetScore
	}

	w := wizardForm{
		focus:    fInput,
		input:    in,
		output:   out,
		minCRF:   newNumericInput("15", strconv.Itoa(minC)),
		maxCRF:   newNumericInput("36", strconv.Itoa(maxC)),
		target:   newNumericInput("95.0", strconv.FormatFloat(target, 'f', 1, 64)),
		audioIdx: indexOf(audioLabels(), string(opts.Audio)),
	}
	for _, t := range []*textinput.Model{&w.minCRF, &w.maxCRF, &w.target} {
		t.TextStyle = valueStyle
		t.PlaceholderStyle = dimStyle
	}
	for i, c := range codecChoices {
		if c == opts.Codec {
			w.codecIdx = i
			break
		}
	}
	// Presetリスト構築後、Options指定値が実在すればその位置へ（フラグとの整合維持）
	w.resetPresetList()
	if opts.Preset != "" {
		for i, p := range w.presetList {
			if p == opts.Preset {
				w.presetIdx = i
				break
			}
		}
	}
	for i, d := range depthChoices {
		if d == opts.BitDepth {
			w.depthIdx = i
			break
		}
	}
	w.metricIdx = indexOf(metricLabels(), string(opts.Metric))
	return w
}

// audioLabels は音声モードの実値一覧（選択肢テーブルの文字列表現）。
func audioLabels() []string {
	list := make([]string, len(audioChoices))
	for i, a := range audioChoices {
		list[i] = string(a)
	}
	return list
}

// metricLabels は合否指標の実値一覧。
func metricLabels() []string {
	list := make([]string, len(metricChoices))
	for i, m := range metricChoices {
		list[i] = string(m)
	}
	return list
}

func indexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return 0 // 未一致は先頭（既定）へ
}

func (w *wizardForm) isSelectField() bool {
	return w.focus == fCodec || w.focus == fPreset || w.focus == fMetric ||
		w.focus == fDepth || w.focus == fAudio
}

func (w *wizardForm) isTextField() bool { return !w.isSelectField() }

// preset / metric / depth / audio は現在選択中の実値を返す。
func (w *wizardForm) preset() string { return w.presetList[w.presetIdx] }
func (w *wizardForm) metric() domain.ScoreMetric {
	return metricChoices[w.metricIdx]
}
func (w *wizardForm) depth() int              { return depthChoices[w.depthIdx] }
func (w *wizardForm) audio() domain.AudioMode { return audioChoices[w.audioIdx] }

// resetPresetList は現在のCodecに応じてPresetリストを再構築し既定へ戻す。
// コーデック間でプリセット体系が異なるため、切替時の不正組合せを防ぐ。
func (w *wizardForm) resetPresetList() {
	w.presetList, w.presetIdx = presetChoicesFor(codecChoices[w.codecIdx])
}

// handleSetupKey はsetupフェーズのキー操作を処理する。
func (m Model) handleSetupKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	w := &m.wiz
	key := msg.String()

	switch key {
	case "ctrl+c", "esc":
		return m, tea.Quit
	case "q":
		// テキスト入力中の q は文字として扱う（終了はesc/ctrl+c専用）
		if !w.isTextField() {
			return m, tea.Quit
		}
	case "tab", "down":
		w.focus = (w.focus + 1) % fieldCount
		m.syncFocus()
		return m, nil
	case "shift+tab", "up":
		w.focus = (w.focus - 1 + fieldCount) % fieldCount
		m.syncFocus()
		return m, nil
	case "left":
		return m.stepField(-1)
	case "right":
		return m.stepField(+1)
	case "enter":
		return m.confirmWizard()
	}

	// テキストフィールドへのフォーカス時のみ入力を転送する
	var c tea.Cmd
	switch w.focus {
	case fInput:
		w.input, c = w.input.Update(msg)
	case fOutput:
		w.output, c = w.output.Update(msg)
	case fMinCRF:
		w.minCRF, c = w.minCRF.Update(msg)
	case fMaxCRF:
		w.maxCRF, c = w.maxCRF.Update(msg)
	case fTarget:
		w.target, c = w.target.Update(msg)
	}
	return m, c
}

// stepField は←→キーのフィールド別挙動。
// 選択系は値を循環、数値系はステップ加減算（CRF±1 / VMAF±0.5）、
// テキスト系はtextinputへカーソル移動を委譲する。
func (m Model) stepField(dir int) (Model, tea.Cmd) {
	w := &m.wiz
	switch w.focus {
	case fCodec:
		w.codecIdx = mod(w.codecIdx+dir, len(codecChoices))
		w.resetPresetList() // コーデック切替時はPresetを当該既定へリセット
	case fPreset:
		w.presetIdx = mod(w.presetIdx+dir, len(w.presetList))
	case fMetric:
		w.metricIdx = mod(w.metricIdx+dir, len(metricChoices))
	case fDepth:
		w.depthIdx = mod(w.depthIdx+dir, len(depthChoices))
	case fAudio:
		w.audioIdx = mod(w.audioIdx+dir, len(audioChoices))
	case fMinCRF:
		stepInt(&w.minCRF, dir, 0, 63)
	case fMaxCRF:
		stepInt(&w.maxCRF, dir, 0, 63)
	case fTarget:
		stepFloat(&w.target, float64(dir)*0.5, 50, 100)
	default: // fInput / fOutput: カーソル移動
		kt := tea.KeyRight
		if dir < 0 {
			kt = tea.KeyLeft
		}
		var c tea.Cmd
		if w.focus == fInput {
			w.input, c = w.input.Update(tea.KeyMsg{Type: kt})
		} else {
			w.output, c = w.output.Update(tea.KeyMsg{Type: kt})
		}
		return m, c
	}
	return m, nil
}

// syncFocus はフォーカス状態を全textinputへ反映する。
func (m *Model) syncFocus() {
	m.wiz.input.Blur()
	m.wiz.output.Blur()
	m.wiz.minCRF.Blur()
	m.wiz.maxCRF.Blur()
	m.wiz.target.Blur()
	switch m.wiz.focus {
	case fInput:
		m.wiz.input.Focus()
	case fOutput:
		m.wiz.output.Focus()
	case fMinCRF:
		m.wiz.minCRF.Focus()
	case fMaxCRF:
		m.wiz.maxCRF.Focus()
	case fTarget:
		m.wiz.target.Focus()
	}
}

func stepInt(t *textinput.Model, dir, lo, hi int) {
	v, err := strconv.Atoi(strings.TrimSpace(t.Value()))
	if err != nil {
		return // 不正入力は←→では直さず確定時の検証に委ねる
	}
	v += dir
	if v < lo {
		v = lo
	}
	if v > hi {
		v = hi
	}
	t.SetValue(strconv.Itoa(v))
}

func stepFloat(t *textinput.Model, delta, lo, hi float64) {
	v, err := strconv.ParseFloat(strings.TrimSpace(t.Value()), 64)
	if err != nil {
		return
	}
	v += delta
	if v < lo {
		v = lo
	}
	if v > hi {
		v = hi
	}
	t.SetValue(strconv.FormatFloat(v, 'f', 1, 64))
}

// buildConfig はフォーム内容からSearchConfigを組み立て検証する。
// 数値パース失敗や範囲外はドメインValidateと合わせてここでfail-fastする。
func (w *wizardForm) buildConfig() (domain.SearchConfig, error) {
	parseErr := func(field, val string) error {
		return fmt.Errorf("%s: invalid number %q", field, val)
	}
	minC, err := strconv.Atoi(strings.TrimSpace(w.minCRF.Value()))
	if err != nil {
		return domain.SearchConfig{}, parseErr("Min CRF", w.minCRF.Value())
	}
	maxC, err := strconv.Atoi(strings.TrimSpace(w.maxCRF.Value()))
	if err != nil {
		return domain.SearchConfig{}, parseErr("Max CRF", w.maxCRF.Value())
	}
	tgt, err := strconv.ParseFloat(strings.TrimSpace(w.target.Value()), 64)
	if err != nil {
		return domain.SearchConfig{}, parseErr("Target VMAF", w.target.Value())
	}
	// ウィザード入力は50〜100へ制限（memo.md A-5）。意味のある画質帯のみ受け付ける
	if tgt < 50 || tgt > 100 {
		return domain.SearchConfig{}, fmt.Errorf("Target VMAF %.1f out of range [50.0, 100.0]", tgt)
	}

	cfg := domain.SearchConfig{
		Codec:       codecChoices[w.codecIdx],
		MinCRF:      minC,
		MaxCRF:      maxC,
		TargetScore: tgt,
		Preset:      w.preset(),
		BitDepth:    w.depth(),
		Metric:      w.metric(),
	}
	if err := cfg.Validate(); err != nil {
		return domain.SearchConfig{}, err
	}
	return cfg, nil
}

// confirmWizard は入力内容を検証し、妥当ならパイプライン起動コマンドを返す。
// 検証結果（formErr / starting）は更新後のModelとして返す（値レシーバのため）。
func (m Model) confirmWizard() (Model, tea.Cmd) {
	if m.wiz.starting {
		return m, nil
	}
	// D&Dや「パスのコピー」では空白入りパスが二重引用符付きで張り付くため剥いてから扱う
	// （入力・出力の両欄を同様に正規化する）
	in := strings.Trim(strings.TrimSpace(m.wiz.input.Value()), `"`)
	out := strings.Trim(strings.TrimSpace(m.wiz.output.Value()), `"`)
	switch {
	case in == "":
		m.wiz.formErr = "入力ファイルを指定してください"
		return m, nil
	case !toolbin.FileExists(in):
		m.wiz.formErr = fmt.Sprintf("入力ファイルが見つかりません: %s", in)
		return m, nil
	}
	cfg, cerr := m.wiz.buildConfig()
	if cerr != nil {
		m.wiz.formErr = cerr.Error()
		return m, nil
	}
	m.wiz.formErr = ""
	m.wiz.starting = true
	audio := audioChoices[m.wiz.audioIdx]

	return m, func() tea.Msg {
		prep, err := m.factory(in, out, cfg, audio)
		if err != nil {
			return setupErrorMsg{err}
		}
		spawnPipeline(m.send, m.sessCtx, prep, cfg, m.workDir)
		return pipelineStartedMsg{input: prep.InputPath, output: prep.OutputPath}
	}
}

func mod(a, n int) int { return ((a % n) + n) % n }

// pipelineStartedMsg はパイプライン起動確定（既定出力名の解決済みパスを運ぶ）。
type pipelineStartedMsg struct {
	input  string
	output string
}

// setupErrorMsg はファクトリ失敗等のウィザード画面内エラー。
type setupErrorMsg struct{ err error }

// ===== setupフェーズの描画 =====

// renderSetup は設定ウィザード画面を描画する。
// 項目の並びはタブ移動順（fInput→fOutput）と一致させる。視覚的な小節キャプション
// （source / encode / output）は順序を変えない装飾として挟む。
func renderSetup(m Model) string {
	w := &m.wiz

	const labelW = 13
	row := func(label string, focused bool, content string) string {
		padded := fmt.Sprintf("%-*s", labelW, label)
		if content == "" && !focused {
			content = dimStyle.Render("(未設定)")
		}
		if focused {
			return accentBar() + focusLabelStyle.Render(padded) + " " + selActiveStyle.Render(content)
		}
		return "  " + labelStyle.Render(padded) + " " + valueStyle.Render(content)
	}

	var rows []string
	rows = append(rows, row("入力ファイル", w.focus == fInput, w.input.View()))
	rows = append(rows, "", sectionCaption("encode"))
	rows = append(rows,
		row("Codec:", w.focus == fCodec, selectValue(string(codecChoices[w.codecIdx]), w.focus == fCodec)),
		row("Preset:", w.focus == fPreset, selectValue(w.preset(), w.focus == fPreset)),
		row("Min CRF:", w.focus == fMinCRF, w.minCRF.View()),
		row("Max CRF:", w.focus == fMaxCRF, w.maxCRF.View()),
		row("Target VMAF:", w.focus == fTarget, w.target.View()),
		row("Metric:", w.focus == fMetric, selectValue(string(w.metric()), w.focus == fMetric)),
	)
	d := w.depth()
	rows = append(rows,
		row("Bit Depth:", w.focus == fDepth,
			selectValue(fmt.Sprintf("%d (%s)", d, depthLabels[d]), w.focus == fDepth)),
		row("Audio:", w.focus == fAudio, selectValue(string(audioChoices[w.audioIdx]), w.focus == fAudio)),
		"", sectionCaption("output"),
		row("Output:", w.focus == fOutput, w.output.View()),
	)

	body := strings.Join(rows, "\n")

	var b strings.Builder
	b.WriteString(brandHeader("セットアップ") + "\n\n")
	b.WriteString(titledPanel("設定", body) + "\n")

	if w.formErr != "" {
		b.WriteString("\n" + errorBox(w.formErr) + "\n")
	}

	b.WriteString("\n" + dimStyle.Render(
		"※ 既定値のままであれば従来の固定仕様と同一の挙動。Target VMAF は harmonic_mean 基準") + "\n")
	b.WriteString(strings.Join([]string{
		keyHint("Tab/↑↓", "項目移動"),
		keyHint("←→", "変更"),
		keyHint("Enter", "最適化開始"),
		keyHint("Esc", "終了"),
	}, "  ") + "\n")
	return b.String()
}

// selectValue は選択系フィールドの "< 値 >" 表現（矢印をアクセント色で強調）。
func selectValue(v string, focused bool) string {
	open, close := selectArrows(focused)
	return open + v + close
}
