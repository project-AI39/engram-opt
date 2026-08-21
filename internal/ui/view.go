package ui

import (
	"fmt"
	"strings"
	"time"
)

// View はダッシュボードを描画する。
//
// レイアウト:
//
//	ヘッダ（入出力・設定）
//	全体進捗バー＋経過時間
//	シーン一覧テーブル
//	ログテール
//	フッタ
func (m Model) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("engram optimizer") + "  " +
		dimStyle.Render(shorten(m.opts.InputPath, 30)+" -> "+shorten(m.opts.OutputPath, 30)))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("codec=%s preset=%s target=%.1f", m.opts.Codec, m.opts.Preset, m.opts.Target))
	b.WriteString("\n\n")

	// 全体進捗
	b.WriteString(fmt.Sprintf("%-14s %s\n", m.phaseLabel(), m.progress.View()))
	b.WriteString(fmt.Sprintf("scenes %d/%d done · trials %d · elapsed %s\n",
		m.doneCount, maxInt(m.total, 0), m.trialCount, formatDuration(m.elapsed)))

	if m.phase == phaseFailed {
		b.WriteString("\n" + failStyle.Render("FAILED: "+errText(m.err)) + "\n")
	}

	// シーン一覧
	if m.total > 0 {
		b.WriteString("\n" + headerStyle.Render("SHOT  FRAMES       STATUS  CRF   VMAF(harm)  LAST") + "\n")
		for _, sc := range m.scenes {
			row := sceneRow{index: sc.Index, start: sc.StartFrame, end: sc.EndFrame}
			b.WriteString(m.renderRow(row) + "\n")
		}
	}

	// ログテール
	if len(m.logs) > 0 {
		b.WriteString("\n" + headerStyle.Render("-- log --") + "\n")
		for _, l := range m.logs {
			b.WriteString(dimStyle.Render(truncate(l, m.width-2)) + "\n")
		}
	}

	switch m.phase {
	case phaseDone:
		b.WriteString("\n" + hitStyle.Render("completed: "+m.opts.OutputPath) + "\n")
	case phaseFailed:
		b.WriteString("\n" + dimStyle.Render("temp files kept for inspection (see log above)") + "\n")
	default:
		b.WriteString("\n" + dimStyle.Render("[q] quit (running pipeline will be cancelled)") + "\n")
	}
	return b.String()
}

type sceneRow struct {
	index      int
	start, end int64
}

// renderRow は1シーン分の行を描画する。
// STATUS は状態アイコン、LAST は直近試行の合否（確定後は試行回数）を表示する。
func (m Model) renderRow(r sceneRow) string {
	st := m.shots[r.index]
	if st == nil {
		return ""
	}
	statusCell := pendStyle.Render(st.status.icon())
	crfCell := "-"
	harmCell := "-"
	lastCell := "-"

	switch st.status {
	case shotRunning:
		statusCell = runStyle.Render(st.status.icon())
		if st.last != nil {
			t := *st.last
			crfCell = fmt.Sprintf("%d", t.CRF)
			harmCell = fmt.Sprintf("%.2f", t.Metrics.HarmonicMean)
			if t.MetTarget {
				lastCell = hitStyle.Render("HIT")
			} else {
				lastCell = missStyle.Render("MISS")
			}
		}
	case shotDone:
		statusCell = hitStyle.Render(st.status.icon())
		res := st.result
		crfCell = fmt.Sprintf("%d", res.CRF)
		harmCell = fmt.Sprintf("%.2f", res.Metrics.HarmonicMean)
		lastCell = fmt.Sprintf("%d", res.Trials)
		if !res.MetTarget {
			statusCell = missStyle.Render(st.status.icon())
		}
	case shotFailed:
		statusCell = failStyle.Render(st.status.icon())
	}

	return fmt.Sprintf("%-5d %-11s  %-6s %-5s %-10s  %s",
		r.index,
		fmt.Sprintf("%d-%d", r.start, r.end),
		statusCell,
		crfCell,
		harmCell,
		lastCell,
	)
}

func (m Model) phaseLabel() string {
	label := "[" + m.phase.String() + "]"
	if m.phase == phaseDetecting || m.phase == phaseConcat || m.runningAny() {
		label += " " + m.spinner.View()
	}
	return label
}

func (m Model) runningAny() bool {
	for _, st := range m.shots {
		if st.status == shotRunning {
			return true
		}
	}
	return false
}

// shorten は長いパスを中略表示する。
func shorten(p string, max int) string {
	if len(p) <= max {
		return p
	}
	half := (max - 3) / 2
	return p[:half] + "..." + p[len(p)-half:]
}

// truncate は行を指定幅で打ち切る。
func truncate(s string, w int) string {
	if w <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= w {
		return s
	}
	return string(runes[:w-1]) + "~"
}

func formatDuration(d time.Duration) string {
	s := int(d.Seconds())
	h, mnt, sec := s/3600, (s%3600)/60, s%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, mnt, sec)
	}
	return fmt.Sprintf("%02d:%02d", mnt, sec)
}

// errText はエラーメッセージの1行目のみを返す（詳細はログ欄参照の設計）。
func errText(err error) string {
	if err == nil {
		return "unknown"
	}
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return msg
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
