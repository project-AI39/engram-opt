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
	// workDir は評価ログ等の中間生成物の書き出し先（ジョブ一時領域配下のシーン毎ディレクトリ）。
	// 失敗時に調査証拠を残せるよう、掃除は実装側の責務で行う。
	Evaluate(ctx context.Context, originalPath string, scene Scene, encodedChunkPath string, workDir string) (QualityMetrics, error)
}

// AudioMuxer 完成映像への音声付与の契約（memo.md「音声処理」）。
// 音声はシーン分割の対象外のため、チャンク単位ではなく最終ミックスで1回だけ呼ばれる。
type AudioMuxer interface {
	// MuxAudio videoPath（音声なし完成映像）と originalPath（元動画）から
	// mode に従って音声を付与し outputPath へ書き出す。
	// 元動画に音声が無い場合は映像のみを outputPath へ書き出す（全モード共通）。
	MuxAudio(ctx context.Context, videoPath string, originalPath string, mode AudioMode, outputPath string) error
}
