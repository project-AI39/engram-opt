package cli

import (
	"github.com/spf13/cobra"

	"engram-opt/internal/setup"
)

// NewSetupCmd は開発者向けセットアップCLI（cmd/engram-setup）のルートを構築する。
// ランタイム本体（engram-opt）からは分離されており、配布物には含まれない。
func NewSetupCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "engram-setup",
		Short: "Install pinned dependencies (FFmpeg, av-scenechange) into build/bin/",
		Long: `Installs pinned external dependencies into build/bin/:

- FFmpeg / ffprobe static builds (version pinned + SHA256 verified)
- av-scenechange (source tarball pinned + local cargo build)

Idempotent: existing working binaries are verified and reused.
Used by both local development and CI (Dev-CI Parity).

This tool is for developers only; end users receive a distribution
zip with bin/ already bundled.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return setup.Run()
		},
	}
	root.AddCommand(newCheckCmd())
	return root
}
