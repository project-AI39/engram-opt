// engram-opt CLI本体のエントリーポイント。ロジックは一切持たず internal/cli へ委譲する。
// 依存関係のセットアップは開発者向けの別バイナリ cmd/engram-setup を使う。
package main

import (
	"bufio"
	"fmt"
	"os"

	"engram-opt/internal/cli"
)

// version は配布ビルド時に engram-package が -ldflags "-X main.version=..." で注入する。
// Zip名のバージョン（git describe）と同一値になり、--version で確認できる。
// 素の go build / go run では既定値 devel のまま。
var version = "devel"

func main() {
	if err := cli.Execute(version); err != nil {
		// cobraが "Error: ..." を既にstderrへ出力している。ダブルクリック起動者
		// （裸起動＋対話端末）のみ[Enter]待ちして、窓が瞬時に閉じるのを防ぐ。
		if cli.ShouldPauseAfterError() {
			fmt.Fprintln(os.Stderr, "\n[Enter]キーで終了します...")
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		}
		os.Exit(1)
	}
}
