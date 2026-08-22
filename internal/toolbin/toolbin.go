// Package toolbin は同梱外部バイナリ（bin/ 配下）の解決や実行レイアウトの検出、
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

// Layout は本体と付帯リソース（bin/・tmp/）の所在を表す。
//
// 唯一の正式レイアウトは配布Zipと同一のステージング構造（memo.md「配置」）:
//
//	<base>/optimizer.exe   本体
//	<base>/bin/            外部バイナリ群
//	<base>/tmp/            実行時一領域
//
// ソースツリーからの実行（go run / go test 等）では <repo>/build を base に寄せる。
type Layout struct {
	Base string // bin/ と tmp/ を格納するディレクトリ
	Dev  bool   // 配布物ではなくソースツリーからの実行の場合 true
}

// DetectLayout は実行バイナリの所在からレイアウトを確定する。
//
// 判定は配置済みレイアウトの定義そのものである「本体の隣に bin/ があるか」を
// 直接確認する自己検証方式。OSの一時領域などの場所に基づく間接ヒューリスティックは
// 意図的に使わない:
//   - go run / go test の生成バイナリはキャッシュに置かれ隣に bin/ がない → 開発扱い
//   - ローカルで go build -o build/optimizer.exe しても bin/ が隣にあるため配布扱い
//     （開発・配布で同一のコードパスを通る）
//   - ユーザーがZipをどのパスへ解凍しても正しく動作する
func DetectLayout() (Layout, error) {
	exe, err := os.Executable()
	if err != nil {
		return Layout{}, fmt.Errorf("resolving executable: %w", err)
	}
	return detectLayoutFor(exe)
}

func detectLayoutFor(exePath string) (Layout, error) {
	base := filepath.Dir(exePath)
	if FileExists(filepath.Join(base, "bin", ToolName("ffmpeg"))) {
		return Layout{Base: base}, nil
	}
	root, rerr := RepoRoot()
	if rerr != nil {
		return Layout{}, fmt.Errorf(
			"no bundled tools next to %s and no repository root detected from the working directory; run 'go run ./cmd/engram-setup' first (keep the distributed folder layout intact)",
			filepath.Base(exePath))
	}
	return Layout{Base: filepath.Join(root, "build"), Dev: true}, nil
}

// Resolve は同梱外部バイナリのパスを解決する。
// PATH参照やシステムインストール済みのffmpeg等は絶対に使わない（ポータブル配布の固定方針）。
func Resolve(name string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolving executable: %w", err)
	}
	return resolveFor(exe, name)
}

func resolveFor(exePath, name string) (string, error) {
	lay, err := detectLayoutFor(exePath)
	if err != nil {
		return "", err
	}
	p := filepath.Join(lay.Base, "bin", ToolName(name))
	if !FileExists(p) {
		return "", fmt.Errorf("binary %q not found in %s; run 'go run ./cmd/engram-setup' first",
			name, filepath.Join(lay.Base, "bin"))
	}
	return p, nil
}

// TempRoot は実行時の一時作業領域（tmp/）の基点ディレクトリを返す。
// ジョブごとのサブディレクトリ（タイムスタンプ等）は呼び出し側がこの下に作る。
func TempRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolving executable: %w", err)
	}
	return tempRootFor(exe)
}

func tempRootFor(exePath string) (string, error) {
	lay, err := detectLayoutFor(exePath)
	if err != nil {
		return "", err
	}
	return filepath.Join(lay.Base, "tmp"), nil
}
