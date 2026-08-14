package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSha256sum_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	h, err := sha256sum(path)
	if err != nil {
		t.Fatalf("sha256sum: %v", err)
	}
	// SHA-256 of empty string
	expected := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if h != expected {
		t.Errorf("expected %s, got %s", expected, h)
	}
}

func TestSha256sum_Consistency(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	content := []byte("package main\nfunc main() {}\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	h1, _ := sha256sum(path)
	h2, _ := sha256sum(path)
	if h1 != h2 {
		t.Errorf("sha256sum should be consistent: %s vs %s", h1, h2)
	}
}

func TestSha256sum_DifferentContent(t *testing.T) {
	dir := t.TempDir()
	path1 := filepath.Join(dir, "a.go")
	path2 := filepath.Join(dir, "b.go")
	os.WriteFile(path1, []byte("package a"), 0644)
	os.WriteFile(path2, []byte("package b"), 0644)

	h1, _ := sha256sum(path1)
	h2, _ := sha256sum(path2)
	if h1 == h2 {
		t.Error("different files should have different hashes")
	}
}

func TestSha256sum_FileNotFound(t *testing.T) {
	_, err := sha256sum("/nonexistent/file.go")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestCountLines(t *testing.T) {
	tests := []struct {
		content string
		want    int
	}{
		{"", 0},
		{"line1", 1},
		{"line1\n", 1},
		{"line1\nline2", 2},
		{"line1\nline2\nline3\n", 3},
		{"\n", 1},
	}
	for _, tt := range tests {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.txt")
		os.WriteFile(path, []byte(tt.content), 0644)
		got := countLines(path)
		if got != tt.want {
			t.Errorf("countLines(%q) = %d, want %d", tt.content, got, tt.want)
		}
	}
}

func TestDetectLang(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"main.go", "go"},
		{"Main.java", "java"},
		{"util.py", "py"},
		{"component.ts", "ts"},
		{"component.tsx", "ts"},
		{"app.js", "js"},
		{"app.jsx", "js"},
		{"lib.rs", "rust"},
		{"main.cpp", "cpp"},
		{"main.cc", "cpp"},
		{"main.c", "c"},
		{"header.h", "cpp"},
		{"readme.unknown_ext", "unknown"},
		{"Makefile", "unknown"},
	}
	for _, tt := range tests {
		got := detectLang(tt.path)
		if got != tt.want {
			t.Errorf("detectLang(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
