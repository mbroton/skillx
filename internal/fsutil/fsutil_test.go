package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReplaceDirRefusesSameDir(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "skills", "alpha")
	writeFile(t, filepath.Join(skill, "SKILL.md"), "# alpha")

	if err := ReplaceDir(skill, skill); err == nil {
		t.Fatal("want overlap error, got nil")
	}
	// The source must survive the refusal.
	if _, err := os.Stat(filepath.Join(skill, "SKILL.md")); err != nil {
		t.Fatalf("source destroyed: %v", err)
	}
}

func TestReplaceDirRefusesSameDirViaSymlink(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "skills", "alpha")
	writeFile(t, filepath.Join(skill, "SKILL.md"), "# alpha")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(skill, link); err != nil {
		t.Fatal(err)
	}

	if err := ReplaceDir(link, skill); err == nil {
		t.Fatal("want overlap error for symlinked source, got nil")
	}
	if _, err := os.Stat(filepath.Join(skill, "SKILL.md")); err != nil {
		t.Fatalf("source destroyed: %v", err)
	}
}

func TestReplaceDirRefusesNesting(t *testing.T) {
	dir := t.TempDir()
	outer := filepath.Join(dir, "outer")
	writeFile(t, filepath.Join(outer, "SKILL.md"), "# outer")

	if err := ReplaceDir(outer, filepath.Join(outer, "inner")); err == nil {
		t.Fatal("want overlap error for dst inside src, got nil")
	}
	if err := ReplaceDir(filepath.Join(outer, "inner"), outer); err == nil {
		t.Fatal("want overlap error for src inside dst, got nil")
	}
}

func TestReplaceDirReplacesContent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	writeFile(t, filepath.Join(src, "SKILL.md"), "new")
	writeFile(t, filepath.Join(dst, "SKILL.md"), "old")
	writeFile(t, filepath.Join(dst, "stale.txt"), "stale")

	if err := ReplaceDir(src, dst); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if err != nil || string(data) != "new" {
		t.Fatalf("content not replaced: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "stale.txt")); !os.IsNotExist(err) {
		t.Error("stale file survived the replace")
	}
	// No staging leftovers next to dst.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "src" && e.Name() != "dst" {
			t.Errorf("staging leftover: %s", e.Name())
		}
	}
}

func TestReplaceDirKeepsDstOnCopyFailure(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst")
	writeFile(t, filepath.Join(dst, "SKILL.md"), "precious")

	if err := ReplaceDir(filepath.Join(dir, "missing"), dst); err == nil {
		t.Fatal("want error for missing source")
	}
	data, err := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if err != nil || string(data) != "precious" {
		t.Fatalf("dst damaged by failed replace: %q, %v", data, err)
	}
}

func TestMoveDirRefusesOverlap(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	writeFile(t, filepath.Join(src, "SKILL.md"), "x")

	if err := MoveDir(src, filepath.Join(src, "sub")); err == nil {
		t.Fatal("want overlap error, got nil")
	}
	if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
		t.Fatalf("source destroyed: %v", err)
	}
}
