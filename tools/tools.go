//go:build tools

// Package tools は将来使う予定の依存ライブラリを go.mod に固定するためのアンカー。
// go mod tidy は全ビルドタグを有効化した状態で解析するため、このファイルがあると
// 未使用でも依存が削除されない。実際に使い始めたら対応する _ import を削除してよい。
//
// - cobra: 本体CLIのサブコマンド構成用
// - bubbletea / lipgloss / bubbles: TUIダッシュボード用
// - charmbracelet/log: コンソール用の整ったログ出力候補（採用は未決、無人実行時はslogも検討）
package tools

import (
	_ "github.com/charmbracelet/bubbles"
	_ "github.com/charmbracelet/bubbletea"
	_ "github.com/charmbracelet/lipgloss"
	_ "github.com/charmbracelet/log"
	_ "github.com/spf13/cobra"
)
