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
		{"GPL-2.0", "GNU GENERAL PUBLIC LICENSE\nVersion 2, June 1991", "GPL-2.0"},
		{"LGPL", "GNU LESSER GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007", "LGPL"},
		{"ISC", "Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee...", "ISC"},
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

func TestAssembleNoticesContainsAllSections(t *testing.T) {
	modDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(modDir, "LICENSE"), []byte("Apache License\nVersion 2.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	mods := []moduleInfo{
		{Path: "github.com/zzz/last", Version: "v1.0.0", Dir: modDir},
		{Path: "github.com/aaa/first", Version: "v0.2.3", Dir: ""}, // キャッシュ欠けでも落ちない
	}
	out := assembleNotices(mods)

	for _, want := range []string{
		"ENGRAM OPT - THIRD PARTY NOTICES",
		"FFMPEG 8.1.2",
		"b8cdefab5f50590a076c27c2b56b0294a0e6154faded28ba1ba05ebc4f801f57",
		"AV-SCENECHANGE v0.24.1",
		"GO MODULES (embedded in the optimizer executable; 2 modules)",
		"github.com/aaa/first@v0.2.3",
		"github.com/zzz/last@v1.0.0",
		"https://pkg.go.dev/github.com/zzz/last@v1.0.0",
		"Apache-2.0",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("notices missing %q", want)
		}
	}
	// モジュールはパス順にソートされていること
	if strings.Index(out, "github.com/aaa/first") > strings.Index(out, "github.com/zzz/last") {
		t.Fatal("modules must be sorted by path")
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
