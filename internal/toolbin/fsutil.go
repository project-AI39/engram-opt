package toolbin

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

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
