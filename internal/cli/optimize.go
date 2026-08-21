package cli

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"

	"engram-opt/internal/detector/avscenechange"
)

func newOptimizeCmd() *cobra.Command {
	var outPath string

	cmd := &cobra.Command{
		Use:   "optimize <input>",
		Args:  cobra.ExactArgs(1),
		Short: "Optimize a video per-shot (Phase 2: scene detection only)",
		Long: `Analyzes the input video and outputs per-shot scene boundaries as JSON.

Pipeline stages implemented so far:
  [x] scene detection (av-scenechange via FFmpeg Y4M pipe)
  [ ] encoding / VMAF evaluation / CRF bisection (later phases)`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			scenes, err := avscenechange.New().Detect(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			data, err := json.MarshalIndent(scenes, "", "  ")
			if err != nil {
				return fmt.Errorf("marshaling result: %w", err)
			}
			data = append(data, '\n')

			if outPath == "" {
				out := cmd.OutOrStdout()
				if _, err := out.Write(data); err != nil {
					return fmt.Errorf("writing to stdout: %w", err)
				}
			} else if err := os.WriteFile(outPath, data, 0o644); err != nil {
				return fmt.Errorf("writing result file: %w", err)
			}

			log.Printf("[optimize] scene detection done: %d scene(s)", len(scenes))
			log.Printf("[optimize] encoding / VMAF / CRF-search stages are not implemented yet (Phase 3+)")
			return nil
		},
	}
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "write result JSON to file instead of stdout")
	return cmd
}
