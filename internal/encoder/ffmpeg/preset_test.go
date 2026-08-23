package ffmpeg

import "testing"

// SVT-AV1 は数値プリセットのみ受けるための解決層。
// 数値文字列は透過、x264流の名前はスケール違いの黙って置換を避けるためエラー（fail-fast）。
func TestSvtPresetNumericOnly(t *testing.T) {
	// 数値はそのまま透過（範囲判定はSVT-AV1本体へ委ねる）
	for _, s := range []string{"0", "4", "6", "13"} {
		got, err := svtPreset(s)
		if err != nil || got != s {
			t.Fatalf("numeric passthrough failed: %q -> %q, %v", s, got, err)
		}
	}

	// x264流の名前は全て拒否（かつての対応表 medium->6 等の置換を禁止）
	for _, s := range []string{
		"veryslow", "slower", "slow", "medium",
		"fast", "faster", "veryfast", "superfast",
		"meduim", // 意図的なタイポ
		"",
	} {
		if _, err := svtPreset(s); err == nil {
			t.Fatalf("non-numeric preset %q must be an error, not silently replaced", s)
		}
	}
}

// concat リストのクォートエスケープ形式（ffmpeg concat demuxer形式）。
// 各シングルクォートは '\” （閉じてからエスケープ付き開き）へ置換される。
func TestConcatEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{`C:\videos\a.mkv`, `C:\videos\a.mkv`}, // クォート無しはそのまま
		{"my'movie.mkv", "my" + `'\''` + "movie.mkv"},
		{"it''s.mkv", "it" + `'\''` + `'\''` + "s.mkv"}, // 連続クォートは全て対象
	}
	for _, tc := range cases {
		if got := concatEscape(tc.in); got != tc.want {
			t.Fatalf("concatEscape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
