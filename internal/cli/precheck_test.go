package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckInputFile(t *testing.T) {
	if err := checkInputFile(filepath.Join(t.TempDir(), "missing.mp4")); err == nil {
		t.Fatal("missing file must be rejected")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not-found message", err)
	}
	if err := checkInputFile(t.TempDir()); err == nil {
		t.Fatal("directory input must be rejected")
	}
}

func TestCheckOutputExt(t *testing.T) {
	for _, ok := range []string{"", "out.mkv", `C:\x\OUT.MKV`, "out.mp4", "out.webm", "out.mov"} {
		if err := checkOutputExt(ok); err != nil {
			t.Fatalf("checkOutputExt(%q) unexpected error: %v", ok, err)
		}
	}
	for _, bad := range []string{"out.txt", "out", "out.avi"} {
		if err := checkOutputExt(bad); err == nil {
			t.Fatalf("checkOutputExt(%q) must be rejected", bad)
		}
	}
}

func TestCheckDistinctArtifacts(t *testing.T) {
	if err := checkDistinctArtifacts("in.mkv", "same.mkv", strings.ToUpper(`.\SAME.MKV`)); err == nil {
		t.Fatal("out == log-file (case-insensitive) must be rejected")
	}
	if err := checkDistinctArtifacts("in.mkv", "", "in.mkv"); err == nil {
		t.Fatal("log-file == input must be rejected")
	}
	if err := checkDistinctArtifacts("in.mkv", "out.mkv", "run.log"); err != nil {
		t.Fatalf("distinct paths must pass: %v", err)
	}
}
