package ui

// デザインシステム（全画面共通のテーマと部品）。
//
// 方針:
//   - 色はここで一定義。各ビューは意味名のスタイルのみを参照する
//   - lipgloss は実行環境のカラー能力へ自動フォールバックするため、
//     truecolor指定でも非TTY（テスト/パイプ）では素のテキストとして崩れない
//   - 「幅計算してから色を塗る」を徹底する（先に %-Ns パディング→Render。
//     逆だとANSIエスケープがパディング幅に算入され桁が揃わなくなる）

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ===== パレット =====

var (
	cPrimary = lipgloss.Color("#7D56F4") // バイオレット: ブランド/フォーカス
	cCyan    = lipgloss.Color("#00C2D1") // シアン: 進行中/情報
	cGreen   = lipgloss.Color("#04B575") // 成功/HIT
	cAmber   = lipgloss.Color("#FFA630") // 警告/MISS
	cRed     = lipgloss.Color("#FF4672") // 失敗/エラー
	cText    = lipgloss.Color("#E2E2E8") // 明文字（見出し等）
	cMuted   = lipgloss.Color("#7A7A93") // 補助文字
	cBorder  = lipgloss.Color("#3A3A55") // パネル枠
	cAccent2 = lipgloss.Color("#2CC6A8") // ティール: サブアクセント
)

// ===== 意味スタイル =====

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(cPrimary)
	dimStyle    = lipgloss.NewStyle().Foreground(cMuted)
	hitStyle    = lipgloss.NewStyle().Bold(true).Foreground(cGreen)
	missStyle   = lipgloss.NewStyle().Bold(true).Foreground(cAmber)
	runStyle    = lipgloss.NewStyle().Bold(true).Foreground(cCyan)
	pendStyle   = lipgloss.NewStyle().Foreground(cMuted)
	failStyle   = lipgloss.NewStyle().Bold(true).Foreground(cRed)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(cMuted)

	labelStyle      = lipgloss.NewStyle().Foreground(cMuted)                    // フォーム項目名（非フォーカス）
	focusLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(cPrimary)       // フォーカス項目名
	valueStyle      = lipgloss.NewStyle().Foreground(cText)                     // 通常値
	selActiveStyle  = lipgloss.NewStyle().Bold(true).Foreground(cText)          // フォーカス中の選択値
	errBoxStyle     = lipgloss.NewStyle().Foreground(cRed)                      // エラー本文
	chipKeyStyle    = lipgloss.NewStyle().Foreground(cMuted)                    // chip のキー部
	chipValStyle    = lipgloss.NewStyle().Bold(true).Foreground(cAccent2)       // chip の値部
	logStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF")) // ログ行
	spinnerStyle    = lipgloss.NewStyle().Foreground(cCyan)
)

// ===== 部品 =====

// brandHeader はアプリ名＋サブタイトルのヘッダ行を返す。
func brandHeader(sub string) string {
	bar := lipgloss.NewStyle().Bold(true).Foreground(cPrimary).Render("▌")
	name := lipgloss.NewStyle().
		Bold(true).
		Foreground(cText).
		Render("engram")
	subStyled := lipgloss.NewStyle().Foreground(cPrimary).Render(sub)
	return lipgloss.JoinHorizontal(lipgloss.Center,
		bar, " ", name, dimStyle.Render(" · "), subStyled)
}

// titledPanel は上枠に埋め込まれたタイトル付き角丸パネル。
func titledPanel(title, content string) string {
	body := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), false, true, true, true).
		BorderForeground(cBorder).
		Padding(0, 1).
		Render(content)
	top := lipgloss.NewStyle().Foreground(cBorder).Render("╭") +
		lipgloss.NewStyle().Bold(true).Foreground(cPrimary).Render(" "+title+" ") +
		lipgloss.NewStyle().Foreground(cBorder).Render(strings.Repeat("─", maxInt(0, lipgloss.Width(body)-lipgloss.Width(title)-4))+"╮")
	return top + "\n" + body
}

// plainPanel はタイトル無しの角丸パネル。
func plainPanel(content string) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1).
		Render(content)
}

// chip は "key=value" 形式の小さな情報片。
func chip(key, value string) string {
	return chipKeyStyle.Render(key) + chipValStyle.Render(value)
}

// keyHint はフッター用のキー操作ガイド1個分（"[Tab] 移動" 形式）。
func keyHint(k, desc string) string {
	bracket := lipgloss.NewStyle().Foreground(cBorder)
	kb := bracket.Render("[") + lipgloss.NewStyle().Bold(true).Foreground(cText).Render(k) + bracket.Render("]")
	return kb + " " + dimStyle.Render(desc)
}

// statBlock はサマリー用の統計カード（ラベル+強調値の縦並びミニパネル）。
func statBlock(label, value string) string {
	head := lipgloss.NewStyle().Foreground(cMuted).Render(label)
	body := lipgloss.NewStyle().Bold(true).Foreground(cText).Render(value)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1).
		Render(head + "\n" + body)
}

// cell は固定幅にパディングしてから着色する（桁揃えの要）。
func cell(w int, s string, st lipgloss.Style) string {
	return st.Render(fmt.Sprintf("%-*s", w, s))
}

// divider は横罫線。
func divider(width int) string {
	return lipgloss.NewStyle().Foreground(cBorder).Render(strings.Repeat("─", maxInt(0, width)))
}

// phaseBadge は進行フェーズの色付きバッジ。
func phaseBadge(p phase) string {
	base := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	var st lipgloss.Style
	switch p {
	case phaseDetecting:
		st = base.Foreground(lipgloss.Color("#0B1020")).Background(cCyan)
	case phaseOptimizing:
		st = base.Foreground(lipgloss.Color("#0B1020")).Background(cPrimary)
	case phaseConcat:
		st = base.Foreground(lipgloss.Color("#0B1020")).Background(cAccent2)
	case phaseDone:
		st = base.Foreground(lipgloss.Color("#0B1020")).Background(cGreen)
	default:
		st = base.Foreground(lipgloss.Color("#0B1020")).Background(cRed)
	}
	return st.Render(" " + p.String() + " ")
}

// errorBox は赤枠のエラーボックス（ウィザードのインラインエラー等）。
func errorBox(msg string) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cRed).
		Padding(0, 1).
		Render(errBoxStyle.Render("✕ ") + valueStyle.Render(msg))
}

// sectionCaption はフォーム内の小節キャプション。
func sectionCaption(t string) string {
	return lipgloss.NewStyle().Foreground(cAccent2).Render("──") +
		lipgloss.NewStyle().Bold(true).Foreground(cMuted).Render(" "+t+" ")
}

// accentBar はフォーカス行の左アクセントバー。
func accentBar() string {
	return lipgloss.NewStyle().Bold(true).Foreground(cPrimary).Render("▌ ")
}

// selectArrows は選択系フィールドの "< >" 矢印（フォーカス時はシアン強調）。
func selectArrows(focused bool) (string, string) {
	st := lipgloss.NewStyle().Foreground(cPrimary)
	if focused {
		st = lipgloss.NewStyle().Bold(true).Foreground(cCyan)
	}
	return st.Render("< "), st.Render(" >")
}
