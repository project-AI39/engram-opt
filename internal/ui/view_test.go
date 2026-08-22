package ui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// shorten のルーン単位動作。マルチバイト（日本語）パスがバイト境界で
// 切断されて文字化けしないこと（dashboard の in/out チップ表示）を保証する。
func TestShorten(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
	}{
		{"short path untouched", "a.mp4", 42},
		{"exactly max runes untouched", strings.Repeat("あ", 42), 42},
		{"ascii long path", strings.Repeat("a", 50), 42},
		{"japanese long path", `/動画/` + strings.Repeat("あ", 40) + ".mp4", 42},
		{"tiny max falls back to truncate", strings.Repeat("あ", 20), 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shorten(tc.in, tc.max)
			if !utf8.ValidString(got) {
				t.Fatalf("shorten produced invalid UTF-8: %q", got)
			}
			limit := tc.max
			if tc.max < 3 {
				limit = tc.max // truncate フォールバック経路も max 文字以内
			}
			if n := len([]rune(got)); n > limit {
				t.Fatalf("result %d runes exceeds limit %d: %q", n, limit, got)
			}
			if tc.max >= 3 && len([]rune(tc.in)) <= tc.max && got != tc.in {
				t.Fatalf("short path should be untouched: got %q", got)
			}
		})
	}
}

// 中略形式そのもの（前半+"..."+後半）も ASCII ケースで固定照合する。
func TestShortenFormat(t *testing.T) {
	in := strings.Repeat("a", 50)
	want := strings.Repeat("a", 19) + "..." + strings.Repeat("a", 19)
	if got := shorten(in, 42); got != want {
		t.Fatalf("shorten = %q, want %q", got, want)
	}
}
