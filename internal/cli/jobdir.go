package cli

// ジョブ一時領域（jobDir）の生成・掃除。実装の詳細は jobdir_test.go と対で読むこと。

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// newJobDir はジョブ専用の一時ディレクトリパスを返す。
// PID接尾辞により同一秒起動の別プロセスとの衝突（同名チャンクの相互上書き）を防ぐ。
// 平文/TUI/ウィザード全起動経路で必ずこれを使うこと。
func newJobDir(tmpRoot string) string {
	return filepath.Join(tmpRoot,
		fmt.Sprintf("%s-p%d", time.Now().Format("20060102-150405"), os.Getpid()))
}

// sweepStaleJobs は tmpRoot 内で72時間より古いジョブディレクトリを掃除する（ベストエフォート）。
// クラッシュ等で失敗時に残したtmpが無人長時間運用中に単調増加するのを防ぐ。
func sweepStaleJobs(tmpRoot string) {
	entries, err := os.ReadDir(tmpRoot)
	if err != nil {
		// 掃除はベストエフォートだが完全な黙黙殺はしない（観測可能性のため警告のみ）
		log.Printf("[optimize] warning: failed to read temp root for sweeping: %v", err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ts, ok := jobTimestampOf(e.Name())
		if !ok || time.Since(ts) < 72*time.Hour {
			continue
		}
		p := filepath.Join(tmpRoot, e.Name())
		if err := os.RemoveAll(p); err != nil {
			log.Printf("[optimize] warning: failed to sweep stale temp dir: %v", err)
		} else {
			log.Printf("[optimize] swept stale temp dir (older than 72h): %s", p)
		}
	}
}

// jobTimestampOf はジョブディレクトリ名先頭の "20060102-150405" 接頭辞を解釈する。
func jobTimestampOf(name string) (time.Time, bool) {
	if len(name) < 15 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("20060102-150405", name[:15], time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
