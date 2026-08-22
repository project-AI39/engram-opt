package ui

// ウィザード（setupステージ）の実装。
// 設定項目は既存CLIフラグと同じ範囲のみ（memo.md「TUIウィザード化」）:
// 入力ファイル / コーデック / プリセット / 音声 / 出力先。
// 探索仕様（CRF 15〜36 / 目標95.0）は編集不可のため画面下部に固定値として出す。

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// ===== 選択肢テーブル =====

var codecChoices = []domain.VideoCodec{domain.CodecH264, domain.CodecHEVC, domain.CodecAV1}

var presetChoices = []struct {
	label string
	value string
}{
	{"高圧縮(低速)", "slow"},
	{"バランス", "medium"},
	{"高速", "fast"},
}

var audioChoices = []domain.AudioMode{
	domain.AudioCopy,
	domain.AudioOpus,
	domain.AudioAAC,
	domain.AudioNone,
}

var audioLabels = map[domain.AudioMode]string{
	domain.AudioCopy: "copy (無劣化)",
	domain.AudioOpus: "opus (再圧縮)",
	domain.AudioAAC:  "aac (再圧縮)",
	domain.AudioNone: "none (音声なし)",
}

// フォーカス対象のフィールド番号
const (
	fInput = iota
	fCodec
	fPreset
	fAudio
	fOutput
	fieldCount
)

// wizardForm はsetupフェーズのフォーム状態。
type wizardForm struct {
	focus     int
	input     textinput.Model
	output    textinput.Model
	codecIdx  int
	presetIdx int
	audioIdx  int
	formErr   string
	starting  bool // Enter連打による二重起動防止
}

func newWizardForm(opts Options) wizardForm {
	in := textinput.New()
	in.Placeholder = "例: C:\\Videos\\input.mp4 （D&Dでパスを貼り付け可）"
	in.Prompt = ""
	in.Focus()

	out := textinput.New()
	out.Placeholder = "空欄= <入力名>.opt.mkv"
	out.Prompt = ""

	w := wizardForm{focus: fInput, input: in, output: out}
	// フラグ由来の初期値を反映
	if opts.InputPath != "" {
		w.input.SetValue(opts.InputPath)
	}
	if opts.OutputPath != "" {
		w.output.SetValue(opts.OutputPath)
	}
	for i, c := range codecChoices {
		if c == opts.Codec {
			w.codecIdx = i
		}
	}
	for i, p := range presetChoices {
		if p.value == opts.Preset {
			w.presetIdx = i
		}
	}
	for i, a := range audioChoices {
		if a == domain.AudioMode(opts.Audio) {
			w.audioIdx = i
		}
	}
	return w
}

// handleSetupKey はsetupフェーズのキー操作を処理する。
// 戻り値のcmdはパイプライン起動（Enter確定時）。handled=true の場合は
// 呼び出し側が以後のデフォルト処理（textinputへの転送等）をしない。
func (m Model) handleSetupKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	w := &m.wiz
	key := msg.String()

	switch key {
	case "ctrl+c", "esc":
		return m, tea.Quit
	case "q":
		// テキスト入力中の q は文字として扱う（終了はesc/ctrl+c専用）
		if w.focus != fInput && w.focus != fOutput {
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
	case "left", "h":
		if w.isSelectField() {
			w.cycle(-1)
			return m, nil
		}
	case "right", "l":
		if w.isSelectField() {
			w.cycle(+1)
			return m, nil
		}
	case "enter":
		return m.confirmWizard()
	}

	// テキストフィールドへのフォーカス時のみ入力を転送する
	if !w.isSelectField() {
		if w.focus == fInput {
			var c tea.Cmd
			w.input, c = w.input.Update(msg)
			return m, c
		}
		var c tea.Cmd
		w.output, c = w.output.Update(msg)
		return m, c
	}
	return m, nil
}

func (w *wizardForm) isSelectField() bool {
	return w.focus == fCodec || w.focus == fPreset || w.focus == fAudio
}

// codec / audio は現在選択中の値を返す（テストと描画で使用）。
func (w *wizardForm) codec() domain.VideoCodec { return codecChoices[w.codecIdx] }
func (w *wizardForm) audio() domain.AudioMode  { return audioChoices[w.audioIdx] }

// cycle は選択系フィールドの値を循環させる。
func (w *wizardForm) cycle(dir int) {
	switch w.focus {
	case fCodec:
		w.codecIdx = mod(w.codecIdx+dir, len(codecChoices))
	case fPreset:
		w.presetIdx = mod(w.presetIdx+dir, len(presetChoices))
	case fAudio:
		w.audioIdx = mod(w.audioIdx+dir, len(audioChoices))
	}
}

// syncFocus はフォーカス状態をtextinputへ反映する。
func (m *Model) syncFocus() {
	m.wiz.input.Blur()
	m.wiz.output.Blur()
	switch m.wiz.focus {
	case fInput:
		m.wiz.input.Focus()
	case fOutput:
		m.wiz.output.Focus()
	}
}

// confirmWizard は入力内容を検証し、妥当ならパイプライン起動コマンドを返す。
// 検証結果（formErr / starting）は更新後のModelとして返す（値レシーバのため）。
func (m Model) confirmWizard() (Model, tea.Cmd) {
	if m.wiz.starting {
		return m, nil
	}
	in := strings.TrimSpace(m.wiz.input.Value())
	switch {
	case in == "":
		m.wiz.formErr = "入力ファイルを指定してください"
		return m, nil
	case !toolbin.FileExists(in):
		m.wiz.formErr = fmt.Sprintf("入力ファイルが見つかりません: %s", in)
		return m, nil
	}
	m.wiz.formErr = ""
	m.wiz.starting = true

	cfg := domain.SearchConfig{
		Codec:       codecChoices[m.wiz.codecIdx],
		MinCRF:      domain.DefaultMinCRF,
		MaxCRF:      domain.DefaultMaxCRF,
		TargetScore: m.opts.Target,
		Preset:      presetChoices[m.wiz.presetIdx].value,
	}
	audio := audioChoices[m.wiz.audioIdx]

	return m, func() tea.Msg {
		prep, err := m.factory(in, strings.TrimSpace(m.wiz.output.Value()), cfg, audio)
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

var (
	boxStyle       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	selActiveStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	errBoxStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

// renderSetup は設定ウィザード画面を描画する。
func renderSetup(m Model) string {
	w := &m.wiz

	row := func(label string, focused bool, content string) string {
		cursor := "  "
		if focused {
			cursor = "> "
			content = selActiveStyle.Render(content)
		} else if content == "" {
			content = dimStyle.Render("(未設定)")
		}
		return fmt.Sprintf(" %-12s%s %s", label, cursor, content)
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("engram opt - エンコード設定") + "\n\n")

	b.WriteString(row("入力ファイル:", w.focus == fInput, w.input.View()))
	b.WriteString("\n")
	b.WriteString(row("コーデック:", w.focus == fCodec,
		fmt.Sprintf("< %s >", string(codecChoices[w.codecIdx]))))
	b.WriteString("\n")
	b.WriteString(row("プリセット:", w.focus == fPreset,
		fmt.Sprintf("< %s >", presetChoices[w.presetIdx].label)))
	b.WriteString("\n")
	b.WriteString(row("音声:", w.focus == fAudio,
		fmt.Sprintf("< %s >", audioLabels[audioChoices[w.audioIdx]])))
	b.WriteString("\n")
	b.WriteString(row("出力先:", w.focus == fOutput, w.output.View()))
	b.WriteString("\n")

	if w.formErr != "" {
		b.WriteString("\n" + errBoxStyle.Render("× "+w.formErr) + "\n")
	}

	b.WriteString("\n" + dimStyle.Render(
		"探索仕様（固定）: CRF 15-36 二分探索 / VMAF harmonic_mean >= 95.0") + "\n")
	b.WriteString(dimStyle.Render(
		"[Tab/↑↓] 項目移動  [←→] 選択  [Enter] 最適化開始  [Esc/Ctrl+C] 終了") + "\n")
	return b.String()
}
