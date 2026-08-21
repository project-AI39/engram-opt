// Package avscenechange は av-scenechange によるシーン境界検出の実装。
//
// av-scenechange は生映像（Y4M）しか直接読めないため、FFmpeg の標準出力を
// RAM 上のパイプで直結して入力する。中間ファイルのディスク書き出しは禁止方針
// （memo.md「メモリパイプによるディスクI/O・容量爆発の防止」参照）。
//
// v0.24.1 の実測挙動:
//
//   - 入力に "-" を渡すと stdin から Y4M を読み取る
//
//   - stdout へデフォルトでJSONを出力する（--json フラグは v0.24.1 には存在しない）
//
//     {"scene_changes":[0,120],"scores":{...},"frame_count":180,"speed":...}
//
//   - scene_changes はカット点（先頭必ず0）、frame_count は処理した総フレーム数。
//     終端は含まれないため最終シーンの EndFrame は frameCount-1 となる。
//
//   - scores は全フレーム分の内訳で長時間動画では巨大になるため、streaming
//     デコーダで scene_changes / frame_count のみ抽出し、scores はスキップする。
package avscenechange

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"

	"engram-opt/internal/domain"
	"engram-opt/internal/toolbin"
)

// Detector は domain.SceneDetector の実装。
type Detector struct{}

// New は Detector を生成する。
func New() *Detector { return &Detector{} }

// Name は実装名を返す。
func (d *Detector) Name() string { return "av-scenechange" }

// Detect 動画全体を解析し、フレーム単位のシーンリストを返す。
func (d *Detector) Detect(ctx context.Context, inputPath string) ([]domain.Scene, error) {
	if !toolbin.FileExists(inputPath) {
		return nil, fmt.Errorf("input file not found: %s", inputPath)
	}
	ffmpegPath, err := toolbin.Resolve("ffmpeg")
	if err != nil {
		return nil, err
	}
	ffprobePath, err := toolbin.Resolve("ffprobe")
	if err != nil {
		return nil, err
	}
	ascPath, err := toolbin.Resolve("av-scenechange")
	if err != nil {
		return nil, err
	}

	// 総フレーム数の裏付け取得（コンテナメタデータ。無い場合は -1 でスキップ）
	probedFrames, err := probeTotalFrames(ctx, ffprobePath, inputPath)
	if err != nil {
		return nil, fmt.Errorf("probing total frames: %w", err)
	}

	cuts, frameCount, err := detectCuts(ctx, ffmpegPath, ascPath, inputPath)
	if err != nil {
		return nil, err
	}

	// メタデータとパイプで実際に処理されたフレーム数の整合確認。
	// 不一致は後段のチャンク分割・VMAF評価のフレームずれ（スコア崩壊）に直結するため fail-fast する。
	if probedFrames >= 0 && probedFrames != frameCount {
		return nil, fmt.Errorf(
			"total frame count mismatch: ffprobe reports %d but av-scenechange processed %d; input stream may be broken or container metadata stale",
			probedFrames, frameCount)
	}

	return buildScenes(cuts, frameCount)
}

// detectCuts は FFmpeg→Y4M パイプ → av-scenechange(stdin "-") を実行し、
// カット点リストと総フレーム数を返す。
func detectCuts(ctx context.Context, ffmpegPath, ascPath, inputPath string) ([]int64, int64, error) {
	// Y4M生映像を stdout へ流す。-nostdin で誤ってstdinを読まないよう明示。
	ffCmd := exec.CommandContext(ctx, ffmpegPath,
		"-hide_banner", "-nostdin", "-loglevel", "error",
		"-i", inputPath,
		"-f", "yuv4mpegpipe", "-")
	var ffErr bytes.Buffer
	ffCmd.Stderr = &ffErr

	y4mPipe, err := ffCmd.StdoutPipe()
	if err != nil {
		return nil, 0, fmt.Errorf("creating ffmpeg stdout pipe: %w", err)
	}

	ascCmd := exec.CommandContext(ctx, ascPath, "-") // "-" はstdin入力の意味
	ascCmd.Stdin = y4mPipe
	var jsonOut, ascErr bytes.Buffer
	ascCmd.Stdout = &jsonOut
	ascCmd.Stderr = &ascErr

	if err := ffCmd.Start(); err != nil {
		return nil, 0, fmt.Errorf("starting ffmpeg: %w\n%s", err, tail(ffErr.String(), 10))
	}
	if err := ascCmd.Start(); err != nil {
		_ = ffCmd.Process.Kill()
		_ = ffCmd.Wait()
		return nil, 0, fmt.Errorf("starting av-scenechange: %w", err)
	}

	// 待機順序が重要: 先に asc を待つ。
	// ffmpeg は終了時にstdoutの書き込み側を閉じる → asc がEOFまで読み切って終了する。
	// 先に ffCmd.Wait() を呼ぶと StdoutPipe の読み込み側が早期クローズされ、
	// asc がデータを飲み残す恐れがある。
	ascWaitErr := ascCmd.Wait()
	ffWaitErr := ffCmd.Wait()

	if ffWaitErr != nil {
		return nil, 0, fmt.Errorf("ffmpeg failed while piping y4m: %w\n%s", ffWaitErr, tail(ffErr.String(), 20))
	}
	if ascWaitErr != nil {
		return nil, 0, fmt.Errorf("av-scenechange failed: %w\n%s", ascWaitErr, tail(ascErr.String(), 20))
	}

	res, err := parseResult(jsonOut.Bytes())
	if err != nil {
		return nil, 0, err
	}
	log.Printf("[detector] av-scenechange finished: %d cut point(s), %d frame(s)", len(res.sceneChanges), res.frameCount)
	return res.sceneChanges, res.frameCount, nil
}

// detectionResult は av-scenechange 出力から必要な字段のみを取り出したもの。
type detectionResult struct {
	sceneChanges []int64
	frameCount   int64
}

// parseResult は av-scenechange のJSON出力をストリーミング解析する。
// scores（全フレーム分の内訳）は巨大なため値ごと読み飛ばしてメモリ使用量を抑える。
func parseResult(data []byte) (detectionResult, error) {
	var res detectionResult

	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return res, fmt.Errorf("parsing av-scenechange output: %w", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return res, fmt.Errorf("unexpected av-scenechange output: want a JSON object")
	}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return res, fmt.Errorf("parsing av-scenechange output: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return res, fmt.Errorf("parsing av-scenechange output: unexpected key token %v", keyTok)
		}
		switch key {
		case "scene_changes":
			if err := dec.Decode(&res.sceneChanges); err != nil {
				return res, fmt.Errorf("parsing scene_changes: %w", err)
			}
		case "frame_count":
			if err := dec.Decode(&res.frameCount); err != nil {
				return res, fmt.Errorf("parsing frame_count: %w", err)
			}
		default:
			// scores / speed 等は不要なので値ツリーごとスキップ
			if err := skipValue(dec); err != nil {
				return res, fmt.Errorf("skipping %q field: %w", key, err)
			}
		}
	}

	if res.frameCount <= 0 {
		return res, fmt.Errorf("av-scenechange output has no valid frame_count")
	}
	if len(res.sceneChanges) == 0 {
		return res, fmt.Errorf("av-scenechange output has no scene_changes")
	}
	return res, nil
}

// skipValue はデコーダの現在位置にある値1個（プリミティブ／オブジェクト／配列）を
// 読み飛ばす。標準ライブラリに Skip 相当が無いためトークンで深さを追跡して実装する。
// 巨大な scores フィールドをメモリに展開せず破棄するために使う。
func skipValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return nil // プリミティブは読み捨てで完了
	}
	// 構造化値は対応する閉じ区切りまでトークンを消費する
	depth := 1
	open := delim == '{' || delim == '['
	if !open {
		return fmt.Errorf("unexpected closing delimiter %v", delim)
	}
	for depth > 0 {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		switch d := t.(type) {
		case json.Delim:
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}

// buildScenes はカット点リストと総フレーム数から domain.Scene 列を構築する。
// カット点 c0=0, c1, ..., ck に対しシーン区間は [ci, c(i+1)) 半開区間で与えられ、
// 最終区間の終端は総フレーム数。domain では inclusive 扱いのため EndFrame = 次開始-1。
func buildScenes(cuts []int64, frameCount int64) ([]domain.Scene, error) {
	if frameCount <= 0 {
		return nil, fmt.Errorf("invalid frame count: %d", frameCount)
	}
	if cuts[0] != 0 {
		return nil, fmt.Errorf("first scene change must be frame 0, got %d", cuts[0])
	}
	for i := 1; i < len(cuts); i++ {
		if cuts[i] <= cuts[i-1] {
			return nil, fmt.Errorf("scene changes must be strictly ascending: [%d]=%d, [%d]=%d",
				i-1, cuts[i-1], i, cuts[i])
		}
		if cuts[i] >= frameCount {
			return nil, fmt.Errorf("scene change %d is out of range (frame_count=%d)", cuts[i], frameCount)
		}
	}

	bounds := make([]int64, 0, len(cuts)+1)
	bounds = append(bounds, cuts...)
	bounds = append(bounds, frameCount)

	scenes := make([]domain.Scene, 0, len(bounds)-1)
	for i := 0; i+1 < len(bounds); i++ {
		scenes = append(scenes, domain.Scene{
			Index:      i,
			StartFrame: bounds[i],
			EndFrame:   bounds[i+1] - 1,
		})
	}
	// 構築したシーンがフレームSSOTの不変条件を満たすことの最終確認
	for _, sc := range scenes {
		if err := sc.Validate(); err != nil {
			return nil, fmt.Errorf("constructed invalid scene: %w", err)
		}
	}
	return scenes, nil
}

// probeTotalFrames はコンテナメタデータ（nb_frames）から総フレーム数を取得する。
// メタデータが提供されない形式（N/A 等）の場合は -1 を返し、整合確認をスキップする。
// デコードを伴わないヘッダ参照のみのため高速。
func probeTotalFrames(ctx context.Context, ffprobePath, inputPath string) (int64, error) {
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=nb_frames",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputPath).Output()
	if err != nil {
		return -1, fmt.Errorf("ffprobe failed: %w", err)
	}
	n, perr := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if perr != nil || n < 0 {
		return -1, nil // N/A や空文字など、メタデータ無しは「不明」として扱う
	}
	return n, nil
}

// tail は文字列の末尾 maxLines 行のみを返す（エラーメッセージ用の切り詰め）。
func tail(s string, maxLines int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}
