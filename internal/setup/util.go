package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mholt/archives"

	"engram-opt/internal/toolbin"
)

// ===== 共通ユーティリティ =====

// downloadFile は url を dest へ保存する。curl/wget等を使わず net/http で直接取得し、
// シェル差異を排除する。進捗は20MBごとにログ出力する。
// 途中失敗時に部分ファイルを dest へ残留させないよう、一旦 <dest>.part へ書き出し、
// 完了後に置換する。
func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "engram-opt-setup")

	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w (%s)", err, url)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s (%s)", resp.Status, url)
	}

	part := dest + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			f.Close()
			os.Remove(part) // 失敗時の断片を掃除
		}
	}()

	const chunk = 1 << 20         // 1 MiB
	const progressUnit = 20 << 20 // 20 MiB ごとに進捗表示
	var total int64
	buf := make([]byte, chunk)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			prev := total
			total += int64(n)
			if prev/progressUnit != total/progressUnit {
				log.Printf("[setup]   %.0f MB downloaded...", float64(total)/(1<<20))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	log.Printf("[setup]   %.1f MB downloaded", float64(total)/(1<<20))
	if err := f.Sync(); err != nil {
		return err
	}
	// Windowsでも置換できるよう、Close済みの状態でrenameする
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(part, dest); err != nil {
		return err
	}
	complete = true
	return nil
}

// verifySHA256 はファイルのSHA256が期待値と一致することを確認する。
func verifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != strings.ToLower(want) {
		return fmt.Errorf("sha256 mismatch for %s:\n  got:  %s\n  want: %s", filepath.Base(path), got, want)
	}
	log.Printf("[setup] sha256 OK")
	return nil
}

// openArchive はアーカイブを開いて形式を自動判定し、抽出用リーダーを返す。
func openArchive(ctx context.Context, archivePath string) (archives.Format, io.Reader, func(), error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, nil, nil, err
	}
	format, reader, err := archives.Identify(ctx, filepath.Base(archivePath), f)
	if err != nil {
		f.Close()
		return nil, nil, nil, fmt.Errorf("identifying archive format: %w", err)
	}
	return format, reader, func() { f.Close() }, nil
}

// extractAll はアーカイブ全体を destDir へ展開する。
// アーカイブ内パスを正規化し、destDir の外へ書き込まれないよう防ぐ（Zip Slip対策）。
func extractAll(ctx context.Context, archivePath, destDir string) error {
	format, reader, closer, err := openArchive(ctx, archivePath)
	if err != nil {
		return err
	}
	defer closer()

	extractor, ok := format.(archives.Extractor)
	if !ok {
		return fmt.Errorf("format %T does not support extraction", format)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	return extractor.Extract(ctx, reader, func(_ context.Context, info archives.FileInfo) error {
		// アーカイブ内部は '/' 区切りなので先に正規化してから安全確認する
		name := path.Clean(strings.TrimPrefix(info.NameInArchive, "/"))
		if name == "." || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("unsafe path in archive: %q", info.NameInArchive)
		}
		target := filepath.Join(destDir, filepath.FromSlash(name))
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyArchiveEntry(info, target)
	})
}

// copyArchiveEntry はアーカイブ内エントリの中身を destPath へ書き込む。
func copyArchiveEntry(info archives.FileInfo, destPath string) error {
	src, err := info.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(destPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	// Unix系では実行権限を付与（Windowsでは不要）
	if runtime.GOOS != "windows" {
		return os.Chmod(destPath, 0o755)
	}
	return nil
}

// copyFile はファイルをコピーする（Unix系では実行権限を付与）。
func copyFile(srcPath, destPath string) error {
	if err := toolbin.CopyFile(srcPath, destPath); err != nil {
		return err
	}
	// Unix系では実行権限を付与（Windowsでは不要）
	if runtime.GOOS != "windows" {
		return os.Chmod(destPath, 0o755)
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// 以下の薄いラッパは setup パッケージ内の既存呼び出し箇所を保ちつつ、
// 実装を toolbin の共通関数へ集約するためのもの。
func tail(s string, maxLines int) string { return toolbin.Tail(s, maxLines) }

func fileExists(path string) bool { return toolbin.FileExists(path) }

func toolName(base string) string { return toolbin.ToolName(base) }

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func keysOf[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
