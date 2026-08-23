// Package packaging は配布物（build/ ステージング領域 → Zip）の生成を担う。
//
// 責務:
//   - 本体バイナリの build/ への配置（go build）
//   - THIRD-PARTY-NOTICES.txt の自動生成（Go依存モジュールのライセンス分類込み）
//   - LICENSE / tmp プレースホルダの同梱
//   - dist/ へのZip出力（決定論的タイムスタンプ）
//
// memo.md「配置」節の構造と1:1に対応する。実行コマンドは go run ./cmd/engram-package。
package packaging

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"engram-opt/internal/devcheck"
	"engram-opt/internal/toolbin"
)

// Options はパッケージング実行時の設定。
type Options struct {
	Version string                   // Zipファイル名へ使うバージョン文字列（例: v0.1.0 / snapshot）
	OutDir  string                   // Zipの出力先ディレクトリ（既定: dist）。空ならdist扱い
	NoZip   bool                     // trueでステージングのみ行いZip化を省略（検証用）
	Logf    func(f string, a ...any) // 進捗ログ（nil可）
}

func (o *Options) logf(f string, a ...any) {
	if o.Logf != nil {
		o.Logf(f, a...)
	}
}

// Run はステージング〜Zip化までを実行し、生成したZipのパスを返す（NoZip時は空文字）。
func Run(root string, opt Options) (string, error) {
	if opt.OutDir == "" {
		opt.OutDir = "dist"
	}
	buildDir := filepath.Join(root, "build")
	binDir := filepath.Join(buildDir, "bin")

	// 0) 開発品質ゲート: 整形漏れ・静的解析違反は配布物を作らせない（fail-fast）。
	//    「ビルドしたら問題も一緒に見つかる」を保証する正規のフックポイント。
	opt.logf("[package] dev checks (gofmt / go vet) ...")
	if err := devcheck.Run(root, opt.logf); err != nil {
		return "", fmt.Errorf("dev checks: %w", err)
	}

	// 1) 同梱バイナリの事前確認（setup未実行ならここで明確に失敗させる）
	for _, tool := range []string{"ffmpeg", "ffprobe", "av-scenechange"} {
		p := filepath.Join(binDir, toolbin.ToolName(tool))
		if !toolbin.FileExists(p) {
			return "", fmt.Errorf("bundled tool missing: %s\nrun 'go run ./cmd/engram-setup' first", p)
		}
	}
	opt.logf("[package] bundled tools OK")

	// 2) 本体ビルド → build/<engram-opt>.exe（バイナリ名はアプリ名）
	// Zip名と同一のバージョンを実行バイナリへ埋め込み、--version で確認できるようにする
	exe := filepath.Join(buildDir, toolbin.ToolName("engram-opt"))
	if err := goBuild(root, exe, opt.Version); err != nil {
		return "", fmt.Errorf("building engram-opt: %w", err)
	}
	opt.logf("[package] built %s", exe)

	// 3) THIRD-PARTY-NOTICES.txt 自動生成（ビルド済み本体から実リンク依存を収集）
	notices, err := BuildThirdPartyNotices(root, exe)
	if err != nil {
		return "", fmt.Errorf("generating third-party notices: %w", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "THIRD-PARTY-NOTICES.txt"), []byte(notices), 0o644); err != nil {
		return "", err
	}
	opt.logf("[package] wrote THIRD-PARTY-NOTICES.txt (full license texts embedded)")

	// 4) LICENSE 同梱 + 5) tmp プレースホルダ
	if err := toolbin.CopyFile(filepath.Join(root, "LICENSE"), filepath.Join(buildDir, "LICENSE")); err != nil {
		return "", fmt.Errorf("copying LICENSE: %w", err)
	}
	// tmp/ は実行時の一時領域であり前回実行の残骸（チャンク・中断ジョブ等）を含み得る。
	// 配布Zipへの混入を防ぐため、ステージング時に必ず空から作り直す。
	tmpDir := filepath.Join(buildDir, "tmp")
	if err := os.RemoveAll(tmpDir); err != nil {
		return "", fmt.Errorf("resetting staging tmp dir: %w", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", err
	}
	placeholder := []byte("This directory is recreated at runtime. Safe to delete.\n")
	if err := os.WriteFile(filepath.Join(tmpDir, ".placeholder"), placeholder, 0o644); err != nil {
		return "", err
	}
	// README も配布物に同梱する（memo.md 配置図どおり README.txt として）
	if err := toolbin.CopyFile(filepath.Join(root, "README.md"), filepath.Join(buildDir, "README.txt")); err != nil {
		return "", fmt.Errorf("copying README: %w", err)
	}
	opt.logf("[package] staged LICENSE / README.txt / tmp/")

	if opt.NoZip {
		return "", nil
	}

	// 6) Zip化: build/ の中身をZipルートへ展開する形で dist/ へ出力
	out := filepath.Join(root, opt.OutDir, fmt.Sprintf("engram-opt_%s_%s-%s.zip", opt.Version, runtime.GOOS, runtime.GOARCH))
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	n, err := zipDirectory(buildDir, out)
	if err != nil {
		return "", fmt.Errorf("creating zip: %w", err)
	}
	opt.logf("[package] created %s (%d entries)", out, n)
	return out, nil
}

// ===== 内部ヘルパ =====

// goBuild はリポジトリルートで本体をビルドし outPath へ出力する。
// クロスコンパイルは行わない（ホストOS/arch向け。配布は現行pin windows/amd64前提）。
func goBuild(root, outPath, version string) error {
	args := []string{"build"}
	if version != "" {
		args = append(args, "-ldflags", "-X main.version="+version)
	}
	args = append(args, "-o", outPath, "./cmd/engram-opt")
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build: %w\n%s", err, toolbin.Tail(string(b), 15))
	}
	return nil
}

// zipDirectory は srcDir の中身（再帰）を dstZip のルート直下へ格納する。
// エントリのタイムスタンプをエポックに固定し、同内容なら同一ハッシュになるよう
// 決定論的に生成する（再現性のある配布物）。
func zipDirectory(srcDir, dstZip string) (int, error) {
	f, err := os.Create(dstZip)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	count := 0
	err = filepath.WalkDir(srcDir, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(srcDir, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil // ルート自身は格納しない
		}
		name := filepath.ToSlash(rel)

		hdr := &zip.FileHeader{Name: name, Modified: time.Unix(0, 0).UTC()}
		if d.IsDir() {
			hdr.Name += "/"
			if _, err := zw.CreateHeader(hdr); err != nil {
				return err
			}
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		hdr.SetMode(info.Mode())
		w, cerr := zw.CreateHeader(hdr)
		if cerr != nil {
			return cerr
		}
		src, oerr := os.Open(path)
		if oerr != nil {
			return oerr
		}
		defer src.Close()
		if _, cerr = io.Copy(w, src); cerr != nil {
			return cerr
		}
		count++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, zw.Close()
}
