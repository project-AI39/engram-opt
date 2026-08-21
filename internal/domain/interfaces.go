package domain

import "context"

// SceneDetector シーン境界検出モジュールの契約。
type SceneDetector interface {
	Name() string
	// Detect 動画全体を解析し、フレーム単位のシーンリストを返す。
	// 総フレーム数も実装内部で取得するため、呼び出し側は入力パスだけ渡す。
	Detect(ctx context.Context, inputPath string) ([]Scene, error)
}

// VideoEncoder エンコードモジュールの契約。
type VideoEncoder interface {
	Name() string
	// EncodeChunk 元動画の特定シーン区間を指定パラメータでエンコードする。
	EncodeChunk(ctx context.Context, inputPath string, scene Scene, params EncodeParams, outputPath string) error
	// ConcatChunks 最終的に確定した全チャンクを無劣化結合する。
	ConcatChunks(ctx context.Context, chunkPaths []string, finalOutputPath string) error
}

// QualityEvaluator 画質評価モジュールの契約。
type QualityEvaluator interface {
	Name() string
	// Evaluate 元動画の特定区間とエンコード済みチャンクを比較評価する。
	Evaluate(ctx context.Context, originalPath string, scene Scene, encodedChunkPath string) (QualityMetrics, error)
}
