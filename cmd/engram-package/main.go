// 配布物パッケージャーの薄いエントリポイント。
// 実体は internal/packaging。実行: go run ./cmd/engram-package
package main

import (
	"flag"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"engram-opt/internal/packaging"
	"engram-opt/internal/toolbin"
)

func main() {
	var (
		version = flag.String("version", "", "version string for the zip name (default: git describe)")
		outDir  = flag.String("out", "dist", "output directory for the zip")
		noZip   = flag.Bool("no-zip", false, "stage build/ only, skip zipping")
	)
	flag.Parse()
	log.SetFlags(0)

	root, err := toolbin.RepoRoot()
	if err != nil {
		log.Fatalf("repository root not found: %v (run from inside the repository)", err)
	}
	if *version == "" {
		*version = gitVersion(root)
	}

	zipPath, err := packaging.Run(root, packaging.Options{
		Version: *version,
		OutDir:  *outDir,
		NoZip:   *noZip,
		Logf: func(f string, a ...any) {
			log.Printf(f, a...)
		},
	})
	if err != nil {
		log.Fatalf("packaging failed: %v", err)
	}
	fmt.Println(zipPath) // 成功時は生成物パスを最終行へ（スクリプト連携用）
}

// gitVersion は git describe でバージョンを導出する。失敗時は snapshot。
// タグ未整備の開発段階でも動くように、エラーは致命扱いにしない。
func gitVersion(root string) string {
	cmd := exec.Command("git", "describe", "--tags", "--always", "--dirty")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "snapshot"
	}
	return strings.TrimSpace(string(out))
}
