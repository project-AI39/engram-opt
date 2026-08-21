// Package setup は依存関係（FFmpeg / av-scenechange）のセットアップ実装。
//
// 実行方法（リポジトリルートから）:
//
//	go run ./cmd/engram setup
//
// やること:
//   - build/bin/ へ FFmpeg / ffprobe の配置（バージョンpin + SHA256検証付き）
//   - build/bin/ へ av-scenechange の配置（ソースtarball pin + cargo build）
//   - 配置後、各バイナリの動作確認と vmaf フィルタの存在確認
//
// シェルを一切経由せず net/http / os/exec / mholt/archives だけで動作するため、
// ローカルとCIで同一コマンドが使える（Dev-CI Parity）。
package setup

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"engram-opt/internal/toolbin"
)

// Run は依存関係の導入状態を検査し、不足・破損があれば修復する。
func Run() error {
	root, err := toolbin.RepoRoot()
	if err != nil {
		return err
	}
	binDir := filepath.Join(root, "build", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", binDir, err)
	}

	if err := installFFmpeg(binDir); err != nil {
		return fmt.Errorf("ffmpeg: %w", err)
	}
	if err := installSceneChange(binDir); err != nil {
		return fmt.Errorf("av-scenechange: %w", err)
	}

	log.Printf("[setup] all dependencies are ready: %s", binDir)
	return nil
}
