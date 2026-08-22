package packaging

// THIRD-PARTY-NOTICES.txt の生成（配布コンプライアンス対応版）。
//
// 方針（memo.md「パッケージング」）:
//   - 同梱物ごとに「ライセンス全文」を埋め込む。GPLv3は本文同梱が配布要件であり、
//     MIT/Apacheも著作権表示＋許諾文の同梱が条件のため（URL参照では不足）。
//   - Goモジュールはビルド済み本体から `go version -m` で実リンク依存のみを収集する
//     （go.mod上の全推移グラフではなく、バイナリに実際に入ったものだけ）。
//   - 本文は必ず一次ファイルの実読み（モジュールキャッシュ／リポジトリ固定ファイル）。
//     推測・要約は行わない。埋め込みできない依存が1件でもあれば配布を停止する。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// 固定ライセンス原文（リポジトリへベンダリング済み。更新時は一次元から再取得して差し替え）。
const (
	vendoredGPLv3 = "third_party/licenses/COPYING.GPLv3.txt"
	vendoredAsc   = "third_party/licenses/av-scenechange-LICENSE-v0.24.1.txt"
	overridesDir  = "third_party/licenses/modules"
)

// overrideLicensePath は上流タグzipにLICENSEが含まれない依存向けの
// ベンダリング原文パスを返す（存在しなければ空文字）。
// 命名規約: モジュールパスの "/"→"_"、"@"→"_at_" + "_LICENSE.txt"
func overrideLicensePath(root, path, version string) string {
	key := strings.ReplaceAll(path, "/", "_") + "_at_" + version + "_LICENSE.txt"
	p := filepath.Join(root, overridesDir, key)
	if fileExistsAt(p) {
		return p
	}
	return ""
}

func fileExistsAt(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// moduleInfo はGo依存モジュール1件分の情報。
type moduleInfo struct {
	Path    string
	Version string
	Dir     string // モジュールキャッシュ上の実ディレクトリ
}

// resolvedModule はライセンス本文の埋め込みまで完了した依存情報。
type resolvedModule struct {
	moduleInfo
	LicenseType string // 分類ラベル（例: MIT / Apache-2.0）
	Text        string // ライセンス全文（LICENSEファイルを逐字）
}

// parseGoVersionM は `go version -m <exe>` の出力から dep 行を抽出する。
// 行形式: "\tdep\t<module path>\t<version>\t<checksum>"（checksumは省略され得る）。
func parseGoVersionM(output []byte) []moduleInfo {
	var mods []moduleInfo
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, "\tdep\t") {
			continue // mod / build / path 行は対象外
		}
		fields := strings.Split(strings.TrimPrefix(line, "\tdep\t"), "\t")
		if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
			continue
		}
		mods = append(mods, moduleInfo{Path: fields[0], Version: fields[1]})
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Path < mods[j].Path })
	return mods
}

// goRunner は go サブコマンドを実行し stdout を返すための差し込み口（テスト用）。
type goRunner func(args []string) ([]byte, error)

// collectModules は実行バイナリへ静的にリンクされたモジュール一覧を取得する。
func collectModules(exePath string) ([]moduleInfo, error) {
	out, err := exec.Command("go", "version", "-m", exePath).Output()
	if err != nil {
		return nil, fmt.Errorf("go version -m %s: %w", exePath, err)
	}
	return parseGoVersionM(out), nil
}

// resolveModuleDirs は各モジュールのキャッシュ上ディレクトリを解決する。
// go mod download -json は未取得分を取得するため、一度ネットワーク可能な環境で
// 実行していれば以後はオフラインでも解決する。解決できないものが残ればエラー
// （＝配布停止。unknown のまま出荷しないための安全側）。
func resolveModuleDirs(run goRunner, mods []moduleInfo) error {
	if len(mods) == 0 {
		return nil
	}
	specs := make([]string, 0, len(mods))
	for _, m := range mods {
		specs = append(specs, m.Path+"@"+m.Version)
	}
	out, err := run(append([]string{"mod", "download", "-json"}, specs...))
	if err != nil {
		return fmt.Errorf("go mod download: %w", err)
	}

	dirs := map[string]string{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var e struct {
			Path string
			Dir  string
		}
		if err := dec.Decode(&e); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("parsing go mod download output: %w", err)
		}
		dirs[e.Path] = e.Dir
	}

	var missing []string
	for i := range mods {
		d := dirs[mods[i].Path]
		if d == "" {
			missing = append(missing, mods[i].Path)
			continue
		}
		mods[i].Dir = d
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("could not resolve module cache dirs for: %s\nrun once with network access ('go mod download') and retry",
			strings.Join(missing, ", "))
	}
	return nil
}

// ===== ライセンスファイルの探索と全文埋め込み =====

// findLicenseFile は dir 直下から主ライセンスファイルを選び（内容実読みで分類）、
// 絶対パス・分類ラベルを返す。認識可能なものが無ければ ok=false。
func findLicenseFile(dir string) (path string, label string, ok bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", false
	}
	var candidates []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := strings.ToUpper(e.Name())
		for _, pfx := range licenseFileNames {
			if strings.HasPrefix(n, pfx) {
				candidates = append(candidates, e.Name())
				break
			}
		}
	}
	sort.Strings(candidates)
	for _, name := range candidates {
		data, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			continue
		}
		if lic := detectLicenseText(string(data)); lic != "" {
			return filepath.Join(dir, name), lic, true
		}
	}
	return "", "", false
}

// embedModuleLicense はモジュールのライセンス全文を読み込み resolvedModule を返す。
// モジュールキャッシュ内にLICENSEが無い場合、ベンダリング済みの上流原文
// （overridesDir、タグzip欠落対策）へフォールバックする。どちらも無ければエラー。
func embedModuleLicense(root string, m moduleInfo) (resolvedModule, error) {
	if p := overrideLicensePath(root, m.Path, m.Version); p != "" {
		data, err := os.ReadFile(p)
		if err != nil {
			return resolvedModule{}, fmt.Errorf("%s@%s: reading vendored license: %w", m.Path, m.Version, err)
		}
		label := detectLicenseText(string(data))
		if label == "" {
			return resolvedModule{}, fmt.Errorf("%s@%s: vendored override %s is not a recognizable license",
				m.Path, m.Version, p)
		}
		return resolvedModule{
			moduleInfo:  m,
			LicenseType: label,
			Text: strings.TrimRight(string(data), "\r\n") +
				fmt.Sprintf("\n\n(Note: text vendored from upstream repository; the %s module zip does not ship a LICENSE file.)", m.Version),
		}, nil
	}
	if m.Dir == "" {
		return resolvedModule{}, fmt.Errorf("%s@%s: source not available in module cache", m.Path, m.Version)
	}
	path, label, ok := findLicenseFile(m.Dir)
	if !ok {
		key := strings.ReplaceAll(m.Path, "/", "_") + "_at_" + m.Version + "_LICENSE.txt"
		return resolvedModule{}, fmt.Errorf("%s@%s: no recognizable LICENSE file under %s\n"+
			"    -> if the upstream tag omits a LICENSE file, vendor its official text as:\n"+
			"       %s",
			m.Path, m.Version, m.Dir, filepath.Join(overridesDir, key))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return resolvedModule{}, fmt.Errorf("%s@%s: reading %s: %w", m.Path, m.Version, path, err)
	}
	return resolvedModule{
		moduleInfo:  m,
		LicenseType: label,
		Text:        strings.TrimRight(string(data), "\r\n"),
	}, nil
}

// ===== ライセンス分類（推測なし・ファイル実読み） =====

var licenseFileNames = []string{"LICENSE", "LICENCE", "COPYING", "COPYING.LESSER", "NOTICE", "UNLICENSE"}

// detectLicenseText はライセンス文の特徴句からSPDX風ラベルを返す（非該当時""）。
//
// 判定は二段構え:
//  1. 文書タイトル（正規化後の先頭200文字）でGPL系ファミリを判定する
//     （GPLv3本文中に「Lesser」「Affero」への言及が存在するため、本文全体での
//     部分一致では誤分類する。実測済み）
//  2. タイトルを持たないMIT/ISC/BSD系は本文全体の特徴句で判定する
func detectLicenseText(s string) string {
	u := strings.ToUpper(strings.Join(strings.Fields(s), " "))
	headLen := len(u)
	if headLen > 200 {
		headLen = 200
	}
	title := u[:headLen]

	switch {
	case strings.Contains(title, "APACHE LICENSE") && strings.Contains(title, "VERSION 2"):
		return "Apache-2.0"
	case strings.Contains(title, "MOZILLA PUBLIC LICENSE"):
		return "MPL-2.0"
	case strings.Contains(title, "GNU LESSER GENERAL PUBLIC LICENSE"):
		if strings.Contains(title, "VERSION 2.1") {
			return "LGPL-2.1"
		}
		return "LGPL-3.0"
	case strings.Contains(title, "GNU AFFERO GENERAL PUBLIC LICENSE"):
		return "AGPL-3.0"
	case strings.Contains(title, "GNU GENERAL PUBLIC LICENSE"):
		if strings.Contains(title, "VERSION 3") {
			return "GPL-3.0"
		}
		if strings.Contains(title, "VERSION 2") {
			return "GPL-2.0"
		}
		return "GPL"
	}

	switch {
	case strings.Contains(u, "PERMISSION IS HEREBY GRANTED, FREE OF CHARGE"):
		return "MIT"
	case strings.Contains(u, "PERMISSION TO USE, COPY, MODIFY, AND/OR DISTRIBUTE THIS SOFTWARE FOR ANY PURPOSE WITH OR WITHOUT FEE"):
		return "ISC / 0BSD"
	case strings.Contains(u, "REDISTRIBUTION AND USE IN SOURCE AND BINARY FORMS"):
		return "BSD-style"
	default:
		return ""
	}
}

// classifyLicense はディレクトリ直下のライセンスを分類ラベルとファイル名で返す。
// 埋め込み経路（findLicenseFile）と同一の選択規則を使う単一ソース。
func classifyLicense(dir string) (label, fileName string) {
	path, label, ok := findLicenseFile(dir)
	if ok {
		return label, filepath.Base(path)
	}
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			n := strings.ToUpper(e.Name())
			for _, pfx := range licenseFileNames {
				if strings.HasPrefix(n, pfx) {
					return "custom (see LICENSE file)", e.Name()
				}
			}
		}
	}
	return "unknown", ""
}

// loadVendoredText は固定ライセンス原文を読み、分類ラベルが期待どおりか検証する。
// ベンダリング済みファイルの破損・取り違えを出荷前に検知するためのガード。
func loadVendoredText(root, rel, wantLabel, displayName string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return "", fmt.Errorf("vendored license missing (%s): %w", rel, err)
	}
	text := strings.TrimRight(string(data), "\r\n")
	if got := detectLicenseText(text); got != wantLabel {
		return "", fmt.Errorf("vendored %s does not classify as %s (got %q); re-fetch from the canonical source into %s",
			displayName, wantLabel, orDefault(got, "unclassified"), rel)
	}
	return text, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ===== 生成 =====

// BuildThirdPartyNotices はビルド済み本体 exe の実リンク依存に基づき
// THIRD-PARTY-NOTICES.txt 全文を組み立てる。失敗時は配布物を作らない。
func BuildThirdPartyNotices(root, exePath string) (string, error) {
	mods, err := collectModules(exePath)
	if err != nil {
		return "", err
	}
	run := func(args []string) ([]byte, error) {
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		return cmd.Output()
	}
	if err := resolveModuleDirs(run, mods); err != nil {
		return "", err
	}

	resolved := make([]resolvedModule, 0, len(mods))
	var failures []string
	for _, m := range mods {
		rm, err := embedModuleLicense(root, m)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		resolved = append(resolved, rm)
	}
	// 1件でも埋め込み不能なら全体を失敗させ、全件を一度に列挙する
	// （一件ずつの修正ループを防ぐ）。
	if len(failures) > 0 {
		sort.Strings(failures)
		return "", fmt.Errorf("cannot embed licenses for %d module(s):\n  - %s",
			len(failures), strings.Join(failures, "\n  - "))
	}

	gplText, err := loadVendoredText(root, vendoredGPLv3, "GPL-3.0", "FFmpeg GPLv3")
	if err != nil {
		return "", err
	}
	ascText, err := loadVendoredText(root, vendoredAsc, "MIT", "av-scenechange MIT")
	if err != nil {
		return "", err
	}
	return assembleNotices(gplText, ascText, resolved), nil
}

func assembleNotices(ffmpegGPLText, ascMITText string, mods []resolvedModule) string {
	var b strings.Builder

	b.WriteString(`ENGRAM OPT - THIRD PARTY NOTICES
================================

This distribution bundles third-party software. EngramOpt itself is MIT
licensed (see LICENSE) and invokes bundled executables as separate processes;
no source-level linking occurs. Each bundled component keeps its own license,
whose FULL TEXT is included below.

Contents:
  1. FFmpeg 8.1.2 (GPLv3)            -> ffmpeg.exe / ffprobe.exe
  2. av-scenechange v0.24.1 (MIT)    -> av-scenechange.exe
  3. Go modules embedded in optimizer.exe

`)
	fmt.Fprintf(&b, `================================================================================
1. FFMPEG 8.1.2 (full_build, static executables)
================================================================================
- Upstream source : https://ffmpeg.org/download.html
                    https://github.com/FFmpeg/FFmpeg/tree/n8.1.2
- Prebuilt binaries (pinned): https://github.com/GyanD/codexffmpeg/releases/tag/8.1.2
  ("full_build" variant, SHA256 b8cdefab5f50590a076c27c2b56b0294a0e6154faded28ba1ba05ebc4f801f57)
- License         : GNU General Public License version 3 (GPLv3). This build is
  configured with GPL components (--enable-gpl), so the GPLv3 applies to these
  binaries. EngramOpt conveys them unmodified as published at the URL above;
  per GPLv3 section 6, the Corresponding Source is obtainable from the same
  location for as long as EngramOpt distributes these binaries.

FULL LICENSE TEXT:

%s

`, ffmpegGPLText)

	fmt.Fprintf(&b, `================================================================================
2. AV-SCENECHANGE v0.24.1 (static executable, built locally from pinned source)
================================================================================
- Upstream source (pinned): https://github.com/rust-av/av-scenechange/tree/v0.24.1
- License         : MIT License, Copyright (c) 2019 Multimedia and Rust.
  The vendored LICENSE file also carries third-party notices for vendored
  assembly sources (dav1d contributors: BSD-style, x264 authors: ISC,
  rav1e tables: BSD-2-Clause with Alliance for Open Media Patent License 1.0).

FULL LICENSE TEXT:

%s

`, ascMITText)

	fmt.Fprintf(&b, `================================================================================
3. GO MODULES EMBEDDED IN OPTIMIZER.EXE (%d modules)
================================================================================
Collected from the built binary's embedded build info ("go version -m"):
only modules actually linked into the executable are listed.
`, len(mods))

	for _, m := range mods {
		fmt.Fprintf(&b, `
--- %s@%s ------------------------------------------------------------
License: %s
Source : https://pkg.go.dev/%s@%s

%s
`, m.Path, m.Version, m.LicenseType, m.Path, m.Version, m.Text)
	}

	return b.String()
}
