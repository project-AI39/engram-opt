// engram-opt CLI本体のエントリーポイント。ロジックは一切持たず internal/cli へ委譲する。
// 依存関係のセットアップは開発者向けの別バイナリ cmd/engram-setup を使う。
package main

import (
	"bufio"
	"fmt"
	"os"

	"engram-opt/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		// cobraが "Error: ..." を既にstderrへ出力している。ダブルクリック起動者
		// （裸起動＋対話端末）のみ[Enter]待ちして、窓が瞬時に閉じるのを防ぐ。
		if cli.ShouldPauseAfterError() {
			fmt.Fprintln(os.Stderr, "\n[Enter]キーで終了します...")
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		}
		os.Exit(1)
	}
}
