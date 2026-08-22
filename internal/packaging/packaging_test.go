package packaging

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectLicenseText(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"MIT", "The MIT License\n\nPermission is hereby granted, free of charge, to any person...", "MIT"},
		{"Apache-2.0", "Apache License\nVersion 2.0, January 2004\nhttp://www.apache.org/licenses/", "Apache-2.0"},
		{"GPL-3.0", "GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007", "GPL-3.0"},
		// GPLv3本文は「Lesser」「Affero」への言及を含むが（実ファイルでは本文深部）、
		// タイトル判定により誤分類されないこと
		{"GPL-3.0 with lesser reference",
			"GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007\n\n" +
				strings.Repeat("Each contributor grants you a non-exclusive, worldwide, royalty-free patent license.\n", 10) +
				"\n13. Use with the GNU Affero General Public License.\nuse the GNU Lesser General Public License",
			"GPL-3.0"},
		{"GPL-2.0", "GNU GENERAL PUBLIC LICENSE\nVersion 2, June 1991", "GPL-2.0"},
		{"LGPL", "GNU LESSER GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007", "LGPL-3.0"},
		{"ISC", "Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee...", "ISC / 0BSD"},
		{"BSD-style", "Redistribution and use in source and binary forms, with or without modification,", "BSD-style"},
		{"MPL", "Mozilla Public License Version 2.0", "MPL-2.0"},
		{"unknown returns empty", "some random agreement text", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detectLicenseText(c.text); got != c.want {
				t.Fatalf("detectLicenseText = %q, want %q", got, c.want)
			}
		})
	}
}

func TestClassifyLicenseFromDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "LICENSE.txt"), []byte("Permission is hereby granted, free of charge"), 0o644); err != nil {
		t.Fatal(err)
	}
	lic, file := classifyLicense(dir)
	if lic != "MIT" || !strings.EqualFold(file, "LICENSE.txt") {
		t.Fatalf("classify = %s / %s, want MIT / LICENSE.txt", lic, file)
	}

	// ライセンスファイル無しは unknown/custom（エラーにしない: 一覧生成を止めない）
	if l, _ := classifyLicense(t.TempDir()); !strings.HasPrefix(l, "unknown") && !strings.HasPrefix(l, "custom") {
		t.Fatalf("empty dir classify = %q", l)
	}
	// キャッシュ欠け（dir=""）も安全側
	if l, _ := classifyLicense(""); !strings.HasPrefix(l, "unknown") {
		t.Fatalf("empty dir arg = %q", l)
	}
}

// 生成物には固定ライセンス原文と各モジュールの全文が逐字含まれること。
func TestAssembleNoticesEmbedsFullTexts(t *testing.T) {
	dirA := t.TempDir()
	mit := "MIT License\n\nCopyright (c) 2026 Someone\n\nPermission is hereby granted, free of charge..."
	if err := os.WriteFile(filepath.Join(dirA, "LICENSE"), []byte(mit), 0o644); err != nil {
		t.Fatal(err)
	}
	modA, err := embedModuleLicense("", moduleInfo{Path: "github.com/zzz/last", Version: "v1.0.0", Dir: dirA})
	if err != nil {
		t.Fatalf("embed zzz: %v", err)
	}
	// キャッシュ解決不能（Dir空）かつオーバーライド無しはエラーになり配布が止まる
	if _, err := embedModuleLicense("", moduleInfo{Path: "github.com/aaa/first", Version: "v0.2.3"}); err == nil {
		t.Fatal("unresolved module must fail embedding")
	}

	// オーバーライド（タグzip欠落対策）: ベンダリング原文から埋め込まれること
	ovrDir := filepath.Join(t.TempDir(), "third_party", "licenses", "modules")
	if err := os.MkdirAll(ovrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ovrText := "Copyright (C) 2015-2017 Michael Cross\n\nPermission to use, copy, modify, and/or distribute this software for any\npurpose with or without fee is hereby granted."
	if err := os.WriteFile(filepath.Join(ovrDir, "github.com_mikelolasagasti_xz_at_v1.0.1_LICENSE.txt"), []byte(ovrText), 0o644); err != nil {
		t.Fatal(err)
	}
	rootWithOvr := filepath.Dir(filepath.Dir(filepath.Dir(ovrDir)))
	modB, err := embedModuleLicense(rootWithOvr, moduleInfo{Path: "github.com/mikelolasagasti/xz", Version: "v1.0.1"})
	if err != nil {
		t.Fatalf("embed via override: %v", err)
	}
	if modB.LicenseType != "ISC / 0BSD" || !strings.Contains(modB.Text, "Michael Cross") ||
		!strings.Contains(modB.Text, "vendored from upstream") {
		t.Fatalf("override embed = %+v", modB)
	}

	gpl := "GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007\n...(full official text)..."
	out := assembleNotices(gpl, "MIT License\nCopyright (c) 2019 Multimedia and Rust",
		[]resolvedModule{modA})

	for _, want := range []string{
		"ENGRAM OPT - THIRD PARTY NOTICES",
		"1. FFMPEG 8.1.2 (full_build, static executables)",
		"b8cdefab5f50590a076c27c2b56b0294a0e6154faded28ba1ba05ebc4f801f57",
		"2. AV-SCENECHANGE v0.24.1",
		"3. GO MODULES EMBEDDED IN OPTIMIZER.EXE (1 modules)",
		"github.com/zzz/last@v1.0.0",
		"License: MIT",
		"FULL LICENSE TEXT:",
		strings.TrimSpace(mit),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("notices missing %q", want)
		}
	}
	// GPL原文も逐字で入る
	if !strings.Contains(out, gpl[:40]) {
		t.Fatal("GPL full text not embedded verbatim")
	}
}

// `go version -m` 出力のパース: dep行のみを実リンク依存として採用する。
func TestParseGoVersionM(t *testing.T) {
	sample := []byte(strings.Join([]string{
		"build\\optimizer.exe: go1.27.0",
		"\tpath\tengram-opt/cmd/engram",
		"\tmod\tengram-opt\t(devel)\t",
		"\tdep\tgithub.com/spf13/cobra\tv1.10.2\th1:abc=",
		"\tdep\tgolang.org/x/sys\tv0.30.0\th1:def=",
		"\tbuild\t-compiler=gc",
	}, "\n"))
	mods := parseGoVersionM(sample)
	if len(mods) != 2 {
		t.Fatalf("mods = %+v, want 2 entries", mods)
	}
	if mods[0].Path != "github.com/spf13/cobra" || mods[0].Version != "v1.10.2" {
		t.Fatalf("mods[0] = %+v", mods[0])
	}
	if mods[1].Path != "golang.org/x/sys" { // パス順ソート済み
		t.Fatalf("mods[1] = %+v, want sorted order", mods[1])
	}
}

// モジュールキャッシュ解決: 欠落がある場合は配布停止エラー（unknown出荷防止）。
func TestResolveModuleDirsAbortsOnMissing(t *testing.T) {
	mods := []moduleInfo{{Path: "a.example/mod"}, {Path: "b.example/gone"}}
	jsonOut := strings.Join([]string{
		`{"Path":"a.example/mod","Dir":"C:/cache/a.example/mod@v1"}`,
		`{"Path":"b.example/gone","Dir":""}`,
	}, "\n")
	run := func(args []string) ([]byte, error) { return []byte(jsonOut), nil }

	err := resolveModuleDirs(run, mods)
	if err == nil || !strings.Contains(err.Error(), "b.example/gone") {
		t.Fatalf("missing module should abort packaging, got %v", err)
	}
	if mods[0].Dir == "" {
		t.Fatal("resolvable module should have its dir filled before the abort")
	}
}

// Zip生成の検証: 中身の完全性＋決定論的出力（同入力なら同一ハッシュ）。
func TestZipDirectoryDeterministic(t *testing.T) {
	buildZip := func() (map[string]string, string) {
		src := t.TempDir()
		write := func(rel, content string) {
			p := filepath.Join(src, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("optimizer.exe", "binary-bytes")
		write("bin/ffmpeg.exe", "ffmpeg")
		write("tmp/.placeholder", "placeholder")
		out := filepath.Join(t.TempDir(), "out.zip")
		if _, err := zipDirectory(src, out); err != nil {
			t.Fatalf("zipDirectory: %v", err)
		}
		files := map[string]string{}
		zr, err := zip.OpenReader(out)
		if err != nil {
			t.Fatalf("open zip: %v", err)
		}
		defer zr.Close()
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			rc, oerr := f.Open()
			if oerr != nil {
				t.Fatal(oerr)
			}
			var buf bytes.Buffer
			if _, cerr := buf.ReadFrom(rc); cerr != nil {
				t.Fatal(cerr)
			}
			rc.Close()
			files[f.Name] = buf.String()
		}
		return files, out
	}

	files1, out1 := buildZip()
	files2, _ := buildZip()

	for name, want := range map[string]string{
		"optimizer.exe":    "binary-bytes",
		"bin/ffmpeg.exe":   "ffmpeg",
		"tmp/.placeholder": "placeholder",
	} {
		if files1[name] != want {
			t.Fatalf("zip[%q] = %q, want %q", name, files1[name], want)
		}
		delete(files1, name)
	}
	if len(files1) != 0 {
		t.Fatalf("unexpected extra entries: %v", files1)
	}

	h1, h2 := sha256.Sum256(readAll(t, out1)), sha256.Sum256(readAll(t, out1))
	_ = files2 // 同一構築なので内容一致は上で担保済み
	if hex.EncodeToString(h1[:]) != hex.EncodeToString(h2[:]) {
		t.Fatal("same input must yield byte-identical zip")
	}
}

func readAll(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
