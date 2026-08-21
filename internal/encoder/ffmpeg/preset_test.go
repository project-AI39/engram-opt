package ffmpeg

import "testing"

// SVT-AV1 は数値プリセットのみ受けるための解決層。
// 既知名は対応数値へ、数値文字列は透過、未知名は黙って代替せずエラー（fail-fast）。
func TestSvtPresetMapping(t *testing.T) {
	known := map[string]string{
		"veryslow": "1", "slower": "2", "slow": "4", "medium": "6",
		"fast": "8", "faster": "10", "veryfast": "12", "superfast": "13",
	}
	for name, want := range known {
		got, err := svtPreset(name)
		if err != nil {
			t.Fatalf("svtPreset(%q) unexpected error: %v", name, err)
		}
		if got != want {
			t.Fatalf("svtPreset(%q) = %q, want %q", name, got, want)
		}
	}

	// 数値はそのまま透過
	got, err := svtPreset("4")
	if err != nil || got != "4" {
		t.Fatalf("numeric passthrough failed: %q, %v", got, err)
	}

	// 未知名はエラー（旧実装は黙って medium 相当へ置換していた）
	if _, err := svtPreset("meduim"); err == nil { // 意図的なタイポ
		t.Fatal("unknown preset must be an error, not silently replaced")
	}
}

// concat リストのクォートエスケープ形式（ffmpeg concat demuxer形式）。
// 各シングルクォートは '\” （閉じてからエスケープ付き開き）へ置換される。
func TestConcatEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{`C:\videos\a.mkv`, `C:\videos\a.mkv`}, // クォート無しはそのまま
		{"my'movie.mkv", "my" + `'\''` + "movie.mkv"},
		{"it''s.mkv", "it" + `'\''` + `'\''` + "s.mkv"}, // 連続クォートも全て対象
	}
	for _, tc := range cases {
		if got := concatEscape(tc.in); got != tc.want {
			t.Fatalf("concatEscape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
