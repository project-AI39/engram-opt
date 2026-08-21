package domain

import "testing"

// memo.md「共通データ契約」: FrameCount = End - Start + 1（EndFrame inclusive）。
func TestSceneFrameCount(t *testing.T) {
	cases := []struct {
		name  string
		scene Scene
		want  int64
	}{
		{"normal range", Scene{Index: 0, StartFrame: 0, EndFrame: 99}, 100},
		{"single frame", Scene{Index: 1, StartFrame: 5, EndFrame: 5}, 1},
		{"second scene offset", Scene{Index: 2, StartFrame: 100, EndFrame: 179}, 80},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.scene.FrameCount(); got != tc.want {
				t.Fatalf("FrameCount() = %d, want %d", got, tc.want)
			}
		})
	}
}

// Validate はフレームSSOTの不変条件を一元検証する。
// detector生成物・engine入力・encoder入力すべてがこの契約を満たすことを保証する。
func TestSceneValidateAcceptsValidScenes(t *testing.T) {
	valid := []Scene{
		{Index: 0, StartFrame: 0, EndFrame: 99},
		{Index: 3, StartFrame: 60, EndFrame: 60}, // 1フレームシーンも有効
	}
	for _, sc := range valid {
		if err := sc.Validate(); err != nil {
			t.Fatalf("valid scene %+v rejected: %v", sc, err)
		}
	}
}

func TestSceneValidateRejectsInvalidScenes(t *testing.T) {
	cases := []struct {
		name  string
		scene Scene
	}{
		{"inverted range", Scene{Index: 0, StartFrame: 50, EndFrame: 49}},
		{"negative start", Scene{Index: 0, StartFrame: -1, EndFrame: 10}},
		{"negative index", Scene{Index: -1, StartFrame: 0, EndFrame: 10}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.scene.Validate(); err == nil {
				t.Fatalf("invalid scene %+v accepted", tc.scene)
			}
		})
	}
}
