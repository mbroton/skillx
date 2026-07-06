package linker

import (
	"os"
	"path/filepath"
	"testing"
)

// fixture builds a fake clone with skills and returns (clone, hub, agentDir).
func fixture(t *testing.T, skills ...string) (clone, hub, claudeDir string) {
	t.Helper()
	root := t.TempDir()
	clone = filepath.Join(root, "repo")
	hub = filepath.Join(root, "agents-hub")
	claudeDir = filepath.Join(root, "claude", "skills")
	for _, s := range skills {
		mkdir(t, filepath.Join(clone, "skills", s))
		write(t, filepath.Join(clone, "skills", s, "SKILL.md"), "# "+s)
	}
	mkdir(t, hub)
	mkdir(t, claudeDir)
	return
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func spec(clone, hub, claudeDir string, skills []string) GlobalSpec {
	return GlobalSpec{
		SkillsDir: filepath.Join(clone, "skills"),
		Hub:       hub,
		AgentDirs: map[string]string{"claude": claudeDir},
		Skills:    skills,
	}
}

func computeFor(t *testing.T, g GlobalSpec, prune bool) *Plan {
	t.Helper()
	p, err := Compute(Input{
		Desired:     g.Desired(),
		ManagedDirs: g.ManagedDirs(),
		Ownership:   g.Ownership(),
		Prune:       prune,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func readlink(t *testing.T, p string) string {
	t.Helper()
	got, err := os.Readlink(p)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCreateFromScratchAndIdempotence(t *testing.T) {
	clone, hub, claude := fixture(t, "pr-review")
	g := spec(clone, hub, claude, []string{"pr-review"})

	plan := computeFor(t, g, false)
	if len(plan.Actions) != 2 {
		t.Fatalf("want 2 creates (hub + spoke), got %v", plan.Actions)
	}
	for _, a := range plan.Actions {
		if a.Op != OpCreate {
			t.Errorf("want create, got %v", a)
		}
	}
	if err := Apply(plan, g.Ownership()); err != nil {
		t.Fatal(err)
	}

	// Hub link -> canonical, spoke -> hub.
	canonical := filepath.Join(clone, "skills", "pr-review")
	if got := readlink(t, filepath.Join(hub, "pr-review")); got != canonical {
		t.Errorf("hub link -> %s, want %s", got, canonical)
	}
	if got := readlink(t, filepath.Join(claude, "pr-review")); got != filepath.Join(hub, "pr-review") {
		t.Errorf("spoke -> %s, want hub entry", got)
	}
	// Chain resolves to real files.
	if _, err := os.Stat(filepath.Join(claude, "pr-review", "SKILL.md")); err != nil {
		t.Errorf("chained symlink does not resolve: %v", err)
	}

	// Second run: nothing to do.
	plan2 := computeFor(t, g, false)
	if len(plan2.Actions) != 0 || plan2.OK != 2 {
		t.Errorf("not idempotent: actions=%v ok=%d", plan2.Actions, plan2.OK)
	}
}

func TestFixWrongOwnedTarget(t *testing.T) {
	clone, hub, claude := fixture(t, "a", "b")
	g := spec(clone, hub, claude, []string{"a"})

	// Owned but wrong: spoke points at hub/b instead of hub/a.
	symlink(t, filepath.Join(hub, "b"), filepath.Join(claude, "a"))
	symlink(t, filepath.Join(clone, "skills", "a"), filepath.Join(hub, "a"))

	plan := computeFor(t, g, false)
	var fixed bool
	for _, a := range plan.Actions {
		if a.Op == OpFix && a.Path == filepath.Join(claude, "a") {
			fixed = true
		}
	}
	if !fixed {
		t.Fatalf("expected a fix action, got %v", plan.Actions)
	}
	if err := Apply(plan, g.Ownership()); err != nil {
		t.Fatal(err)
	}
	if got := readlink(t, filepath.Join(claude, "a")); got != filepath.Join(hub, "a") {
		t.Errorf("after fix, spoke -> %s", got)
	}
}

func TestForeignSymlinkAndRegularDirNeverTouched(t *testing.T) {
	clone, hub, claude := fixture(t, "a", "b")
	g := spec(clone, hub, claude, []string{"a", "b"})

	// Foreign symlink where we want a spoke.
	foreign := t.TempDir()
	symlink(t, foreign, filepath.Join(claude, "a"))
	// Regular directory where we want a spoke.
	mkdir(t, filepath.Join(claude, "b"))
	write(t, filepath.Join(claude, "b", "SKILL.md"), "user's own")
	// Foreign symlink as a stray in the hub.
	symlink(t, foreign, filepath.Join(hub, "stray"))
	// Regular file as a stray in the agent dir.
	write(t, filepath.Join(claude, "notes.txt"), "hi")

	plan := computeFor(t, g, false)
	for _, a := range plan.Actions {
		switch a.Path {
		case filepath.Join(claude, "a"), filepath.Join(claude, "b"),
			filepath.Join(hub, "stray"), filepath.Join(claude, "notes.txt"):
			t.Errorf("planned action on foreign entry: %v", a)
		}
	}
	kinds := map[IssueKind]int{}
	for _, is := range plan.Issues {
		kinds[is.Kind]++
	}
	if kinds[IssueForeignLink] != 2 { // claude/a and hub/stray
		t.Errorf("want 2 foreign-link issues, got %v", plan.Issues)
	}
	if kinds[IssueConflict] != 1 { // claude/b
		t.Errorf("want 1 conflict issue, got %v", plan.Issues)
	}
	if kinds[IssueForeignEntry] != 1 { // claude/notes.txt
		t.Errorf("want 1 foreign-entry issue, got %v", plan.Issues)
	}

	if err := Apply(plan, g.Ownership()); err != nil {
		t.Fatal(err)
	}
	// Untouched.
	if got := readlink(t, filepath.Join(claude, "a")); got != foreign {
		t.Error("foreign symlink was modified")
	}
	if _, err := os.Stat(filepath.Join(claude, "b", "SKILL.md")); err != nil {
		t.Error("regular dir was modified")
	}
	if got := readlink(t, filepath.Join(hub, "stray")); got != foreign {
		t.Error("foreign stray was modified")
	}
}

func TestRemoveOwnedStray(t *testing.T) {
	clone, hub, claude := fixture(t, "old", "keep")
	// Simulate previous sync of "old" + "keep", then "old" leaves the manifest.
	symlink(t, filepath.Join(clone, "skills", "old"), filepath.Join(hub, "old"))
	symlink(t, filepath.Join(hub, "old"), filepath.Join(claude, "old"))
	symlink(t, filepath.Join(clone, "skills", "keep"), filepath.Join(hub, "keep"))
	symlink(t, filepath.Join(hub, "keep"), filepath.Join(claude, "keep"))

	g := spec(clone, hub, claude, []string{"keep"})
	plan := computeFor(t, g, false)
	if len(plan.Actions) != 2 {
		t.Fatalf("want 2 removes, got %v", plan.Actions)
	}
	for _, a := range plan.Actions {
		if a.Op != OpRemove {
			t.Errorf("want remove, got %v", a)
		}
	}
	if err := Apply(plan, g.Ownership()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(claude, "old")); !os.IsNotExist(err) {
		t.Error("stray spoke still present")
	}
	if _, err := os.Lstat(filepath.Join(hub, "old")); !os.IsNotExist(err) {
		t.Error("stray hub link still present")
	}
	if _, err := os.Lstat(filepath.Join(claude, "keep")); err != nil {
		t.Error("kept spoke was removed")
	}
	// Canonical files always survive.
	if _, err := os.Stat(filepath.Join(clone, "skills", "old", "SKILL.md")); err != nil {
		t.Error("canonical files were deleted")
	}
}

func TestDanglingKeptByDefaultPrunedWithFlag(t *testing.T) {
	clone, hub, claude := fixture(t) // no skill files at all
	// Manifest still lists "ghost"; links exist from before.
	symlink(t, filepath.Join(clone, "skills", "ghost"), filepath.Join(hub, "ghost"))
	symlink(t, filepath.Join(hub, "ghost"), filepath.Join(claude, "ghost"))
	g := spec(clone, hub, claude, []string{"ghost"})

	plan := computeFor(t, g, false)
	if len(plan.Actions) != 0 {
		t.Fatalf("default run must not touch dangling desired links: %v", plan.Actions)
	}
	dangling := 0
	for _, is := range plan.Issues {
		if is.Kind == IssueDangling {
			dangling++
		}
	}
	if dangling != 2 {
		t.Errorf("want 2 dangling issues, got %v", plan.Issues)
	}

	pruned := computeFor(t, g, true)
	if len(pruned.Actions) != 2 {
		t.Fatalf("prune should remove both links, got %v", pruned.Actions)
	}
	if err := Apply(pruned, g.Ownership()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(hub, "ghost")); !os.IsNotExist(err) {
		t.Error("dangling hub link survived prune")
	}
}

func TestMissingSourceNeverCreated(t *testing.T) {
	clone, hub, claude := fixture(t) // no files
	g := spec(clone, hub, claude, []string{"ghost"})
	plan := computeFor(t, g, false)
	if len(plan.Actions) != 0 {
		t.Fatalf("must not create links to missing files: %v", plan.Actions)
	}
	if len(plan.Issues) == 0 {
		t.Error("expected dangling issues for missing source")
	}
}

func TestAgentDirsDriveSpokes(t *testing.T) {
	clone, hub, claude := fixture(t, "a")
	// No agent dirs in the spec: hub link only, no spokes anywhere.
	g := GlobalSpec{
		SkillsDir: filepath.Join(clone, "skills"),
		Hub:       hub,
		Skills:    []string{"a"},
	}
	plan := computeFor(t, g, false)
	if err := Apply(plan, g.Ownership()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(claude, "a")); !os.IsNotExist(err) {
		t.Error("spoke created although no agent dirs were given")
	}
	if _, err := os.Lstat(filepath.Join(hub, "a")); err != nil {
		t.Error("hub link should exist")
	}
}

func TestApplyRefusesWhenLinkChangedAfterPlan(t *testing.T) {
	clone, hub, claude := fixture(t, "old")
	symlink(t, filepath.Join(clone, "skills", "old"), filepath.Join(hub, "old"))
	g := spec(clone, hub, claude, nil)

	plan := computeFor(t, g, false)
	if len(plan.Actions) != 1 || plan.Actions[0].Op != OpRemove {
		t.Fatalf("want one remove, got %v", plan.Actions)
	}

	// Race: the link is swapped to a foreign target between plan and apply.
	foreign := t.TempDir()
	if err := os.Remove(filepath.Join(hub, "old")); err != nil {
		t.Fatal(err)
	}
	symlink(t, foreign, filepath.Join(hub, "old"))

	if err := Apply(plan, g.Ownership()); err == nil {
		t.Fatal("apply should refuse to remove a link that became foreign")
	}
	if got := readlink(t, filepath.Join(hub, "old")); got != foreign {
		t.Error("foreign link was removed despite safety check")
	}
}

func TestLocalSpecRelativeLinks(t *testing.T) {
	project := t.TempDir()
	hub := filepath.Join(project, ".agents", "skills")
	mkdir(t, filepath.Join(hub, "fmt"))
	write(t, filepath.Join(hub, "fmt", "SKILL.md"), "# fmt")

	l := LocalSpec{
		ProjectRoot: project,
		Agents:      map[string]string{"claude": ".claude/skills", "codex": ".codex/skills"},
	}
	desired, err := l.Desired()
	if err != nil {
		t.Fatal(err)
	}
	if len(desired) != 2 {
		t.Fatalf("want 2 spokes, got %v", desired)
	}
	plan, err := Compute(Input{Desired: desired, ManagedDirs: l.ManagedDirs(), Ownership: l.Ownership()})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(plan, l.Ownership()); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(project, ".claude", "skills", "fmt")
	got := readlink(t, link)
	want := filepath.Join("..", "..", ".agents", "skills", "fmt")
	if got != want {
		t.Errorf("local spoke target = %q, want relative %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(link, "SKILL.md")); err != nil {
		t.Errorf("relative link does not resolve: %v", err)
	}

	// Idempotent.
	plan2, err := Compute(Input{Desired: desired, ManagedDirs: l.ManagedDirs(), Ownership: l.Ownership()})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan2.Actions) != 0 {
		t.Errorf("local sync not idempotent: %v", plan2.Actions)
	}

	// Removing the hub entry makes the spokes owned strays.
	if err := os.RemoveAll(filepath.Join(hub, "fmt")); err != nil {
		t.Fatal(err)
	}
	desired3, err := l.Desired()
	if err != nil {
		t.Fatal(err)
	}
	plan3, err := Compute(Input{Desired: desired3, ManagedDirs: l.ManagedDirs(), Ownership: l.Ownership()})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan3.Actions) != 2 {
		t.Fatalf("want 2 removes after hub entry deleted, got %v", plan3.Actions)
	}
}

func TestOwnershipRelativeTargets(t *testing.T) {
	project := t.TempDir()
	own := NewPointerOwnership(filepath.Join(project, ".agents", "skills"))
	link := filepath.Join(project, ".claude", "skills", "x")
	if !own.OwnsTarget(link, "../../.agents/skills/x") {
		t.Error("relative target into hub should be owned")
	}
	if own.OwnsTarget(link, "../../elsewhere/x") {
		t.Error("relative target outside hub must not be owned")
	}
	if own.OwnsTarget(link, "/somewhere/else") {
		t.Error("absolute foreign target must not be owned")
	}
	// Prefix trickery: /project/.agents/skills-evil is not inside the hub.
	if own.OwnsTarget(link, filepath.Join(project, ".agents", "skills-evil", "x")) {
		t.Error("sibling dir with hub prefix must not be owned")
	}
}

func TestPrefixTrickeryResolutionMode(t *testing.T) {
	root := t.TempDir()
	skillsDir := filepath.Join(root, "repo", "skills")
	mkdir(t, skillsDir)
	// A real dir whose path merely shares the string prefix of skillsDir.
	evil := filepath.Join(root, "repo", "skills-evil", "x")
	mkdir(t, evil)
	link := filepath.Join(root, "l")
	symlink(t, evil, link)
	own := NewOwnership(skillsDir)
	if got := own.Classify(link); got != ClassForeign {
		t.Errorf("Classify = %v, want ClassForeign for prefix-sibling target", got)
	}
}

// TestNpxLayoutNeverClaimed reproduces the migration scenario that motivated
// resolution-based ownership: skills previously installed by `npx skills`
// live as REAL directories in the hub, with relative spoke symlinks in the
// agent dirs. skillx's manifest is empty. sync must plan ZERO removals (or
// any other action) and report the spokes as foreign.
func TestNpxLayoutNeverClaimed(t *testing.T) {
	clone, hub, claude := fixture(t) // empty clone/skills, hub, claude dir
	codex := filepath.Join(filepath.Dir(filepath.Dir(claude)), "codex", "skills")
	mkdir(t, codex)

	// npx-managed: real skill dirs in the hub.
	for _, name := range []string{"hono-cli", "pr-review", "changelog"} {
		mkdir(t, filepath.Join(hub, name))
		write(t, filepath.Join(hub, name, "SKILL.md"), "# "+name)
	}
	// Relative spokes, npx style, across two agent dirs.
	relToHub := func(agentDir, name string) string {
		rel, err := filepath.Rel(agentDir, filepath.Join(hub, name))
		if err != nil {
			t.Fatal(err)
		}
		return rel
	}
	symlink(t, relToHub(claude, "hono-cli"), filepath.Join(claude, "hono-cli"))
	symlink(t, relToHub(claude, "pr-review"), filepath.Join(claude, "pr-review"))
	symlink(t, relToHub(claude, "changelog"), filepath.Join(claude, "changelog"))
	symlink(t, relToHub(codex, "pr-review"), filepath.Join(codex, "pr-review"))
	// A cross-agent link (the user's real hono-cli case).
	symlink(t, filepath.Join(claude, "hono-cli"), filepath.Join(codex, "hono-cli"))

	g := GlobalSpec{
		SkillsDir: filepath.Join(clone, "skills"),
		Hub:       hub,
		AgentDirs: map[string]string{"claude": claude, "codex": codex},
		Skills:    nil, // empty bag
	}
	plan, err := Compute(Input{
		Desired:     g.Desired(),
		ManagedDirs: g.ManagedDirs(),
		Ownership:   g.Ownership(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("npx layout must produce ZERO planned actions, got:\n%v", plan.Actions)
	}
	kinds := map[IssueKind]int{}
	for _, is := range plan.Issues {
		kinds[is.Kind]++
	}
	if kinds[IssueForeignLink] != 5 { // 3 claude + 2 codex spokes
		t.Errorf("want 5 foreign-link issues, got %v", plan.Issues)
	}
	if kinds[IssueForeignEntry] != 3 { // 3 real dirs in the hub
		t.Errorf("want 3 foreign-entry issues (hub dirs), got %v", plan.Issues)
	}
	// Prune must not change that: these are not skillx's.
	pruned, err := Compute(Input{
		Desired:     g.Desired(),
		ManagedDirs: g.ManagedDirs(),
		Ownership:   g.Ownership(),
		Prune:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned.Actions) != 0 {
		t.Fatalf("prune must not touch npx entries either, got %v", pruned.Actions)
	}
}

func TestSkillxSpokeStillOwned(t *testing.T) {
	// The counterpart: a spoke -> hub entry that IS a symlink into the clone
	// resolves into the clone and stays owned (removable when unlisted).
	clone, hub, claude := fixture(t, "mine")
	symlink(t, filepath.Join(clone, "skills", "mine"), filepath.Join(hub, "mine"))
	symlink(t, filepath.Join(hub, "mine"), filepath.Join(claude, "mine"))

	g := spec(clone, hub, claude, nil) // no longer in the bag
	if got := g.Ownership().Classify(filepath.Join(claude, "mine")); got != ClassOwned {
		t.Fatalf("skillx spoke Classify = %v, want ClassOwned", got)
	}
	plan := computeFor(t, g, false)
	if len(plan.Actions) != 2 {
		t.Fatalf("want hub+spoke removals, got %v", plan.Actions)
	}
}

func TestDanglingClassification(t *testing.T) {
	clone, hub, claude := fixture(t)
	skillsDir := filepath.Join(clone, "skills")

	// 1. Chain heads into the clone but breaks -> owned-dangling.
	symlink(t, filepath.Join(skillsDir, "gone"), filepath.Join(hub, "gone"))
	// 2. Basename matches a manifest skill, target elsewhere & missing -> owned-dangling.
	symlink(t, filepath.Join(hub, "listed"), filepath.Join(claude, "listed"))
	// 3. Broken link, unknown name, foreign target -> unknown-dangling.
	symlink(t, "/nonexistent/elsewhere", filepath.Join(claude, "mystery"))

	own := NewOwnership(skillsDir).WithManifestNames([]string{"listed"})
	if got := own.Classify(filepath.Join(hub, "gone")); got != ClassOwnedDangling {
		t.Errorf("clone-headed broken link: Classify = %v, want ClassOwnedDangling", got)
	}
	if got := own.Classify(filepath.Join(claude, "listed")); got != ClassOwnedDangling {
		t.Errorf("manifest-named broken link: Classify = %v, want ClassOwnedDangling", got)
	}
	if got := own.Classify(filepath.Join(claude, "mystery")); got != ClassUnknownDangling {
		t.Errorf("unknown broken link: Classify = %v, want ClassUnknownDangling", got)
	}

	// Plan behavior: strays never removed on plain sync...
	g := GlobalSpec{
		SkillsDir: skillsDir,
		Hub:       hub,
		AgentDirs: map[string]string{"claude": claude},
		Skills:    nil,
	}
	in := Input{Desired: g.Desired(), ManagedDirs: g.ManagedDirs(),
		Ownership: g.Ownership().WithManifestNames([]string{"listed"})}
	plan, err := Compute(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("plain sync must not remove any dangling link, got %v", plan.Actions)
	}

	// ...prune removes only the attributable ones; unknown stays.
	in.Prune = true
	pruned, err := Compute(in)
	if err != nil {
		t.Fatal(err)
	}
	removed := map[string]bool{}
	for _, a := range pruned.Actions {
		if a.Op != OpRemove {
			t.Errorf("unexpected op %v", a)
		}
		removed[a.Path] = true
	}
	if !removed[filepath.Join(hub, "gone")] || !removed[filepath.Join(claude, "listed")] {
		t.Errorf("prune should remove owned-dangling links, got %v", pruned.Actions)
	}
	if removed[filepath.Join(claude, "mystery")] {
		t.Error("prune must NEVER remove unknown-dangling links")
	}
	if err := Apply(pruned, in.Ownership); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(claude, "mystery")); err != nil {
		t.Error("unknown-dangling link disappeared")
	}
}

func TestRemovalsFor(t *testing.T) {
	clone, hub, claude := fixture(t, "mine")
	symlink(t, filepath.Join(clone, "skills", "mine"), filepath.Join(hub, "mine"))
	symlink(t, filepath.Join(hub, "mine"), filepath.Join(claude, "mine"))
	// Foreign link with the same name in another spot.
	foreign := t.TempDir()
	symlink(t, foreign, filepath.Join(claude, "other"))
	// Real dir where a link is expected.
	mkdir(t, filepath.Join(claude, "copied"))

	own := NewOwnership(filepath.Join(clone, "skills"))
	plan := RemovalsFor([]string{
		filepath.Join(hub, "mine"),
		filepath.Join(claude, "mine"),
		filepath.Join(claude, "other"),   // foreign: must not be removed
		filepath.Join(claude, "copied"),  // real dir: must not be removed
		filepath.Join(claude, "missing"), // absent: skipped silently
	}, own, "test removal")

	if len(plan.Actions) != 2 {
		t.Fatalf("want 2 removals, got %v", plan.Actions)
	}
	if err := Apply(plan, own); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(hub, "mine")); !os.IsNotExist(err) {
		t.Error("owned hub link survived")
	}
	if _, err := os.Lstat(filepath.Join(claude, "other")); err != nil {
		t.Error("foreign link was removed")
	}
	if _, err := os.Stat(filepath.Join(claude, "copied")); err != nil {
		t.Error("real dir was removed")
	}
	kinds := map[IssueKind]int{}
	for _, is := range plan.Issues {
		kinds[is.Kind]++
	}
	if kinds[IssueForeignLink] != 1 || kinds[IssueForeignEntry] != 1 {
		t.Errorf("issues = %v", plan.Issues)
	}
}

func TestPruneDangling(t *testing.T) {
	clone, hub, claude := fixture(t, "alive")
	skillsDir := filepath.Join(clone, "skills")
	// Healthy install.
	symlink(t, filepath.Join(skillsDir, "alive"), filepath.Join(hub, "alive"))
	symlink(t, filepath.Join(hub, "alive"), filepath.Join(claude, "alive"))
	// Dangling install (skill deleted from the bag on another machine).
	symlink(t, filepath.Join(skillsDir, "gone"), filepath.Join(hub, "gone"))
	symlink(t, filepath.Join(hub, "gone"), filepath.Join(claude, "gone"))
	// Unattributable broken link: never pruned.
	symlink(t, "/nonexistent/thing", filepath.Join(claude, "mystery"))
	// Foreign link: never pruned.
	foreign := t.TempDir()
	symlink(t, foreign, filepath.Join(claude, "foreign"))

	own := NewOwnership(skillsDir)
	plan, err := PruneDangling([]string{hub, claude}, own)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 2 {
		t.Fatalf("want 2 prunes (hub/gone + claude/gone), got %v", plan.Actions)
	}
	if err := Apply(plan, own); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(hub, "gone"), filepath.Join(claude, "gone")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived prune", p)
		}
	}
	for _, p := range []string{
		filepath.Join(hub, "alive"), filepath.Join(claude, "alive"),
		filepath.Join(claude, "mystery"), filepath.Join(claude, "foreign"),
	} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s was wrongly pruned", p)
		}
	}
}
