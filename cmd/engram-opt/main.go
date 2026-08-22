// engram-opt CLI本体のエントリポイント。ロジックは一切書かず internal/cli へ委譲する。
// 依存関係のセットアップは開発者向けの別バイナリ cmd/engram-setup を使う。
package main

import (
	"os"

	"engram-opt/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
