// Package cli は cobra によるコマンド定義を集約する。
// 各コマンドは引数解析と委譲のみを行い、実際の処理は internal 配下の各パッケージに置く。
package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCmd は engram コマンドのルートを構築する。
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "engram",
		Short: "Perceptual-quality driven video optimizer",
		Long: `engram optimizes videos per-shot: scene detection, encoding,
VMAF v1 evaluation, and CRF bisection to hit a target quality score.`,
		// 実行時エラーで usage をダンプしない（フラグミス以外は不要のため）
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.AddCommand(newSetupCmd())
	root.AddCommand(newOptimizeCmd())
	return root
}

// Execute はCLI全体を実行し、エラーを呼び出し側（main）へ返す。
// 終了コードの制御とエラー表示は main 側が行う。
func Execute() error {
	return NewRootCmd().Execute()
}
