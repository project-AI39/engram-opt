package domain

// パーサ系関数のファジング。不変条件:
//   - ParseOutRes: エラー時は必ず (0,0)。成功時は正の偶数寸法。
//   - ParseExtraArgs: エラーは管理対象オプション名の拒否のみ。成功時は管理対象名を含まない。
// panic 0 が最低ライン。`go test -fuzz` で回す（CIは通常モードでseedのみ実行）。

import (
	"strings"
	"testing"
)

func FuzzParseOutRes(f *testing.F) {
	for _, s := range []string{"", "1920x1080", "0x0", "1920x1079", "ax1080", "1920x", "x", "-4x8", " 1280x720 ", "8x8", "999999999999x2"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		w, h, err := ParseOutRes(s)
		if err != nil {
			if w != 0 || h != 0 {
				t.Fatalf("error case must zero dims: %q -> %dx%d", s, w, h)
			}
			return
		}
		// 空指定（trim後）のみ「入力解像度へ追従」を意味する (0,0) を許す
		if w == 0 && h == 0 {
			if strings.TrimSpace(s) != "" {
				t.Fatalf("non-empty input must not resolve to follow-input dims: %q", s)
			}
			return
		}
		if w <= 0 || h <= 0 || w%2 != 0 || h%2 != 0 {
			t.Fatalf("accepted invalid dims from %q: %dx%d", s, w, h)
		}
	})
}

func FuzzParseExtraArgs(f *testing.F) {
	for _, s := range []string{"", "-crf 20", "--preset=3", "-tune film", "foo bar", "-b:v 1M", "-crf=20", " - ", "--"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		args, err := ParseExtraArgs(s)
		if err != nil {
			if !strings.Contains(err.Error(), "managed by engram-opt") {
				t.Fatalf("unexpected error form for %q: %v", s, err)
			}
			return
		}
		for _, a := range args {
			// 非ダッシュ値トークン（-tune film の "film" 等）は素通しが仕様。
			// 管理対象名チェックはオプション形式のトークンにのみ適用する。
			if !strings.HasPrefix(a, "-") {
				continue
			}
			name := strings.TrimLeft(a, "-")
			if i := strings.IndexByte(name, '='); i >= 0 {
				name = name[:i]
			}
			for _, banned := range forbiddenExtraArgNames {
				bare := strings.TrimLeft(banned, "-")
				if name == bare {
					t.Fatalf("managed option escaped validation: %q in %q", a, s)
				}
			}
		}
	})
}

func FuzzScoreMetricRoundTrip(f *testing.F) {
	for _, s := range []string{"harmonic_mean", "mean", "min", "", "MEAN", "harm"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		m, err := ParseScoreMetric(s)
		if err != nil {
			return // 未知名はエラーで正常
		}
		if m != MetricHarmonic && m != MetricMean && m != MetricMin {
			t.Fatalf("parser produced non-constant metric %q from %q", m, s)
		}
	})
}

func FuzzParseAudioMode(f *testing.F) {
	for _, s := range []string{"copy", "opus", "aac", "none", "libopus", "COPY", "", "flac"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		m, err := ParseAudioMode(s)
		if err != nil {
			return
		}
		switch m {
		case AudioCopy, AudioOpus, AudioAAC, AudioNone:
		default:
			t.Fatalf("parser produced non-constant mode %q from %q", m, s)
		}
	})
}
