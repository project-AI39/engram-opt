package ffmpeg

import (
	"strings"
	"testing"
)

// concatEscape の不変条件: エスケープ済み文字列からエスケープ列を除去すれば
// シングルクォートは一切残らない（concat demuxerのリスト改変を構造的に不可能にする）。
func FuzzConcatEscape(f *testing.F) {
	for _, s := range []string{``, `a.mkv`, `my'movie.mkv`, "it''s.mkv", `C:\v\a'b.mkv`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		esc := concatEscape(in)
		stripped := strings.ReplaceAll(esc, `'\''`, "\x00")
		if strings.ContainsRune(stripped, '\'') {
			t.Fatalf("bare single quote survived escaping: in=%q esc=%q", in, esc)
		}
	})
}
