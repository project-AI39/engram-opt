// Package cli は cobra によるコマンド定義を集約する。
// 各コマンドは引数解析と委譲のみを行い、実際の処理は internal 配下の各パッケージに置く。
//
// バイナリ構成:
//
//	engram-opt    ランタイム本体（配布Zipへ同梱）。実行はルート直下の位置引数で受ける
//	engram-setup  開発者向け依存関係セットアップ（配布物には含めない）
package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"engram-opt/internal/domain"
	"engram-opt/internal/ui"
)

// NewRootCmd は engram-opt コマンドのルートを構築する。
// 単一目的ツールのため実行本体はサブコマンド化せず、入力動画を位置引数で直接受ける:
//
//	engram-opt                 端末なら設定ウィザード / 非端末ならヘルプ
//	engram-opt <input> [flags] 即実行
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "engram-opt [input]",
		Short: "Perceptual-quality driven video optimizer",
		Long: `engram-opt optimizes videos per-shot: scene detection, encoding,
VMAF v1 evaluation, and CRF bisection to hit a target quality score.

Usage:
  engram-opt                     open the interactive setup wizard (double-click friendly)
  engram-opt <input> [flags]     optimize immediately (plain logs unless --tui)

Temporary trial files live under <tmp-root>/<job-id>/ and are removed on
success (kept on failure for debugging). Output defaults to <input>.opt.mkv
next to the source; override with --out.

Use --shot N for a debug run of a single scene's CRF search
(no concat; the winning trial chunk is kept).`,
		// 実行時エラーで usage をダンプしない（フラグミス以外は不要のため）
		SilenceUsage: true,
	}
	registerRun(root)
	return root
}

// Execute はランタイムCLIを実行し、エラーを呼び出し側（main）へ返す。
// 終了コードの制御とエラー表示は main 側が行う。
// シグナル対応コンテキスト付きで実行し、Ctrl+C / SIGTERM で ctx がキャンセルされ、
// 実行中の ffmpeg 等の子プロセスは exec.CommandContext 経由で停止される
// （孤児プロセス化の防止）。
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return NewRootCmd().ExecuteContext(ctx)
}

// ExecuteSetup は開発者向けセットアップCLI（cmd/engram-setup）を実行する。
func ExecuteSetup() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return NewSetupCmd().ExecuteContext(ctx)
}

// bareRunE は引数なし起動の挙動（ウィザード or ヘルプ）を返す。
// registerRun の RunE から、入力が空の場合のフォールバックとして使う。
func bareRunE(cmd *cobra.Command) error {
	// ダブルクリック/裸起動＋端末なら設定ウィザードへ（memo.md「TUIウィザード化」）
	if ui.IsTerminal() {
		cfg, cerr := buildSearchConfig(string(domain.CodecH264), "medium")
		if cerr != nil {
			return cerr
		}
		return launchWizardMode(cmd.Context(), "", "", cfg, domain.DefaultAudioMode, nil)
	}
	return cmd.Help()
}
