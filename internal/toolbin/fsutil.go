package toolbin

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SameAbsPath は2パスが同一実体を指すかを判定する（実ファイルの有無は問わない純粋比較）。
// 絶対パスへ正規化して等価を見る。Windowsでは大小文字を同一視する（C:\a と c:\A は同一）。
// 出力=入力上書き防止など、CLI境界・エンジン境界の双方で使う共通実装。
func SameAbsPath(a, b string) bool {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(absA, absB)
	}
	return absA == absB
}

// IsWithin は target が root 配下（root自身を含む）なら true を返す。
// 相対解決に基づく純粋比較で、実ファイルの存在は問わない。Windowsでは大小文字を同一視。
// 一時領域への出力拒否など「配下チェック」の共通実装。
func IsWithin(root, target string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		absRoot, absTarget = strings.ToLower(absRoot), strings.ToLower(absTarget)
	}
	rel, rerr := filepath.Rel(absRoot, absTarget)
	return rerr == nil && !strings.HasPrefix(rel, "..")
}

// Tail は複数行文字列の末尾 maxLines 行を返す。
// 外部コマンド失敗時のstderr要約など、長大出力の末尾だけを残す用途に使う。
func Tail(s string, maxLines int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

// CopyFile は src を dst へコピーする。dst の親ディレクトリが無ければ作成し、
// 書き込みエラーと Close エラーの双方を検査する（Close時の書き込みバッファ漏れ対策）。
// 実行権限などのモード変更は呼び出し側の責務とする。
func CopyFile(srcPath, dstPath string) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
