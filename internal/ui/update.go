package ui

import (
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"engram-opt/internal/engine"
)

// Update はメッセージに応じて状態を遷移させる。
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.progress.Width = min(msg.Width-16, 60)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case progress.FrameMsg:
		pm, _ := m.progress.Update(msg)
		m.progress = pm.(progress.Model)
		return m, nil

	case tickMsg:
		m.elapsed = time.Since(m.started)
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })

	case detectionDoneMsg:
		m.phase = phaseOptimizing
		m.scenes = msg
		m.total = len(msg)
		for _, sc := range msg {
			m.shots[sc.Index] = &shotState{status: shotPending}
		}
		return m, nil

	case sceneStartMsg:
		if st, ok := m.shots[msg.index]; ok {
			st.status = shotRunning
		}
		return m, nil

	case trialMsg:
		t := engine.Trial(msg)
		m.trialCount++
		if st, ok := m.shots[t.Scene.Index]; ok {
			last := t
			st.last = &last
		}
		return m, nil

	case sceneDoneMsg:
		if st, ok := m.shots[msg.index]; ok {
			st.status = shotDone
			st.result = msg.result
		}
		m.doneCount++
		cmd := m.progress.SetPercent(float64(m.doneCount) / float64(m.total))
		// 全シーン確定 → 結合フェード表示（完了通知までは concat 扱い）
		if m.doneCount == m.total {
			m.phase = phaseConcat
		}
		return m, cmd

	case logLineMsg:
		m.pushLog(string(msg))
		return m, nil

	case pipelineDoneMsg:
		m.report = msg
		m.finished = true
		m.elapsed = time.Since(m.started)
		m.phase = phaseDone
		_ = m.progress.SetPercent(1.0)
		return m, tea.Quit

	case pipelineErrMsg:
		m.err = msg.err
		for _, st := range m.shots {
			if st.status == shotRunning {
				st.status = shotFailed
			}
		}
		m.phase = phaseFailed
		return m, tea.Quit
	}
	return m, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
