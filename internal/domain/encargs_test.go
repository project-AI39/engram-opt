package domain

import (
	"strings"
	"testing"
)

func TestParseExtraArgs(t *testing.T) {
	// 正常: 空白分割・そのままargvトークン化
	got, err := ParseExtraArgs("  -tune film   -x265-params aq-mode=1:psy-rd=1.0  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"-tune", "film", "-x265-params", "aq-mode=1:psy-rd=1.0"}
	if len(got) != len(want) {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// 空はnil（追加なし）
	if got, _ := ParseExtraArgs(""); got != nil {
		t.Fatalf("empty input should return nil, got %v", got)
	}

	// 管理対象オプションの拒否（フェイルファスト）
	for _, bad := range []string{
		"-crf 10",
		"-preset slow",
		"-c:v libx264",
		"-vf scale=1280:720",
		"--crf 10",
	} {
		if _, err := ParseExtraArgs(bad); err == nil {
			t.Fatalf("ParseExtraArgs(%q) should be rejected", bad)
		} else if !strings.Contains(err.Error(), "cannot be overridden") {
			t.Fatalf("reject reason missing for %q: %v", bad, err)
		}
	}

	// 値トークン（非ダッシュ）は直前オプションの値として素通しされる
	got2, err := ParseExtraArgs("-tune film")
	if err != nil || len(got2) != 2 || got2[0] != "-tune" || got2[1] != "film" {
		t.Fatalf("ParseExtraArgs(-tune film) = %v, %v", got2, err)
	}
}
