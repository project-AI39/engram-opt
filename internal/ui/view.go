package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"engram-opt/internal/domain"
	"engram-opt/internal/engine"
)

// View は現在のステージに応じた画面を描画する。
//
//	setup   : 設定ウィザード（wizard.go）
//	running : 実行ダッシュボード（下記レイアウト）
//	summary : 完了サマリー
func (m Model) View() string {
	switch m.stage {
	case stageSetup:
		return renderSetup(m)
	case stageSummary:
		return m.renderSummary()
	default:
		return m.renderDashboard()
	}
}

// metricShort は表示用の指標短縮名（列ヘッダ等）。
func (m Model) metricShort() string {
	return shortMetricName(m.opts.Metric)
}

// shortMetricName は ScoreMetric 文字列の短縮名（harmonic→harm）。
func shortMetricName(s string) string {
	switch domain.ScoreMetric(s) {
	case domain.MetricMean:
		return "mean"
	case domain.MetricMin:
		return "min"
	default:
		return "harm"
	}
}

// ===== 実行ダッシュボード =====

// renderDashboard は実行中ダッシュボードを描画する。
//
//	ヘッダ（ブランド＋フェーズバッジ＋スピナー）
//	設定パネル（in/out パスとエンコード構成のチップ）
//	進捗バー＋統計チップ
//	シーン一覧テーブル
//	ログパネル
//	フッタ
func (m Model) renderDashboard() string {
	var b strings.Builder

	// ヘッダ
	head := lipgloss.JoinHorizontal(lipgloss.Center,
		brandHeader("optimizer"), "  ", m.phaseBadge())
	b.WriteString(head + "\n")

	// 設定パネル
	cfgChips := []string{
		chip("codec ", string(m.opts.Codec)),
		chip("preset ", m.opts.Preset),
		chip("target ", fmt.Sprintf("%.1f", m.opts.Target)),
	}
	if m.opts.BitDepth != 0 {
		cfgChips = append(cfgChips, chip("depth ", fmt.Sprintf("%d-bit", m.opts.BitDepth)))
	}
	if m.opts.Audio != "" {
		cfgChips = append(cfgChips, chip("audio ", m.opts.Audio))
	}
	info := lipgloss.JoinVertical(lipgloss.Left,
		chipKeyStyle.Render("in ")+valueStyle.Render(shorten(m.opts.InputPath, 42))+
			dimStyle.Render("  →  ")+
			chipKeyStyle.Render("out ")+valueStyle.Render(shorten(m.opts.OutputPath, 42)),
		strings.Join(cfgChips, dimStyle.Render(" · ")),
	)
	b.WriteString(plainPanel(info) + "\n\n")

	// 失敗バナー（パイプライン継続中にエラー確定した場合）
	if m.phase == phaseFailed {
		failBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cRed).
			Padding(0, 1).
			Render(failStyle.Render("✗ FAILED  ") + valueStyle.Render(errText(m.err)))
		b.WriteString(failBox + "\n\n")
	}

	// 進捗バー＋統計
	b.WriteString(m.progress.View() + "\n")
	statLine := strings.Join([]string{
		chip("shots ", fmt.Sprintf("%d/%d", m.doneCount, maxInt(m.total, 0))),
		chip("trials ", fmt.Sprint(m.trialCount)),
		chip("elapsed ", formatDuration(m.elapsed)),
	}, dimStyle.Render("  "))
	b.WriteString(statLine + "\n")

	// シーン一覧
	if m.total > 0 {
		b.WriteString("\n" + m.sectionTitle("SHOTS") + "\n")
		b.WriteString(m.dashHeader() + "\n")
		for _, sc := range m.scenes {
			row := sceneRow{index: sc.Index, start: sc.StartFrame, end: sc.EndFrame}
			b.WriteString(m.renderRow(row) + "\n")
		}
	}

	// ログパネル
	if len(m.logs) > 0 {
		lines := make([]string, 0, len(m.logs))
		for _, l := range m.logs {
			lines = append(lines, logStyle.Render(truncate(l, maxInt(10, m.width-6))))
		}
		b.WriteString("\n" + titledPanel("log", strings.Join(lines, "\n")) + "\n")
	}

	// フッタ
	switch m.phase {
	case phaseDone:
		b.WriteString("\n" + hitStyle.Render("✓ completed: ") + valueStyle.Render(m.opts.OutputPath) + "\n")
	case phaseFailed:
		b.WriteString("\n" + dimStyle.Render("temp files kept for inspection (see log above)") + "\n")
	default:
		b.WriteString("\n" + keyHint("q", "quit (running pipeline will be cancelled)") + "\n")
	}
	return b.String()
}

type sceneRow struct {
	index      int
	start, end int64
}

// ダッシュボードテーブルの列幅（ヘッダと行で共有）。
// 各幅に列間ギャップを含めるため、結合はセパレータ無しで行う。
type tableCol struct {
	w     int
	label string
}

// dashCols は実行テーブルの列定義。VMAF列は選択中の合否指標名を反映する。
func (m Model) dashCols() []tableCol {
	return []tableCol{
		{7, "SHOT"}, {14, "FRAMES"}, {8, "STATUS"}, {6, "CRF"},
		{12, fmt.Sprintf("VMAF(%s)", m.metricShort())}, {6, "LAST"},
	}
}

func renderTableHeader(cols []tableCol) string {
	cells := make([]string, 0, len(cols))
	for _, c := range cols {
		cells = append(cells, fmt.Sprintf("%-*s", c.w, c.label))
	}
	return headerStyle.Render(strings.Join(cells, ""))
}

// dashHeader は実行テーブルの見出し行（指標名を含むためModelが必要）。
func (m Model) dashHeader() string { return renderTableHeader(m.dashCols()) } // renderRow は1シーン分の行を描画する。
// STATUS は状態アイコン、LAST は直近試行の合否（確定後は試行回数）を表示する。
// 各セルは「先に固定幅へパディングしてから着色」する——逆順だとANSIエスケープが
// パディング幅に算入され、色違いのセルで桁が揃わなくなる。
func (m Model) renderRow(r sceneRow) string {
	st := m.shots[r.index]
	if st == nil {
		return ""
	}

	statusCell := cell(8, st.status.icon(), pendStyle)
	crfCell := cell(6, "-", pendStyle)
	harmCell := cell(12, "-", pendStyle)
	lastCell := cell(6, "-", pendStyle)
	idxCell := cell(7, fmt.Sprint(r.index), valueStyle)
	framesCell := cell(14, fmt.Sprintf("%d-%d", r.start, r.end), dimStyle)

	switch st.status {
	case shotPending:
		framesCell = cell(14, fmt.Sprintf("%d-%d", r.start, r.end), pendStyle)
	case shotRunning:
		statusCell = cell(8, st.status.icon(), runStyle)
		idxCell = cell(7, fmt.Sprint(r.index), runStyle)
		if st.last != nil {
			t := *st.last
			crfCell = cell(6, fmt.Sprint(t.CRF), valueStyle)
			harmCell = cell(12, fmt.Sprintf("%.2f", t.Metrics.HarmonicMean), valueStyle)
			if t.MetTarget {
				lastCell = cell(6, "HIT", hitStyle)
			} else {
				lastCell = cell(6, "MISS", missStyle)
			}
		}
	case shotDone:
		res := st.result
		metColor := hitStyle
		if !res.MetTarget {
			metColor = missStyle
		}
		statusCell = cell(8, st.status.icon(), metColor)
		crfCell = cell(6, fmt.Sprint(res.CRF), valueStyle)
		harmCell = cell(12, fmt.Sprintf("%.2f", res.Metrics.HarmonicMean), metColor)
		lastCell = cell(6, fmt.Sprint(res.Trials), dimStyle)
	case shotFailed:
		statusCell = cell(8, st.status.icon(), failStyle)
	}

	return strings.Join([]string{idxCell, framesCell, statusCell, crfCell, harmCell, lastCell}, "")
}

// sectionTitle は「▌ LABEL ─────」形式の節見出し。
func (m Model) sectionTitle(label string) string {
	w := maxInt(0, m.width-lipgloss.Width(label)-4)
	bar := lipgloss.NewStyle().Bold(true).Foreground(cPrimary).Render("▌ ")
	lbl := lipgloss.NewStyle().Bold(true).Foreground(cMuted).Render(label + " ")
	rule := lipgloss.NewStyle().Foreground(cBorder).Render(strings.Repeat("─", w))
	return bar + lbl + rule
}

// phaseBadge は進行フェーズの色付きバッジ（稼働中はスピナーを伴う）。
func (m Model) phaseBadge() string {
	badge := phaseBadge(m.phase)
	switch {
	case m.phase == phaseDetecting || m.phase == phaseConcat || m.runningAny():
		return badge + " " + m.spinner.View()
	default:
		return badge
	}
}

func (m Model) runningAny() bool {
	for _, st := range m.shots {
		if st.status == shotRunning {
			return true
		}
	}
	return false
}

// ===== 共通ユーティリティ =====

// shorten は長いパスを中略表示する。
// 日本語パス等のマルチバイト文字をバイト境界で切断して文字化けさせないため、
// ルーン単位で数える（truncate と同じ規約）。
func shorten(p string, max int) string {
	runes := []rune(p)
	if len(runes) <= max {
		return p
	}
	half := (max - 3) / 2
	// max 極小時に half<=0 へ倒れ込むと負/零インデックス参照になるため打ち切りへフォールバック
	if half <= 0 {
		return truncate(p, max)
	}
	return string(runes[:half]) + "..." + string(runes[len(runes)-half:])
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

// ===== 完了サマリー =====

// renderSummary は完了サマリーを描画する（memo.md「TUIウィザード化」）。
// 成功バナー、統計カード、シーン別採用CRF一覧、出力先を表示しキー入力で待つ。
func (m Model) renderSummary() string {
	r := m.report
	var b strings.Builder

	b.WriteString(brandHeader("summary") + "\n\n")

	// 成功バナー
	banner := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cGreen).
		Padding(0, 2).
		Render(lipgloss.NewStyle().Bold(true).Foreground(cGreen).Render("✓ 処理が完了しました"))
	b.WriteString(banner + "\n\n")

	// 統計カード（サイズ / 達成 / 実行）
	cards := []string{}
	if m.inSize > 0 && m.outSize > 0 {
		delta := 100 * (1 - float64(m.outSize)/float64(m.inSize))
		sign, color := "-", cGreen
		if delta < 0 {
			// 大きくなった場合は増加として正直に表示する
			sign, color = "+", cAmber
		}
		sizeVal := lipgloss.NewStyle().Bold(true).Foreground(color).
			Render(fmt.Sprintf("%s%.1f%%", sign, delta))
		detail := dimStyle.Render(fmt.Sprintf("%.1f→%.1f MB",
			float64(m.inSize)/(1<<20), float64(m.outSize)/(1<<20)))
		cards = append(cards, statBlock("SIZE", sizeVal+"  "+detail))
	}
	if r != nil && len(r.Results) > 0 {
		met := 0
		for _, res := range r.Results {
			if res.MetTarget {
				met++
			}
		}
		metVal := hitStyle.Render(fmt.Sprintf("%d", met)) +
			dimStyle.Render(fmt.Sprintf("/%d shots", len(r.Results)))
		cards = append(cards, statBlock("QUALITY", metVal))
	}
	runVal := chipValStyle.Render(fmt.Sprint(totalTrials(r, m.trialCount))) +
		dimStyle.Render(" trials · ") + valueStyle.Render(formatDuration(m.elapsed))
	cards = append(cards, statBlock("RUN", runVal))

	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, cards...) + "\n")

	// 出力先
	if r != nil {
		b.WriteString("\n" + chipKeyStyle.Render("output ") + valueStyle.Render(r.OutputPath) + "\n")
	}

	// 結果テーブル
	if r != nil && len(r.Results) > 0 {
		metric := domain.MetricHarmonic
		if r.Metric != "" {
			metric = r.Metric
		} else if m.opts.Metric != "" {
			metric = domain.ScoreMetric(m.opts.Metric)
		}
		b.WriteString("\n" + m.sectionTitle("RESULTS") + "\n")
		cols := []tableCol{
			{7, "SHOT"}, {14, "FRAMES"}, {6, "CRF"},
			{12, fmt.Sprintf("VMAF(%s)", shortMetricName(string(metric)))}, {6, "MET"},
		}
		b.WriteString(renderTableHeader(cols) + "\n")
		for _, res := range r.Results {
			metSt := missStyle
			metTxt := "MISS"
			if res.MetTarget {
				metSt = hitStyle
				metTxt = "HIT "
			}
			row := strings.Join([]string{
				cell(7, fmt.Sprint(res.Scene.Index), valueStyle),
				cell(14, fmt.Sprintf("%d-%d", res.Scene.StartFrame, res.Scene.EndFrame), dimStyle),
				cell(6, fmt.Sprint(res.CRF), valueStyle),
				cell(12, fmt.Sprintf("%.2f", res.Metrics.Score(metric)), valueStyle),
				cell(6, metTxt, metSt),
			}, "")
			b.WriteString(row + "\n")
		}
	} else {
		b.WriteString(fmt.Sprintf("\n%s %s\n",
			chip("trials ", fmt.Sprint(m.trialCount)),
			chip("elapsed ", formatDuration(m.elapsed))))
	}

	b.WriteString("\n" + keyHint("Enter", "終了") + "  " + keyHint("q", "終了") + "\n")
	return b.String()
}

// totalTrials はレポートがあればその合計試行数を、無ければ実測カウントを返す。
func totalTrials(r *engine.PipelineReport, fallback int) int {
	if r != nil {
		return r.TotalTrials
	}
	return fallback
}
