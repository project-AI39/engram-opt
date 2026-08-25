package media

import (
	"strings"
	"testing"
)

// 複数のキーフレームがある場合、targetSec 以下の最後のものを選ぶ。
func TestPickLastKeyframeSelectsLatestWithinTarget(t *testing.T) {
	csvData := strings.Join([]string{
		"0.000000,K__",
		"1.000000,K__",
		"2.500000,K__",
		"3.700000,K__",
	}, "\n")
	got, ok, err := pickLastKeyframe(csvData, 30, 1, 3.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected an anchor")
	}
	if got.PTS != "2.500000" || got.Frame != 75 {
		t.Fatalf("anchor = %+v, want pts=2.500000 frame=75", got)
	}
}

// 境界値は含めて扱う（キーフレームがシーン開始と一致する場合は残差ゼロが理想）。
func TestPickLastKeyframeIncludesBoundary(t *testing.T) {
	csvData := strings.Join([]string{
		"0.000000,K__",
		"3.000000,K__",
		"6.000000,K__",
	}, "\n")
	got, ok, err := pickLastKeyframe(csvData, 30, 1, 3.0)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.PTS != "3.000000" || got.Frame != 90 {
		t.Fatalf("anchor = %+v, want boundary keyframe 3.000000/90", got)
	}
}

// 対象時刻より前のキーフレームが無い（初回キーのみ等）の場合は見つからない。
func TestPickLastKeyframeNoneBeforeTarget(t *testing.T) {
	csvData := strings.Join([]string{
		"10.000000,K__",
		"12.000000,N__",
	}, "\n")
	_, ok, err := pickLastKeyframe(csvData, 30, 1, 5.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected no anchor before target")
	}
}

// 非キーフレーム・空行・パース不能行・負ptsは無視される。
func TestPickLastKeyframeSkipsNonKeysAndJunk(t *testing.T) {
	csvData := strings.Join([]string{
		"",
		"not-a-number,K__",
		"-1.000000,K__",
		"1.000000,N__",
		"2.000000,K__",
		"trailing-no-comma",
	}, "\n")
	got, ok, err := pickLastKeyframe(csvData, 24, 1, 9.0)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.PTS != "2.000000" || got.Frame != 48 {
		t.Fatalf("anchor = %+v, want 2.000000/48", got)
	}
}

// 有理数fps（24000/1001=23.976…）でも丸めは正しい枠に落ちる。
func TestPickLastKeyframeRationalFPSRounding(t *testing.T) {
	// 1001/24000 秒刻み。第90フレームのpts_time = 90*1001/24000 = 3.753750
	csvData := strings.Join([]string{
		"0.000000,K__",
		"3.753750,K__",
	}, "\n")
	got, _, err := pickLastKeyframe(csvData, 24000, 1001, 4.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Frame != 90 {
		t.Fatalf("frame = %d, want 90 (exact rational mapping)", got.Frame)
	}
}
