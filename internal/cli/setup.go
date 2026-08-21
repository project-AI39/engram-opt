package cli

import (
	"github.com/spf13/cobra"

	"engram-opt/internal/setup"
)

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Install pinned dependencies (FFmpeg, av-scenechange) into build/bin/",
		Long: `Installs pinned external dependencies into build/bin/:

- FFmpeg / ffprobe static builds (version pinned + SHA256 verified)
- av-scenechange (source tarball pinned + local cargo build)

Idempotent: existing working binaries are verified and reused.
Used by both local development and CI (Dev-CI Parity).`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return setup.Run()
		},
	}
}
