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

	"engram-opt/internal/ui"
)

// init はWindows既定のmousetrap（explorer.exe起動時に警告を表示して終了）を無効化する。
// 本CLIはダブルクリック→TUIウィザードを正規導線とするため、cobraの保護機能が
// 起動を横取りしてしまうのを防ぐ（memo.md「TUIウィザード化」）。
// mousetrapはダブルクリック時のみ発動するため、端末起動・headlessへの影響はゼロ。
func init() {
	cobra.MousetrapHelpText = ""
}

// newRootCmd は engram-opt コマンドのルートを構築する。
// 単一目的ツールのため実行本体はサブコマンド化せず、入力動画を位置引数で直接受ける:
//
//	engram-opt                 端末なら設定ウィザード / 非端末ならヘルプ
//	engram-opt <input> [flags] 即実行
func newRootCmd() *cobra.Command {
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

// ShouldPauseAfterError はエラー表示後に[Enter]待ちで一時停止すべきかを返す。
// 裸起動（引数なし）かつ対話端末——すなわちダブルクリック利用者——のみ対象とし、
// TUI開始前の致命的エラー（bin/不在等）でコンソールが瞬時に閉じるのを防ぐ。
// 引数あり・headless・パイプ実行には影響しない。
func ShouldPauseAfterError() bool {
	return len(os.Args) == 1 && ui.IsTerminal()
}

// Execute はランタイムCLIを実行し、エラーを呼び出し側（main）へ返す。
// 終了コードの制御とエラー表示は main 側が行う。
// シグナル対応コンテキスト付きで実行し、Ctrl+C / SIGTERM で ctx がキャンセルされ、
// 実行中の ffmpeg 等の子プロセスは exec.CommandContext 経由で停止される
// （孤児プロセス化の防止）。
//
// version は --version 表示用（配布ビルドではZip名と同一のgit describe値が
// -ldflags -X で注入される。開発時は main 側の既定値 devel）。
func Execute(version string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := newRootCmd()
	root.Version = version
	// cobra既定の "engram-opt version X" 形式を1行へ短縮（スクリプトでの取得も容易に）
	root.SetVersionTemplate("engram-opt {{.Version}}\n")
	return root.ExecuteContext(ctx)
}

// ExecuteSetup は開発者向けセットアップCLI（cmd/engram-setup）を実行する。
func ExecuteSetup() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return NewSetupCmd().ExecuteContext(ctx)
}
