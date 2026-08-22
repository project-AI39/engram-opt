package devcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 一時ディレクトリに最小モジュールを作る（cmd/ internal/ は gofmt -l の対象外でも
// GofmtIssues/Vet は cmd/internal を見るため、テストではルート直下のパス構成を模倣する）。
func writeMiniModule(t *testing.T, dir, badGo string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	// GofmtIssuesは cmd/ internal/ の両方を対象に渡すため、実リポジトリと同じ構成を作る
	if err := os.MkdirAll(filepath.Join(dir, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	gomod := "module example.com/mini\n\ngo 1.24\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "x", "x.go"), []byte(badGo), 0o644); err != nil {
		t.Fatal(err)
	}
}

const goodCode = "package x\n\nfunc F() int {\n\treturn 1\n}\n"

// インデント崩れはgofmtが実際に検出する形（単行関数ボディは整形対象外のため不使用）
const badlyFormatted = "package x\n\nfunc F() int {\nreturn 1\n}\n"

// 整形済みコードは何も検出しない。
func TestGofmtClean(t *testing.T) {
	if testing.Short() {
		t.Skip("go toolchain invocation skipped in -short")
	}
	dir := t.TempDir()
	writeMiniModule(t, dir, goodCode)

	files, err := GofmtIssues(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("issues = %v, want none", files)
	}
}

// 整形漏れはファイル名として検出され、案内付きエラーになる。
func TestGofmtDetectsUnformatted(t *testing.T) {
	if testing.Short() {
		t.Skip("go toolchain invocation skipped in -short")
	}
	dir := t.TempDir()
	writeMiniModule(t, dir, badlyFormatted)

	files, err := GofmtIssues(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "x.go" {
		t.Fatalf("issues = %v, want [x.go]", files)
	}

	err = Run(dir, nil)
	if err == nil || !strings.Contains(err.Error(), "gofmt -w") {
		t.Fatalf("Run should fail with a fix hint, got: %v", err)
	}
}

// vetは「コンパイルは通るが疑わしい」コードを検出する（Printf書式不一致）。
func TestVetReportsSuspiciousCode(t *testing.T) {
	if testing.Short() {
		t.Skip("go toolchain invocation skipped in -short")
	}
	dir := t.TempDir()
	suspicious := "package x\n\nimport \"fmt\"\n\nfunc F() {\n\tfmt.Printf(\"%d\", \"not-a-number\")\n}\n"
	writeMiniModule(t, dir, suspicious)

	if err := Vet(dir); err == nil {
		t.Fatal("vet should flag Printf format mismatch")
	}
}

// 正常なコードならRun全体が成功する。
func TestRunCleanModulePasses(t *testing.T) {
	if testing.Short() {
		t.Skip("go toolchain invocation skipped in -short")
	}
	dir := t.TempDir()
	writeMiniModule(t, dir, goodCode)

	var logs int
	logf := func(f string, a ...any) { logs++ }
	if err := Run(dir, logf); err != nil {
		t.Fatalf("clean module must pass: %v", err)
	}
	if logs < 2 {
		t.Fatalf("progress logs = %d, want >=2", logs)
	}
}
