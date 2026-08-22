package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNewJobDirHasPIDAndTimestamp(t *testing.T) {
	dir := newJobDir(t.TempDir())
	base := filepath.Base(dir)
	if !strings.HasSuffix(base, "-p"+strconv.Itoa(os.Getpid())) {
		t.Fatalf("job dir must carry PID suffix: %s", base)
	}
	if _, ok := jobTimestampOf(base); !ok {
		t.Fatalf("job dir must start with a parseable timestamp: %s", base)
	}
}

func TestSweepStaleJobsRemovesOnlyOldJobDirs(t *testing.T) {
	tmp := t.TempDir()
	old := filepath.Join(tmp, time.Now().Add(-73*time.Hour).Format("20060102-150405")+"-p111")
	fresh := filepath.Join(tmp, time.Now().Format("20060102-150405")+"-p222")
	junk := filepath.Join(tmp, "not-a-job-dir") // 命名規約外は保護される
	for _, d := range []string{old, fresh, junk} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sweepStaleJobs(tmp)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("stale job dir should be removed: %v", err)
	}
	for _, keep := range []string{fresh, junk} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("non-stale dir must survive: %v", err)
		}
	}
}
