package packaging

// THIRD-PARTY-NOTICES.txt の自動生成。
//
// 方式は memo.md 配置図の定義どおり「ライセンス種別＋ソースコードURLの一覧」。
// Go依存モジュールは go list -m all の実依存グラフから収集し、モジュールキャッシュ内の
// LICENSE/COPYING を実読みして種別を分類する（手動の推測を入れない）。
// 同梱外部バイナリ（FFmpeg / av-scenechange）は pin 済みの静的情報を埋め込む。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// moduleInfo はGo依存モジュール1件分の情報。
type moduleInfo struct {
	Path    string
	Version string
	Dir     string // モジュールキャッシュ上の実ディレクトリ（取得できない場合は空）
}

// listModules は main 以外の全依存モジュールを go list -m で収集する。
func listModules(root string) ([]moduleInfo, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}", "all")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -m failed: %w", err)
	}
	var mods []moduleInfo
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(parts) != 3 || parts[0] == "" {
			continue // mainモジュールや空行はスキップ
		}
		mods = append(mods, moduleInfo{Path: parts[0], Version: parts[1], Dir: parts[2]})
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Path < mods[j].Path })
	return mods, nil
}

// BuildThirdPartyNotices は THIRD-PARTY-NOTICES.txt の全文を組み立てる。
func BuildThirdPartyNotices(root string) (string, error) {
	mods, err := listModules(root)
	if err != nil {
		return "", err
	}
	return assembleNotices(mods), nil
}

func assembleNotices(mods []moduleInfo) string {
	// 呼び出し順に依存せず常にパス順で出す（純関数としての自己防衛）
	sort.Slice(mods, func(i, j int) bool { return mods[i].Path < mods[j].Path })

	var b strings.Builder

	b.WriteString(`ENGRAM OPT - THIRD PARTY NOTICES
================================

This distribution bundles the following third-party software.
EngramOpt itself is MIT licensed and invokes bundled binaries as separate
processes (no source-level linking); each component below remains under its
own license.

`)
	b.WriteString(ffmpegNotice)
	b.WriteString("\n")
	b.WriteString(avScenechangeNotice)
	b.WriteString("\n")

	fmt.Fprintf(&b, `GO MODULES (embedded in the optimizer executable; %d modules)
---------------------------------------------------------------`, len(mods))
	b.WriteString("\n\n")
	for _, m := range mods {
		lic, file := classifyLicense(m.Dir)
		line := fmt.Sprintf("%s@%s\n    License: %s\n    Source:  https://pkg.go.dev/%s@%s",
			m.Path, m.Version, lic, m.Path, m.Version)
		if file != "" && !strings.HasPrefix(lic, "unknown") {
			line += "\n    Text:    in module cache: " + filepath.Join(m.Dir, file)
		}
		b.WriteString(line + "\n\n")
	}

	b.WriteString(`NOTICE FORMAT NOTE
------------------
License texts are referenced by canonical URLs / module cache paths instead of
being embedded verbatim. See each component's repository for the full text.
`)
	return b.String()
}

// ===== 同梱バイナリの固定情報（internal/setup のpinと対で更新すること） =====

const ffmpegNotice = `FFMPEG 8.1.2 (full_build, static executables: ffmpeg.exe / ffprobe.exe)
-----------------------------------------------------------------------
- Upstream source : https://ffmpeg.org/download.html
                    https://github.com/FFmpeg/FFmpeg/tree/n8.1.2
- Prebuilt binaries (pinned): https://github.com/GyanD/codexffmpeg/releases/tag/8.1.2
  ("full_build" variant, SHA256 b8cdefab5f50590a076c27c2b56b0294a0e6154faded28ba1ba05ebc4f801f57)
- License         : GNU General Public License version 3 (GPLv3).
  This build is configured with GPL components (--enable-gpl), therefore the
  GPLv3 applies to these binaries. The full license text is available at:
  https://www.gnu.org/licenses/gpl-3.0.txt
  EngramOpt does not modify FFmpeg and only executes it as an external process.
`

const avScenechangeNotice = `AV-SCENECHANGE v0.24.1 (static executable, built locally from pinned source)
----------------------------------------------------------------------------
- Upstream source (pinned): https://github.com/rust-av/av-scenechange/tree/v0.24.1
- License         : MIT License, Copyright (c) 2019 Multimedia and Rust.
  Full text: https://github.com/rust-av/av-scenechange/blob/v0.24.1/LICENSE
  The same LICENSE file also carries third-party notices for vendored
  assembly sources (dav1d contributors: BSD-style, x264 authors: ISC,
  rav1e tables: BSD-2-Clause with Alliance for Open Media Patent License 1.0).
`

// ===== ライセンス分類（推測なし・ファイル実読み） =====

var licenseFileNames = []string{"LICENSE", "LICENCE", "COPYING", "COPYING.LESSER", "NOTICE", "UNLICENSE"}

// classifyLicense はモジュールディレクトリ直下のライセンスファイルを読み、
// 種別（SPDX風ラベル）と該当ファイル名を返す。見つからなければ unknown。
func classifyLicense(dir string) (string, string) {
	if dir == "" {
		return "unknown (module cache unavailable)", ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "unknown", ""
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

	var firstFile string
	for _, name := range candidates {
		data, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			continue
		}
		if firstFile == "" {
			firstFile = name
		}
		if lic := detectLicenseText(string(data)); lic != "" {
			return lic, name
		}
	}
	if firstFile != "" {
		return "custom (see LICENSE file)", firstFile
	}
	return "unknown", ""
}

// detectLicenseText はライセンス文の特徴句からSPDX風ラベルを返す（非該当時""）。
func detectLicenseText(s string) string {
	u := strings.ToUpper(s)
	switch {
	case strings.Contains(u, "APACHE LICENSE") && strings.Contains(u, "VERSION 2"):
		return "Apache-2.0"
	case strings.Contains(u, "GNU LESSER GENERAL PUBLIC LICENSE"):
		return "LGPL"
	case strings.Contains(u, "GNU GENERAL PUBLIC LICENSE") && strings.Contains(u, "VERSION 3"):
		return "GPL-3.0"
	case strings.Contains(u, "GNU GENERAL PUBLIC LICENSE"):
		return "GPL-2.0"
	case strings.Contains(u, "MOZILLA PUBLIC LICENSE"):
		return "MPL-2.0"
	case strings.Contains(u, "PERMISSION IS HEREBY GRANTED, FREE OF CHARGE"):
		return "MIT"
	case strings.Contains(u, "PERMISSION TO USE, COPY, MODIFY, AND/OR DISTRIBUTE THIS SOFTWARE FOR ANY PURPOSE WITH OR WITHOUT FEE"):
		return "ISC"
	case strings.Contains(u, "REDISTRIBUTION AND USE IN SOURCE AND BINARY FORMS"):
		return "BSD-style"
	default:
		return ""
	}
}
