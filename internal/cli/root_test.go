package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// mousetrapが無効化されていること。ダブルクリック→ウィザード導線の前提条件
// （有効のままでは explorer.exe 起動時に警告を表示して終了し、RunEまで到達しない）。
func TestMousetrapDisabled(t *testing.T) {
	if cobra.MousetrapHelpText != "" {
		t.Fatalf("MousetrapHelpText must be empty, got %q", cobra.MousetrapHelpText)
	}
}
