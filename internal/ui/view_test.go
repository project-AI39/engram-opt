package ui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"engram-opt/internal/domain"
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

// ETA推定の決定論ケース: 完了シーン平均 × 残りシーン数。
// running中シーンの time.Since は非決定論的なため、完了のみ／検出中の両面を検証する。
func TestEtaEstimation(t *testing.T) {
	m := testWizardModel(t) // stageSetup のModelを流用し、run表示用フィールドだけ上書きする
	m.stage = stageRun
	m.phase = phaseOptimizing
	m.started = time.Now()
	m.total = 4
	m.doneCount = 2
	m.scenes = []domain.Scene{
		{Index: 0}, {Index: 1}, {Index: 2}, {Index: 3},
	}
	base := time.Now()
	m.shots = map[int]*shotState{
		0: {status: shotDone, dur: 10 * time.Second},
		1: {status: shotDone, dur: 30 * time.Second},
		2: {status: shotRunning, started: base.Add(-5 * time.Second)},
	}

	d, ok := m.eta()
	if !ok {
		t.Fatal("eta should be estimable while optimizing with completed shots")
	}
	// 平均20s × 残り1シーン + 実行中5s経過（数ミリ秒の誤差を許容）
	want := 25 * time.Second
	if d < want || d > want+time.Second {
		t.Fatalf("eta = %v, want ~%v", d, want)
	}

	// 最初のシーン完了前は推定不能
	m2 := m
	m2.doneCount = 0
	if _, ok := m2.eta(); ok {
		t.Fatal("eta must be unavailable before any shot completes")
	}
}
