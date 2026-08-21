package avscenechange

import (
	"testing"

	"engram-opt/internal/domain"
)

// フィクスチャは v0.24.1 の実測出力を基にしたもの。
// scores は巨大なため要点のみ残し、未知キーのスキップ経路も検証する。
const fixtureJSON = `{"scene_changes":[0,120],"scores":{"1":{"inter_cost":0.0,"threshold":112.8},"120":{"inter_cost":320.0,"threshold":208.8}},"frame_count":180,"speed":1277.275146354444}`

func TestParseResult(t *testing.T) {
	res, err := parseResult([]byte(fixtureJSON))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantCuts := []int64{0, 120}
	if len(res.sceneChanges) != len(wantCuts) {
		t.Fatalf("scene_changes = %v, want %v", res.sceneChanges, wantCuts)
	}
	for i, c := range wantCuts {
		if res.sceneChanges[i] != c {
			t.Fatalf("scene_changes[%d] = %d, want %d", i, res.sceneChanges[i], c)
		}
	}
	if res.frameCount != 180 {
		t.Fatalf("frame_count = %d, want 180", res.frameCount)
	}
}

func TestParseResultErrors(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"not json", "not a json"},
		{"not an object", `[]`},
		{"missing frame_count", `{"scene_changes":[0]}`},
		{"missing scene_changes", `{"frame_count":100}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseResult([]byte(tc.json)); err == nil {
				t.Fatalf("expected error for input: %s", tc.json)
			}
		})
	}
}

func TestBuildScenes(t *testing.T) {
	scenes, err := buildScenes([]int64{0, 120}, 180)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.Scene{
		{Index: 0, StartFrame: 0, EndFrame: 119},
		{Index: 1, StartFrame: 120, EndFrame: 179},
	}
	if len(scenes) != len(want) {
		t.Fatalf("scenes = %+v, want %+v", scenes, want)
	}
	for i, s := range want {
		if scenes[i] != s {
			t.Fatalf("scenes[%d] = %+v, want %+v", i, scenes[i], s)
		}
	}

	// 単一シーン（カット点は先頭0のみ）でも全体をカバーすること
	single, err := buildScenes([]int64{0}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if single[0].FrameCount() != 1 || single[0].StartFrame != 0 || single[0].EndFrame != 0 {
		t.Fatalf("single scene = %+v", single[0])
	}
}

func TestBuildScenesErrors(t *testing.T) {
	cases := []struct {
		name       string
		cuts       []int64
		frameCount int64
	}{
		{"first cut is not zero", []int64{10}, 100},
		{"not strictly ascending", []int64{0, 50, 50}, 100},
		{"cut out of range", []int64{0, 100}, 100},
		{"invalid frame count", []int64{0}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildScenes(tc.cuts, tc.frameCount); err == nil {
				t.Fatalf("expected error for cuts=%v frameCount=%d", tc.cuts, tc.frameCount)
			}
		})
	}
}
