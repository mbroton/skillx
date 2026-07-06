package adopt

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fixture lays out a home dir mimicking an `npx skills` install.
func fixture(t *testing.T) (hub, claude, codex string) {
	t.Helper()
	root := t.TempDir()
	hub = filepath.Join(root, ".agents", "skills")
	claude = filepath.Join(root, ".claude", "skills")
	codex = filepath.Join(root, ".codex", "skills")
	for _, d := range []string{hub, claude, codex} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func mkRealSkill(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func relSpoke(t *testing.T, agentDir, hub, name string) {
	t.Helper()
	rel, err := filepath.Rel(agentDir, filepath.Join(hub, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, filepath.Join(agentDir, name)); err != nil {
		t.Fatal(err)
	}
}

func TestDetectNpxLayout(t *testing.T) {
	hub, claude, codex := fixture(t)

	// Hub install spoked into both agents.
	mkRealSkill(t, filepath.Join(hub, "pr-review"))
	relSpoke(t, claude, hub, "pr-review")
	relSpoke(t, codex, hub, "pr-review")
	// Hub install spoked into claude only.
	mkRealSkill(t, filepath.Join(hub, "changelog"))
	relSpoke(t, claude, hub, "changelog")
	// Hub install with no spokes at all.
	mkRealSkill(t, filepath.Join(hub, "orphan"))
	// Copied install directly in codex.
	mkRealSkill(t, filepath.Join(codex, "copied-one"))
	// Distractors that must NOT be detected:
	// - a skillx-style hub symlink,
	if err := os.Symlink("/somewhere/clone/skills/x", filepath.Join(hub, "managed")); err != nil {
		t.Fatal(err)
	}
	// - a real hub dir without SKILL.md,
	if err := os.MkdirAll(filepath.Join(hub, "not-a-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	// - a foreign symlink in an agent dir,
	if err := os.Symlink("/etc", filepath.Join(claude, "etc-link")); err != nil {
		t.Fatal(err)
	}
	// - a plain file in an agent dir.
	if err := os.WriteFile(filepath.Join(claude, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Detect(hub, map[string]string{"claude": claude, "codex": codex})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 candidates, got %d: %+v", len(got), got)
	}

	byName := map[string]Candidate{}
	for _, c := range got {
		byName[c.Name] = c
	}

	pr := byName["pr-review"]
	if !pr.FromHub || !reflect.DeepEqual(pr.Agents, []string{"claude", "codex"}) {
		t.Errorf("pr-review: %+v, want hub install with agents [claude codex]", pr)
	}
	ch := byName["changelog"]
	if !ch.FromHub || !reflect.DeepEqual(ch.Agents, []string{"claude"}) {
		t.Errorf("changelog: %+v, want hub install with agents [claude]", ch)
	}
	or := byName["orphan"]
	if !or.FromHub || len(or.Agents) != 0 {
		t.Errorf("orphan: %+v, want hub install with no derived agents", or)
	}
	cp := byName["copied-one"]
	if cp.FromHub || !reflect.DeepEqual(cp.Agents, []string{"codex"}) {
		t.Errorf("copied-one: %+v, want copied install with agents [codex]", cp)
	}
	if cp.Dir != filepath.Join(codex, "copied-one") {
		t.Errorf("copied-one dir = %s", cp.Dir)
	}
}

func TestDetectCrossAgentSpokeChain(t *testing.T) {
	hub, claude, codex := fixture(t)
	mkRealSkill(t, filepath.Join(hub, "hono-cli"))
	relSpoke(t, claude, hub, "hono-cli")
	// codex spoke chains through the claude spoke (seen in the wild).
	if err := os.Symlink(filepath.Join(claude, "hono-cli"), filepath.Join(codex, "hono-cli")); err != nil {
		t.Fatal(err)
	}

	got, err := Detect(hub, map[string]string{"claude": claude, "codex": codex})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 candidate, got %+v", got)
	}
	if !reflect.DeepEqual(got[0].Agents, []string{"claude", "codex"}) {
		t.Errorf("agents = %v, want both (chain resolves to the same hub entry)", got[0].Agents)
	}
}

func TestDetectCopiedInMultipleAgents(t *testing.T) {
	hub, claude, codex := fixture(t)
	mkRealSkill(t, filepath.Join(claude, "dup"))
	mkRealSkill(t, filepath.Join(codex, "dup"))

	got, err := Detect(hub, map[string]string{"claude": claude, "codex": codex})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 candidate, got %+v", got)
	}
	c := got[0]
	if !reflect.DeepEqual(c.Agents, []string{"claude", "codex"}) {
		t.Errorf("agents = %v", c.Agents)
	}
	if c.Dir != filepath.Join(claude, "dup") {
		t.Errorf("dir = %s, want the first agent's copy", c.Dir)
	}
	if len(c.ExtraCopies) != 1 || c.ExtraCopies[0] != filepath.Join(codex, "dup") {
		t.Errorf("extra copies = %v", c.ExtraCopies)
	}
}

func TestDetectHubWinsNameCollision(t *testing.T) {
	hub, claude, codex := fixture(t)
	mkRealSkill(t, filepath.Join(hub, "same"))
	mkRealSkill(t, filepath.Join(claude, "same"))

	got, err := Detect(hub, map[string]string{"claude": claude, "codex": codex})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].FromHub {
		t.Fatalf("hub install should win the collision, got %+v", got)
	}
	if got[0].Dir != filepath.Join(hub, "same") {
		t.Errorf("dir = %s", got[0].Dir)
	}
}

func TestDetectEmptyAndMissingDirs(t *testing.T) {
	root := t.TempDir()
	got, err := Detect(filepath.Join(root, "nope"), map[string]string{
		"claude": filepath.Join(root, "also-nope"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want none, got %+v", got)
	}
}

func TestIsInstallLink(t *testing.T) {
	hub, claude, codex := fixture(t)
	mkRealSkill(t, filepath.Join(hub, "pr-review"))
	relSpoke(t, claude, hub, "pr-review")
	// Chained spoke: codex -> claude spoke -> hub entry.
	if err := os.Symlink(filepath.Join(claude, "pr-review"), filepath.Join(codex, "pr-review")); err != nil {
		t.Fatal(err)
	}
	// Truly foreign: resolves to a real dir outside hub and candidates.
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(codex, "elsewhere")); err != nil {
		t.Fatal(err)
	}
	// Broken link: resolves nowhere.
	if err := os.Symlink("/nonexistent/x", filepath.Join(codex, "broken")); err != nil {
		t.Fatal(err)
	}

	cands, err := Detect(hub, map[string]string{"claude": claude, "codex": codex})
	if err != nil {
		t.Fatal(err)
	}
	resolved := ResolvedDirs(cands)
	if len(resolved) != 1 {
		t.Fatalf("resolved dirs = %v, want just the hub entry", resolved)
	}

	if !IsInstallLink(filepath.Join(claude, "pr-review"), resolved) {
		t.Error("direct spoke should be an install link")
	}
	if !IsInstallLink(filepath.Join(codex, "pr-review"), resolved) {
		t.Error("chained spoke should be an install link")
	}
	if IsInstallLink(filepath.Join(codex, "elsewhere"), resolved) {
		t.Error("foreign link must not be an install link")
	}
	if IsInstallLink(filepath.Join(codex, "broken"), resolved) {
		t.Error("broken link must not be an install link")
	}
}

func TestResolvedDirsIncludesExtraCopies(t *testing.T) {
	hub, claude, codex := fixture(t)
	mkRealSkill(t, filepath.Join(claude, "dup"))
	mkRealSkill(t, filepath.Join(codex, "dup"))
	cands, err := Detect(hub, map[string]string{"claude": claude, "codex": codex})
	if err != nil {
		t.Fatal(err)
	}
	resolved := ResolvedDirs(cands)
	for _, p := range []string{filepath.Join(claude, "dup"), filepath.Join(codex, "dup")} {
		canon, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		if !resolved[filepath.Clean(canon)] {
			t.Errorf("resolved set missing %s: %v", p, resolved)
		}
	}
}
