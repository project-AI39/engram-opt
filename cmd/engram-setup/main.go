// engram-setup: 開発者向けの依存関係セットアップCLI。
// 配布Zipには含めない（利用者は bin/ 同梱のZipを受け取るためセットアップ不要）。
// 実行: go run ./cmd/engram-setup
package main

import (
	"os"

	"engram-opt/internal/cli"
)

func main() {
	if err := cli.ExecuteSetup(); err != nil {
		os.Exit(1)
	}
}
