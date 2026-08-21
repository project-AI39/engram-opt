// Package ui は bubbletea による最適化パイプラインのダッシュボード。
//
// 設計上の制約（memo.md「エンジン設計」）: engine は ui に依存しない。
// 本パッケージが engine.Orchestrator の進捗コールバックを tea.Msg へ変換して
// プログラムへ送り込むことで、双方向の疎結合を保つ。
package ui

import (
	"io"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"engram-opt/internal/domain"
	"engram-opt/internal/engine"
)

// phase はパイプライン全体の段階。
type phase int

const (
	phaseDetecting phase = iota
	phaseOptimizing
	phaseConcat // 全シーン確定後、結合〜完了通知待ち
	phaseDone
	phaseFailed
)

func (p phase) String() string {
	switch p {
	case phaseDetecting:
		return "detecting"
	case phaseOptimizing:
		return "optimizing"
	case phaseConcat:
		return "concatenating"
	case phaseDone:
		return "done"
	default:
		return "failed"
	}
}

// shotStatus は単一シーンの処理状態。
type shotStatus int

const (
	shotPending shotStatus = iota
	shotRunning
	shotDone
	shotFailed
)

func (s shotStatus) icon() string {
	switch s {
	case shotPending:
		return "·"
	case shotRunning:
		return ">"
	case shotDone:
		return "+"
	default:
		return "x"
	}
}

type shotState struct {
	status shotStatus
	last   *engine.Trial
	result *engine.Result
}

// Options はダッシュボードの表示に必要なメタ情報。
type Options struct {
	InputPath  string
	OutputPath string
	Codec      domain.VideoCodec
	Preset     string
	Target     float64

	// LogMirror はTUI表示中も log 出力を複製する書き込み先（--log-file 用）。
	// nil許容。stderr への迂回は画面破壊になるため、呼び出し側がファイル等を渡す。
	LogMirror io.Writer
}

// ===== tea.Msg 定義（engine コールバックの変換先） =====

type detectionDoneMsg []domain.Scene
type sceneStartMsg struct{ index int }
type trialMsg engine.Trial
type sceneDoneMsg struct {
	index  int
	result *engine.Result
}
type logLineMsg string
type tickMsg struct{}
type pipelineErrMsg struct{ err error }
type pipelineDoneMsg *engine.PipelineReport

// Model はダッシュボードの状態。
type Model struct {
	opts Options

	phase    phase
	err      error
	report   *engine.PipelineReport
	finished bool // パイプラインgoroutineが正常終了したか（結果の有無判定に使用）

	scenes     []domain.Scene
	total      int
	shots      map[int]*shotState
	doneCount  int
	trialCount int

	logs   []string // 直近のログ行（リングバッファ）
	logCap int

	progress progress.Model
	spinner  spinner.Model

	started time.Time
	elapsed time.Duration
	width   int
}

// NewModel は初期状態の Model を生成する。
func NewModel(opts Options) Model {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	pr := progress.New(progress.WithDefaultGradient())
	pr.Width = 44
	return Model{
		opts:     opts,
		phase:    phaseDetecting,
		shots:    map[int]*shotState{},
		logCap:   8,
		progress: pr,
		spinner:  sp,
		started:  time.Now(),
		width:    80,
	}
}

// Init はタイマーとスピナーを起動する。
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} }),
	)
}

// pushLog はログ行をリングバッファへ追加する。
func (m *Model) pushLog(line string) {
	m.logs = append(m.logs, line)
	if len(m.logs) > m.logCap {
		m.logs = m.logs[len(m.logs)-m.logCap:]
	}
}

// ===== スタイル（view.go と共有） =====

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	dimStyle    = lipgloss.NewStyle().Faint(true)
	hitStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	missStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	runStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	pendStyle   = lipgloss.NewStyle().Faint(true)
	failStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245"))
)
