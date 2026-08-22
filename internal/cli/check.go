package cli

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"

	"engram-opt/internal/devcheck"
	"engram-opt/internal/toolbin"
)

// newCheckCmd は開発者向けの品質ゲート実行コマンド（gofmt + go vet）。
// engram-package でも配布前に自動実行されるが、任意タイミングで単発実行できるように公開する。
func newCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Run formatting and static analysis gates (gofmt -l + go vet ./...)",
		Long: `Runs the same quality gates that engram-package enforces before
building a distribution zip:

  1. gofmt -l cmd internal   (any output = unformatted files = failure)
  2. go vet ./...

Exits non-zero on the first violation. Formatting issues are reported
with a hint to run 'gofmt -w .'`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := toolbin.RepoRoot()
			if err != nil {
				return fmt.Errorf("locating repository root: %w", err)
			}
			return devcheck.Run(root, log.Printf)
		},
	}
}
