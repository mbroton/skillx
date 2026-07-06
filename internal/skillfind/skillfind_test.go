package skillfind

import (
	"os"
	"path/filepath"
	"testing"
)

func mkSkill(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, MarkerFile), []byte("# skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(skills []Skill) []string {
	out := make([]string, len(skills))
	for i, s := range skills {
		out[i] = s.Name
	}
	return out
}

func TestDiscoverRootIsSkill(t *testing.T) {
	root := t.TempDir()
	mkSkill(t, root)
	// Nested skills must be ignored when the root itself is a skill.
	mkSkill(t, filepath.Join(root, "nested"))

	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != filepath.Base(root) {
		t.Fatalf("got %v, want just the root skill", names(got))
	}
}

func TestDiscoverChildrenAndSkillsDir(t *testing.T) {
	root := t.TempDir()
	mkSkill(t, filepath.Join(root, "alpha"))
	mkSkill(t, filepath.Join(root, "skills", "beta"))
	mkSkill(t, filepath.Join(root, "skills", "gamma"))
	// Not skills:
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Too deep — must not be found:
	mkSkill(t, filepath.Join(root, "skills", "beta", "sub", "deep"))

	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", names(got), want)
	}
	for i, n := range want {
		if got[i].Name != n {
			t.Errorf("got[%d] = %s, want %s", i, got[i].Name, n)
		}
	}
}

func TestDiscoverDuplicateNameFirstWins(t *testing.T) {
	root := t.TempDir()
	mkSkill(t, filepath.Join(root, "dup"))
	mkSkill(t, filepath.Join(root, "skills", "dup"))

	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %v, want one 'dup'", names(got))
	}
	if got[0].Dir != filepath.Join(root, "dup") {
		t.Errorf("root-level skill should win, got %s", got[0].Dir)
	}
}

func TestDiscoverEmpty(t *testing.T) {
	got, err := Discover(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want none", names(got))
	}
}

func TestDiscoverSkillMdMustBeFile(t *testing.T) {
	root := t.TempDir()
	// SKILL.md as a directory does not count.
	if err := os.MkdirAll(filepath.Join(root, "fake", MarkerFile), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want none", names(got))
	}
}

func TestFilter(t *testing.T) {
	root := t.TempDir()
	mkSkill(t, filepath.Join(root, "a"))
	mkSkill(t, filepath.Join(root, "b"))
	all, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Filter(all, []string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("got %v, want [b]", names(got))
	}

	if _, err := Filter(all, []string{"nope"}); err == nil {
		t.Error("expected error for unknown skill name")
	}

	got, err = Filter(all, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("nil filter should return all, got %v (%v)", names(got), err)
	}
}
