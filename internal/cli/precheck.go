package cli

// 起動時の事前チェック群。探索は1試行ごとにコストが高いため、
// 失敗が確定している入力・出力は1フレームもエンコードする前に拒否する（フェイルファスト）。

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// minInputDurationSeconds はこれ未満の入力を「短すぎる」として拒否する閾値。
// 単一フレーム動画（av-scenechangeが処理できない）を実用上排除できる値。
const minInputDurationSeconds = 0.05

// allowedOutputExts は最終muxへ渡せるコンテナ拡張子。
// mkv が全codec/全音声モードで常に安全（推奨）。mp4/webm/mov は codec 組合せ次第で
// 最終段のffmpegが拒否し得るため、READMEで注意書きする。
var allowedOutputExts = map[string]bool{
	".mkv": true, ".mp4": true, ".webm": true, ".mov": true,
}

// checkInputFile は入力パスの存在と種別を検証する。
// ffprobeの内部エラーより先に、利用者が理解できる形で欠陥を表面化させる。
func checkInputFile(input string) error {
	fi, err := os.Stat(input)
	if os.IsNotExist(err) {
		return fmt.Errorf("input file not found: %s", input)
	}
	if err != nil {
		return fmt.Errorf("accessing input file: %w", err)
	}
	if fi.IsDir() {
		return fmt.Errorf("input is a directory, not a video file: %s", input)
	}
	return nil
}

// checkOutputExt は出力パスの健全性を検証する（空＝既定名生成は通す）。
// - 拡張子が対応コンテナか
// - 既存ディレクトリを指していないか（最終リネーム段階での確定失敗を早期に防ぐ）
func checkOutputExt(output string) error {
	if output == "" {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(output))
	if !allowedOutputExts[ext] {
		return fmt.Errorf(
			"unsupported output extension %q (use .mkv recommended, or .mp4 / .webm / .mov)", ext)
	}
	if fi, err := os.Stat(output); err == nil && fi.IsDir() {
		return fmt.Errorf("output path is an existing directory: %s", output)
	}
	return nil
}

// samePath は2パスが同一実体を指すかを判定する（Windowsは大小無視）。
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	return a == b
}

// checkDistinctArtifacts は出力・ログファイル・入力の相互衝突を検証する。
// 同一パスへの多重書き込みは、最終リネーム段階での不可解な失敗や入力破壊に直結するため
// 実行前に拒否する。
func checkDistinctArtifacts(input, output, logFile string) error {
	if output != "" && logFile != "" && samePath(output, logFile) {
		return fmt.Errorf("--out and --log-file must differ: %s", output)
	}
	if logFile != "" && input != "" && samePath(logFile, input) {
		return fmt.Errorf("--log-file must differ from the input file: %s", logFile)
	}
	return nil
}
