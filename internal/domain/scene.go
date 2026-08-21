// Package domain はパイプライン全体で共有される型・データ契約を定義する。
//
// 依存の錨として機能し、各実装モジュール（detector / encoder / evaluator / engine）
// はこのパッケージに定義されたインターフェースと型のみを介して連携する。
// domain 自体は外部ライブラリや他internalパッケージに依存してはならない。
package domain

// Scene シーンごとの境界情報。
// 浮動小数点による丸め誤差（VMAF急落の原因）を排除するため、
// int64 のフレーム番号のみを唯一の真実（SSOT）とする。
type Scene struct {
	Index      int   `json:"index"`       // シーン通し番号 (0, 1, 2...)
	StartFrame int64 `json:"start_frame"` // 開始フレーム番号 (0-indexed, inclusive)
	EndFrame   int64 `json:"end_frame"`   // 終了フレーム番号 (inclusive)
}

// FrameCount シーンの総フレーム数を取得。
func (s Scene) FrameCount() int64 {
	return s.EndFrame - s.StartFrame + 1
}
