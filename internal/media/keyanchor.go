// Package media は ffprobe を用いた媒体情報の共通取得を提供する。
// エンコーダ（チャンク抽出）と評価器（参照側入力）の双方が
// キーフレームアンカー事前シーク（memo.md §4.2）で使う。
package media

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"

	"engram-opt/internal/toolbin"
)

// KeyAnchor はシーン前方にある同期点（キーフレーム）の情報。
type KeyAnchor struct {
	PTS   string // ffprobeから取得したそのままのpts_time（-ssへそのまま渡す）
	Frame int64  // 動画全体でのフレーム番号（0-indexed）
}

// ProbeFrameRate は入力動画のフレームレートを有理数（num/den、ともに正の整数）で返す。
// 浮動小数点へ落とさない（タイムスタンプ正規式・フレーム番号逆算にそのまま埋め込むため）。
func ProbeFrameRate(ctx context.Context, ffprobePath, inputPath string) (int64, int64, error) {
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=r_frame_rate",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputPath).CombinedOutput()
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe (frame rate) failed: %w\n%s", err, toolbin.Tail(string(out), 5))
	}
	s := strings.TrimSpace(string(out))
	numStr, denStr, ok := strings.Cut(s, "/")
	if !ok {
		numStr, denStr = s, "1"
	}
	num, err1 := strconv.ParseInt(numStr, 10, 64)
	den, err2 := strconv.ParseInt(denStr, 10, 64)
	if err1 != nil || err2 != nil || num <= 0 || den <= 0 {
		return 0, 0, fmt.Errorf("invalid r_frame_rate %q", s)
	}
	return num, den, nil
}

// PlanSeek は startFrame への高速到達方法を返す。
//
// 戻り値:
//   - ssArgs: ffmpeg引数列への挿入片（アンカーが利用可能なとき [-ss <pts>]。不要なら空）
//   - offset: select範囲の平行移動量（between(n,S,E) → between(n,S-offset,E-offset)）
//
// StartFrame<=0、またはアンカーが見つからない場合は (nil, 0, nil) を返し、
// 呼び出し側は従来どおりフルデコードになる（性能最適化層のため可用性優先。
// フォールバック時は警告ログを出す＝黙る代替はしない）。
func PlanSeek(ctx context.Context, ffprobePath, input string, startFrame int64) (ssArgs []string, offset int64, err error) {
	if startFrame <= 0 {
		return nil, 0, nil
	}
	num, den, err := ProbeFrameRate(ctx, ffprobePath, input)
	if err != nil {
		return nil, 0, err
	}
	targetSec := float64(startFrame) * float64(den) / float64(num)
	anchor, ok, err := lastKeyframeBefore(ctx, ffprobePath, input, num, den, targetSec)
	if err != nil {
		return nil, 0, err
	}
	if !ok || anchor.Frame <= 0 {
		return nil, 0, nil
	}
	return []string{"-ss", anchor.PTS}, anchor.Frame, nil
}

// lastKeyframeBefore は targetSec 以前（≤）で最後のキーフレームを実パケットから探す。
// パケット走査はメタデータ読みのみ（デコードなし）で軽量であり、-read_intervals により
// 走査範囲を [0, targetSec+1) に限定する。
func lastKeyframeBefore(ctx context.Context, ffprobePath, input string, fpsNum, fpsDen int64, targetSec float64) (KeyAnchor, bool, error) {
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-select_streams", "v:0",
		// 区間指定は「開始%終了」形式（実機確認済み。'-'区切りは不正引数になる）。
		"-read_intervals", fmt.Sprintf("0%%%d", int64(targetSec)+1),
		"-show_entries", "packet=pts_time,flags",
		"-of", "csv=p=0",
		input).CombinedOutput()
	if err != nil {
		return KeyAnchor{}, false, fmt.Errorf("ffprobe (keyframe scan) failed: %w\n%s", err, toolbin.Tail(string(out), 5))
	}
	return pickLastKeyframe(string(out), fpsNum, fpsDen, targetSec)
}

// pickLastKeyframe は ffprobe CSV 行群から条件を満たす最後のキーフレームを選ぶ純関数。
// 行形式: "<pts_time>,<flags>"（例: "12.345000,K__"）。flags の先頭 "K" が同期点を示す。
// フレーム番号への変換は round(pts_time × fps)。pts_time は6桁小数で出力されるため
// 変換誤差はフレーム周期より十分小さく、常に正しい枠へ丸まる。
func pickLastKeyframe(csvData string, fpsNum, fpsDen int64, targetSec float64) (KeyAnchor, bool, error) {
	var best KeyAnchor
	found := false
	for _, line := range strings.Split(csvData, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		tsStr, flags, ok := strings.Cut(line, ",")
		if !ok || !strings.HasPrefix(flags, "K") {
			continue
		}
		ts, err := strconv.ParseFloat(tsStr, 64)
		if err != nil || ts < 0 || ts > targetSec+1e-6 {
			continue
		}
		best = KeyAnchor{
			PTS:   tsStr,
			Frame: int64(math.Round(ts * float64(fpsNum) / float64(fpsDen))),
		}
		found = true
	}
	return best, found, nil
}
