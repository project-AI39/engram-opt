// Package toolbin は同梱外部バイナリ（build/bin 配下）の解決や
// リポジトリルート検出など、ファイルシステム周りの共通ユーティリティを提供する。
package toolbin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ToolName はホストOSに応じた実行バイナリ名を返す（Windowsは .exe 付き）。
func ToolName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

// FileExists は path が通常ファイルとして存在するかどうかを返す。
func FileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// RepoRoot は cwd から親ディレクトリを辿り、go.mod のある場所（リポジトリルート）を返す。
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if FileExists(filepath.Join(dir, "go.mod")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repository root not found (go.mod not found)")
		}
		dir = parent
	}
}

// Resolve は同梱外部バイナリのパスを解決する。
// PATH参照やシステムインストール済みのffmpeg等は絶対に使わない（ポータブル配布の固定方針）。
//
// 探索順序:
//  1. 配布レイアウト: 実行バイナリからの相対 ../bin/<tool>（build/<本体> + build/bin/）
//  2. 開発レイアウト: リポジトリルート/build/bin/<tool>（go run 実行時）
func Resolve(name string) (string, error) {
	name = ToolName(name)

	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "..", "bin", name)
		if FileExists(p) {
			return p, nil
		}
	}
	root, err := RepoRoot()
	if err != nil {
		return "", fmt.Errorf("binary %q not found: %w", name, err)
	}
	p := filepath.Join(root, "build", "bin", name)
	if FileExists(p) {
		return p, nil
	}
	return "", fmt.Errorf("binary %q not found; run 'go run ./cmd/engram setup' first", name)
}
