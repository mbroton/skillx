package manifest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadMissingIsEmpty(t *testing.T) {
	m, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 0 {
		t.Fatalf("want empty manifest, got %d skills", len(m.Skills))
	}
}

func TestRoundTripProvenance(t *testing.T) {
	dir := t.TempDir()
	m := New()
	m.Skills["pr-review"] = Skill{
		Source: "github.com/someone/repo",
		Path:   "skills/pr-review",
		Commit: "abc1234def",
	}

	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	pr := got.Skills["pr-review"]
	if pr.Source != "github.com/someone/repo" || pr.Path != "skills/pr-review" || pr.Commit != "abc1234def" {
		t.Errorf("provenance lost: %+v", pr)
	}
	if !pr.Vendored() {
		t.Error("pr-review should be vendored")
	}
	if !reflect.DeepEqual(got.Names(), []string{"pr-review"}) {
		t.Errorf("Names() = %v", got.Names())
	}
}

func TestRoundTripBranchAndURL(t *testing.T) {
	dir := t.TempDir()
	m := New()
	m.Skills["ssh-skill"] = Skill{
		Source: "github.com/org/private",
		Path:   "skills/x",
		Branch: "dev",
		Commit: "abc",
		URL:    "git@github.com:org/private.git",
	}
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := got.Skills["ssh-skill"]
	if s.Branch != "dev" || s.URL != "git@github.com:org/private.git" {
		t.Errorf("branch/url lost: %+v", s)
	}
}

func TestCloneURL(t *testing.T) {
	cases := []struct {
		skill Skill
		want  string
	}{
		// recorded URL wins (keeps SSH transport)
		{Skill{Source: "github.com/o/r", URL: "git@github.com:o/r.git"}, "git@github.com:o/r.git"},
		// derived from a bare source
		{Skill{Source: "github.com/o/r"}, "https://github.com/o/r.git"},
		// sources with a scheme pass through (legacy file:// entries)
		{Skill{Source: "file:///nas/skills.git"}, "file:///nas/skills.git"},
	}
	for _, c := range cases {
		if got := c.skill.CloneURL(); got != c.want {
			t.Errorf("CloneURL(%+v) = %q, want %q", c.skill, got, c.want)
		}
	}
}

func TestSaveDropsEntriesWithoutProvenance(t *testing.T) {
	dir := t.TempDir()
	m := New()
	m.Skills["own-skill"] = Skill{} // no provenance: carries no information
	m.Skills["vendored"] = Skill{Source: "github.com/x/y", Path: "vendored", Commit: "abc"}
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Skills["own-skill"]; ok {
		t.Error("provenance-less entry should be dropped on save")
	}
	if _, ok := got.Skills["vendored"]; !ok {
		t.Error("vendored entry lost")
	}
	data, _ := os.ReadFile(filepath.Join(dir, FileName))
	if strings.Contains(string(data), "own-skill") {
		t.Errorf("own-skill serialized:\n%s", data)
	}
}

func TestLoadToleratesLegacyAgentsField(t *testing.T) {
	dir := t.TempDir()
	legacy := `[skills.pr-review]
agents = ["claude", "codex"]
source = "github.com/someone/repo"
path = "skills/pr-review"
commit = "abc1234"

[skills.own-skill]
agents = ["claude"]
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(dir)
	if err != nil {
		t.Fatalf("legacy manifest must parse without error: %v", err)
	}
	pr, ok := m.Skills["pr-review"]
	if !ok {
		t.Fatal("pr-review missing")
	}
	if pr.Source != "github.com/someone/repo" || pr.Commit != "abc1234" {
		t.Errorf("provenance lost from legacy manifest: %+v", pr)
	}
}
