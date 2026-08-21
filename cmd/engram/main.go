// engram CLI本体のエントリポイント。ロジックは一切書かず internal/cli へ委譲する。
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
