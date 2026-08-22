// Package ui は bubbletea による最適化パイプラインのダッシュボード。
//
// 設計上の制約（memo.md「エンジン設計」）: engine は ui に依存しない。
// 本パッケージが engine.Orchestrator の進捗コールバックを tea.Msg へ変換して
// プログラムへ送り込むことで、双方向の疎結合を保つ。
package ui

import (
	"context"
	"io"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"engram-opt/internal/domain"
	"engram-opt/internal/engine"
)

// stage はアプリ全体の段階（memo.md「TUIウィザード化」）。
// setup（設定ウィザード）→ running（実行ダッシュボード）→ summary（完了サマリー）
// の3フェーズを1つのbubbleteaプログラム内で遷移する。
// running中の細分化状態は phase（detecting/optimizing/...）が担当する。
type stage int

const (
	stageSetup stage = iota
	stageRun
	stageSummary
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

	// started / dur はETA算出用（sceneStartで記録し、完了時に所要時間へ確定）。
	started time.Time
	dur     time.Duration
}

// Options はダッシュボードの表示に必要なメタ情報。
type Options struct {
	InputPath  string
	OutputPath string
	Codec      domain.VideoCodec
	Preset     string
	Target     float64

	// MinCRF / MaxCRF / BitDepth はウィザード初期値（memo.md「パラメータ一覧」A-3〜6）。
	// ゼロ値は「未指定」扱いでドメイン既定へフォールバックする。
	MinCRF   int
	MaxCRF   int
	BitDepth int

	// Metric は合否基準指標の表示用（空はharmonic扱い）。
	Metric string

	// EvalProfileName / OutRes はウィザード初期値用（フラグ由来。空は既定）。
	EvalProfileName string
	OutRes          string

	// Audio は音声モード表示用（空なら表示しない）。
	Audio string

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

	stage stage // アプリ全体の段階（setup / run / summary）

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

	// ===== ウィザード/パイプライン起動まわり（setup開始時に設定される） =====
	wiz     wizardForm      // setupフェーズのフォーム状態
	factory PipelineFactory // 設定確定時のOrchestrator組み立て（nil許容: 直実行モード）
	send    func(tea.Msg)   // prog.Send への委譲（runSession内で接続）
	sessCtx context.Context // パイプライン実行用ctx（q押下でcancelされる）
	workDir string          // 一時領域（jobDir）

	inSize  int64 // summary表示用の入力サイズ（完了時に取得）
	outSize int64 // summary表示用の出力サイズ
}

// NewModel は実行ダッシュボード（stageRun）として初期状態の Model を生成する。
func NewModel(opts Options) Model {
	return newModel(opts, stageRun)
}

// newModel は指定ステージで Model を初期化する。
func newModel(opts Options, st stage) Model {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = spinnerStyle
	pr := progress.New(progress.WithDefaultGradient())
	pr.Width = 44
	m := Model{
		opts:     opts,
		stage:    st,
		phase:    phaseDetecting,
		shots:    map[int]*shotState{},
		logCap:   8,
		progress: pr,
		spinner:  sp,
		started:  time.Now(),
		width:    80,
	}
	if st == stageSetup {
		m.wiz = newWizardForm(opts)
	}
	return m
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
