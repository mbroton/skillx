package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mbroton/skillx/internal/config"
	"github.com/mbroton/skillx/internal/manifest"
)

// cleanupFixture builds a machine layout mixing managed, unmanaged (npx),
// foreign, and broken entries, and returns cfg + manifest for it.
func cleanupFixture(t *testing.T) (*config.Config, *manifest.Manifest, string, string, string) {
	t.Helper()
	root := t.TempDir()
	clone := filepath.Join(root, "clone")
	hub := filepath.Join(root, ".agents", "skills")
	claude := filepath.Join(root, ".claude", "skills")
	for _, d := range []string{filepath.Join(clone, "skills", "managed-skill"), hub, claude} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, path string) {
		t.Helper()
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(clone, "skills", "managed-skill", "SKILL.md"), "# managed")

	// Managed: hub link into the clone + spoke.
	link(filepath.Join(clone, "skills", "managed-skill"), filepath.Join(hub, "managed-skill"))
	link(filepath.Join(hub, "managed-skill"), filepath.Join(claude, "managed-skill"))
	// Unmanaged npx-style install: real dir in hub + relative spoke.
	if err := os.MkdirAll(filepath.Join(hub, "npx-old"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(hub, "npx-old", "SKILL.md"), "# npx")
	link("../../.agents/skills/npx-old", filepath.Join(claude, "npx-old"))
	// Non-skill dir in the hub: never a cleanup candidate.
	if err := os.MkdirAll(filepath.Join(hub, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Broken links: unknown origin + skillx's own (heads into the clone).
	link("/nonexistent/ghost", filepath.Join(hub, "ghost"))
	link(filepath.Join(clone, "skills", "gone"), filepath.Join(hub, "stale"))
	// Foreign link resolving elsewhere: never a cleanup candidate.
	elsewhere := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	link(elsewhere, filepath.Join(claude, "foreign"))
	// Plain file: never a cleanup candidate.
	write(filepath.Join(claude, "README.txt"), "hi")

	cfg := &config.Config{
		Repo:      "example.invalid/skills",
		ClonePath: clone,
		Hub:       hub,
		Agents:    map[string]config.Agent{"claude": {Path: claude}},
	}
	m := manifest.New()
	return cfg, m, clone, hub, claude
}

func TestCleanupCandidates(t *testing.T) {
	cfg, _, _, hub, claude := cleanupFixture(t)
	report, err := buildLayoutReport(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	items := report.cleanupCandidates()

	got := map[string]bool{}
	for _, e := range items {
		got[e.path] = true
	}
	want := []string{
		filepath.Join(hub, "npx-old"),    // unmanaged real skill dir
		filepath.Join(hub, "ghost"),      // broken, unknown origin
		filepath.Join(hub, "stale"),      // broken skillx link
		filepath.Join(claude, "npx-old"), // unmanaged install spoke
	}
	if len(items) != len(want) {
		t.Fatalf("got %d candidates %v, want %d", len(items), got, len(want))
	}
	for _, p := range want {
		if !got[p] {
			t.Errorf("missing cleanup candidate %s (got %v)", p, got)
		}
	}
	// Managed and foreign entries must never be offered.
	for _, p := range []string{
		filepath.Join(hub, "managed-skill"),
		filepath.Join(claude, "managed-skill"),
		filepath.Join(claude, "foreign"),
		filepath.Join(hub, "notes"),
		filepath.Join(claude, "README.txt"),
	} {
		if got[p] {
			t.Errorf("%s must not be a cleanup candidate", p)
		}
	}
}

func TestRemoveUnmanagedItemGuardrails(t *testing.T) {
	cfg, _, clone, hub, claude := cleanupFixture(t)
	own, err := bagOwnership(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Refuses a managed link even if handed one.
	managed := layoutEntry{path: filepath.Join(claude, "managed-skill"), isLink: true}
	if err := removeUnmanagedItem(managed, own, clone); err == nil {
		t.Error("must refuse to remove a managed link")
	}
	if _, err := os.Lstat(managed.path); err != nil {
		t.Error("managed link was deleted")
	}

	// Refuses a real dir inside the clone.
	inClone := layoutEntry{path: filepath.Join(clone, "skills", "managed-skill"), isDir: true}
	if err := removeUnmanagedItem(inClone, own, clone); err == nil {
		t.Error("must refuse to remove dirs inside the clone")
	}
	if _, err := os.Stat(inClone.path); err != nil {
		t.Error("clone contents were deleted")
	}

	// Refuses plain files.
	file := layoutEntry{path: filepath.Join(claude, "README.txt")}
	if err := removeUnmanagedItem(file, own, clone); err == nil {
		t.Error("must refuse to remove plain files")
	}

	// Removes an unmanaged real skill dir and an unmanaged spoke.
	for _, e := range []layoutEntry{
		{path: filepath.Join(claude, "npx-old"), isLink: true},
		{path: filepath.Join(hub, "npx-old"), isDir: true},
		{path: filepath.Join(hub, "ghost"), isLink: true, broken: true},
	} {
		if err := removeUnmanagedItem(e, own, clone); err != nil {
			t.Errorf("remove %s: %v", e.path, err)
		}
		if _, err := os.Lstat(e.path); !os.IsNotExist(err) {
			t.Errorf("%s still exists", e.path)
		}
	}

	// Idempotent: removing an already-gone item is not an error.
	if err := removeUnmanagedItem(layoutEntry{path: filepath.Join(hub, "ghost")}, own, clone); err != nil {
		t.Errorf("second removal should be a no-op, got %v", err)
	}
}

func TestBagInventoryAndInstalledSet(t *testing.T) {
	cfg, _, clone, hub, _ := cleanupFixture(t)
	// Grow the bag: a second skill, not installed.
	if err := os.MkdirAll(filepath.Join(clone, "skills", "uninstalled-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Noise in skills/: files and dotdirs are not inventory.
	if err := os.WriteFile(filepath.Join(clone, "skills", ".gitkeep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(clone, "skills", ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}

	inv, err := bagInventory(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"managed-skill", "uninstalled-skill"}
	if len(inv) != 2 || inv[0] != want[0] || inv[1] != want[1] {
		t.Fatalf("inventory = %v, want %v", inv, want)
	}

	installed, err := installedSet(cfg, inv)
	if err != nil {
		t.Fatal(err)
	}
	if !installed["managed-skill"] {
		t.Error("managed-skill has an owned hub link; must count as installed")
	}
	if installed["uninstalled-skill"] {
		t.Error("uninstalled-skill has no hub link; must not count as installed")
	}
	// The npx-style real dir in the hub must NOT make a same-named bag
	// skill count as installed (the hub entry is not an owned link).
	if err := os.MkdirAll(filepath.Join(clone, "skills", "npx-old"), 0o755); err != nil {
		t.Fatal(err)
	}
	inv2, err := bagInventory(cfg)
	if err != nil {
		t.Fatal(err)
	}
	installed2, err := installedSet(cfg, inv2)
	if err != nil {
		t.Fatal(err)
	}
	if installed2["npx-old"] {
		t.Errorf("npx real dir at %s must not count as installed", filepath.Join(hub, "npx-old"))
	}
}
