// Package devcheck は開発者向けの品質ゲート（gofmt / go vet）を提供する。
//
// 配置意図: ランタイム本体（cmd/engram-opt）からは参照しない。
// 利用者マシンにGoツールチェーンは存在せず、起動時チェックは成立しないため、
// 「ビルドの玄関」である engram-package と、任意タイミングの engram-setup check
// からのみ呼び出す。これにより配布物を作る行為そのものが整形・静的解析の強制点になる。
//
// 補足: go build / go run にプリビルドフックは存在しない。素の go build を直接
// 打った場合のみ本ゲートを迂回するため、AGENTS.md の検証手順で周知する。
package devcheck

import (
	"fmt"
	"os/exec"
	"strings"
)

// GofmtIssues は gofmt -l の結果（整形が必要なファイル一覧）を返す。
// 空スライスなら整形済み。
func GofmtIssues(root string) ([]string, error) {
	cmd := exec.Command("gofmt", "-l", "cmd", "internal")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("running gofmt: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// Vet は go vet ./... を実行し、違反があれば詳細付きエラーを返す。
func Vet(root string) error {
	cmd := exec.Command("go", "vet", "./...")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go vet: %w\n%s", err, tail(out, 30))
	}
	return nil
}

// Run は整形チェックと静的解析を順に実行する。logf は進捗表示（nil許容）。
func Run(root string, logf func(f string, a ...any)) error {
	say := func(f string, a ...any) {
		if logf != nil {
			logf(f, a...)
		}
	}

	say("[devcheck] gofmt -l cmd internal ...")
	files, err := GofmtIssues(root)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		return fmt.Errorf(
			"%d file(s) need formatting:\n  %s\nrun 'gofmt -w .' and retry",
			len(files), strings.Join(files, "\n  "))
	}
	say("[devcheck] gofmt: clean")

	say("[devcheck] go vet ./...")
	if err := Vet(root); err != nil {
		return err
	}
	say("[devcheck] go vet: clean")
	return nil
}

// tail は出力の末尾 maxLines 行を返す（エラーメッセージ用）。
func tail(b []byte, maxLines int) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}
