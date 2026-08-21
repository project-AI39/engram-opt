package libvmaf

import (
	"context"
	"testing"

	"engram-opt/internal/domain"
)

// 不正シーンはバイナリ解決・ffprobe起動の前に拒否されること（他モジュールとの契約統一）。
// 単体テストとして実バイナリ無しで検証できる。
func TestEvaluateRejectsInvalidScene(t *testing.T) {
	cases := []struct {
		name  string
		scene domain.Scene
	}{
		{"inverted range", domain.Scene{Index: 0, StartFrame: 59, EndFrame: 49}},
		{"negative start", domain.Scene{Index: 0, StartFrame: -1, EndFrame: 10}},
	}
	e := New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := e.Evaluate(context.Background(), "in.mp4", tc.scene, "chunk.mkv"); err == nil {
				t.Fatalf("invalid scene %+v accepted", tc.scene)
			}
		})
	}
}
