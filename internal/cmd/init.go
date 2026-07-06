package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mbroton/skillx/internal/config"
	"github.com/mbroton/skillx/internal/fsutil"
	"github.com/mbroton/skillx/internal/gitx"
	"github.com/mbroton/skillx/internal/linker"
	"github.com/mbroton/skillx/internal/manifest"
	"github.com/mbroton/skillx/internal/ui"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up skillx on this machine (clone the skills repo, write config)",
	Long: `Sets skillx up in stages:

  1. Repository       validate the skills repo URL (git ls-remote) before
                      touching anything on disk
  2. Clone            clone it (or reuse a matching existing clone) and
                      scaffold skillx.toml + skills/ when missing
  3. Agents           detect agent dirs; pick which to enable
  4. Config           write the machine config
  5. Existing skills  offer to adopt skills installed by other tools
  6. Cleanup          offer to remove leftover unmanaged items (old npx
                      skills, stray/broken links) — defaults to NO; nothing
                      is removed unless you opt in (or pass --clean)
  7. Use skills       pick which bag skills to install on this machine

Idempotent: re-running keeps existing settings unless overridden by flags.
When stdin is not a terminal, nothing is ever removed unless --clean is
passed explicitly (--yes does not imply cleanup).`,
	Args: cobra.NoArgs,
	RunE: runInit,
}

var (
	initRepo      string
	initClonePath string
	initHub       string
	initYes       bool
	initClean     bool
)

func init() {
	initCmd.Flags().StringVar(&initRepo, "repo", "", "git URL of the skills repository")
	initCmd.Flags().StringVar(&initClonePath, "clone-path", "", "where to clone (or find) the skills repo")
	initCmd.Flags().StringVar(&initHub, "hub", "", "hub directory (default ~/.agents/skills)")
	initCmd.Flags().BoolVarP(&initYes, "yes", "y", false, "accept defaults; never prompt (does NOT imply --clean)")
	initCmd.Flags().BoolVar(&initClean, "clean", false, "remove ALL unmanaged items (old npx skills, stray/broken links) without prompting")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	err := runInitStages()
	if err != nil && ui.IsAbort(err) {
		fmt.Println()
		fmt.Println("Aborted. Everything already set up was kept; re-run `skillx init` to continue.")
		return nil
	}
	return err
}

func runInitStages() error {
	interactive := ui.IsInteractive() && !initYes

	// Start from the existing config when present (idempotence).
	cfg, err := config.Load()
	if errors.Is(err, config.ErrNotFound) {
		cfg = &config.Config{Hub: config.DefaultHub, Agents: config.DefaultAgents()}
	} else if err != nil {
		return err
	}
	if initClonePath != "" {
		cfg.ClonePath = absolutize(initClonePath)
	}
	if cfg.ClonePath == "" {
		cfg.ClonePath = fsutil.ContractPath(config.DefaultClonePath())
	}
	if initHub != "" {
		cfg.Hub = absolutize(initHub)
	}
	clonePath := cfg.ExpandedClonePath()
	cloneExists := gitx.IsRepo(clonePath)

	// ── 1. Repository ── validated FIRST; nothing on disk changes before
	// the repo is known to work.
	stage("Repository")
	if initRepo != "" {
		cfg.Repo = initRepo
	}
	if cfg.Repo == "" && cloneExists {
		if url, err := gitx.Run(clonePath, "remote", "get-url", "origin"); err == nil {
			cfg.Repo = url
			fmt.Printf("Using repo URL from existing clone: %s\n", url)
		}
	}
	if cfg.Repo == "" {
		if !interactive {
			return ui.NonInteractiveErr("the skills repo URL", "--repo <url>")
		}
		url, err := ui.Input("Git URL of your skills repository", "git@github.com:you/skills.git")
		if err != nil {
			return err
		}
		if strings.TrimSpace(url) == "" {
			return fmt.Errorf("a skills repo URL is required (or pass --repo)")
		}
		cfg.Repo = strings.TrimSpace(url)
	}
	fmt.Printf("Checking %s ...\n", cfg.Repo)
	refs, err := gitx.Run("", "ls-remote", "--", cfg.Repo)
	if err != nil {
		return fmt.Errorf("repository %s is not reachable:\n  %v\nNothing was changed on disk", cfg.Repo, err)
	}
	if strings.TrimSpace(refs) == "" {
		fmt.Printf("Repository OK: %s (empty — will scaffold)\n", cfg.Repo)
	} else {
		fmt.Printf("Repository OK: %s\n", cfg.Repo)
	}

	// ── 2. Clone ──
	stage("Clone")
	switch {
	case cloneExists:
		origin, _ := gitx.Run(clonePath, "remote", "get-url", "origin")
		if !sameRepoURL(origin, cfg.Repo) {
			return fmt.Errorf("existing clone at %s has origin %s, but the repo is %s;\nmove it aside or pass a different --clone-path",
				fsutil.ContractPath(clonePath), origin, cfg.Repo)
		}
		fmt.Printf("Reusing existing clone at %s\n", fsutil.ContractPath(clonePath))
		if err := gitx.Fetch(clonePath); err != nil {
			fmt.Printf("warning: fetch failed (%v)\n", err)
		} else if _, behind, err := gitx.AheadBehind(clonePath); err == nil && behind > 0 {
			fmt.Printf("note: clone is %d commit(s) behind origin — run `skillx update` afterwards\n", behind)
		}
	case pathExistsNonEmpty(clonePath):
		return fmt.Errorf("%s exists but is not a git repository; move it or pass --clone-path", clonePath)
	default:
		if err := os.MkdirAll(filepath.Dir(clonePath), 0o755); err != nil {
			return err
		}
		fmt.Printf("Cloning into %s ...\n", fsutil.ContractPath(clonePath))
		if err := gitx.Clone(cfg.Repo, clonePath, false, ""); err != nil {
			return err
		}
	}
	if err := scaffoldClone(clonePath); err != nil {
		return err
	}
	inv, err := bagInventory(cfg)
	if err != nil {
		return err
	}
	fmt.Printf("The bag holds %s.\n", plural(len(inv), "skill"))

	// ── 3. Agents ──
	stage("Agents")
	if cfg.Agents == nil {
		cfg.Agents = map[string]config.Agent{}
	}
	for name, a := range config.DefaultAgents() {
		if _, ok := cfg.Agents[name]; !ok {
			cfg.Agents[name] = a
		}
	}
	detected := detectAgents(cfg)
	for _, name := range agentNamesSorted(cfg) {
		a := cfg.Agents[name]
		state := "not found"
		if detected[name] {
			state = "detected"
		}
		if a.Disabled {
			state += ", disabled in config"
		}
		fmt.Printf("  %-8s %s (%s)\n", name, a.Path, state)
	}
	if interactive {
		opts := make([]ui.Option, 0, len(cfg.Agents))
		for _, name := range agentNamesSorted(cfg) {
			a := cfg.Agents[name]
			opts = append(opts, ui.Option{Label: name, Value: name,
				Selected: !a.Disabled && (detected[name] || len(detected) == 0)})
		}
		chosen, err := ui.MultiSelect("Enable which agents on this machine?", opts)
		if err != nil {
			return err
		}
		enabled := map[string]bool{}
		for _, c := range chosen {
			enabled[c] = true
		}
		for name, a := range cfg.Agents {
			a.Disabled = !enabled[name]
			cfg.Agents[name] = a
		}
	}
	fmt.Printf("Enabled agents: %s\n", strings.Join(cfg.AgentNames(), ", "))

	// ── 4. Config ──
	stage("Config")
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Wrote %s\n", config.Path())

	// ── 5. Existing skills ──
	stage("Existing skills")
	adopted := 0
	candidates, err := detectAdoptable(cfg)
	if err != nil {
		return err
	}
	switch {
	case len(candidates) == 0:
		fmt.Println("None found.")
	case interactive:
		ok, err := ui.Confirm(fmt.Sprintf(
			"Found %s not managed by skillx. Adopt into your bag?",
			plural(len(candidates), "existing skill")), true)
		if err != nil {
			return err
		}
		if ok {
			if adopted, err = adoptFlow(cfg, adoptOptions{}); err != nil {
				return err
			}
		} else {
			fmt.Println("Skipped. Run `skillx adopt` any time.")
		}
	default:
		fmt.Printf("Found %s not managed by skillx; run `skillx adopt` to migrate them.\n",
			plural(len(candidates), "existing skill"))
	}

	// ── 6. Cleanup ── LAST and opt-in; nothing is deleted by default.
	stage("Cleanup")
	removed, err := runInitCleanup(cfg, interactive)
	if err != nil {
		return err
	}

	// ── 7. Use skills ──
	stage("Use skills")
	inv, err = bagInventory(cfg) // adoption may have grown the bag
	if err != nil {
		return err
	}
	switch {
	case len(inv) == 0:
		fmt.Println("The bag is empty — collect your first skill with `skillx add <owner/repo>`.")
	case interactive:
		installed, err := installedSet(cfg, inv)
		if err != nil {
			return err
		}
		opts := make([]ui.Option, len(inv))
		for i, name := range inv {
			// Fresh machine (nothing installed yet): preselect everything —
			// installing is reversible. Otherwise preselect what's installed.
			opts[i] = ui.Option{Label: name, Value: name,
				Selected: installed[name] || len(installed) == 0}
		}
		chosen, err := ui.MultiSelect("Use which skills on this machine? (checked = installed)", opts)
		if err != nil {
			return err
		}
		chosenSet := map[string]bool{}
		for _, c := range chosen {
			chosenSet[c] = true
		}
		var toDrop []string
		for _, name := range inv {
			if installed[name] && !chosenSet[name] {
				toDrop = append(toDrop, name)
			}
		}
		if _, err := installSkills(cfg, chosen, nil); err != nil {
			return err
		}
		if _, err := unlinkSkills(cfg, toDrop, "unchecked", nil); err != nil {
			return err
		}
	default:
		fmt.Printf("The bag holds %s. Run `skillx use --all` to install everything, or `skillx use` to pick.\n",
			plural(len(inv), "skill"))
	}

	// ── Summary ──
	stage("Summary")
	finalReport, err := buildLayoutReport(cfg, nil, nil)
	if err != nil {
		return err
	}
	fmt.Printf("  repo:     %s\n", cfg.Repo)
	fmt.Printf("  clone:    %s\n", fsutil.ContractPath(clonePath))
	fmt.Printf("  agents:   %s\n", strings.Join(cfg.AgentNames(), ", "))
	fmt.Printf("  adopted:  %s\n", plural(adopted, "skill"))
	fmt.Printf("  removed:  %s\n", plural(removed, "unmanaged item"))
	fmt.Printf("  links:    %s in place\n", plural(finalReport.counts[kindManaged], "managed link"))
	fmt.Println()
	fmt.Println("Next steps: `skillx add <owner/repo>` to collect a skill,")
	fmt.Println("            `skillx use` to pick what's installed, `skillx status` to inspect.")
	return nil
}

// scaffoldClone creates skills/ and skillx.toml in the clone when missing
// and commits the scaffold.
func scaffoldClone(clonePath string) error {
	scaffolded := false
	skillsDir := filepath.Join(clonePath, "skills")
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		if err := os.MkdirAll(skillsDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(skillsDir, ".gitkeep"), nil, 0o644); err != nil {
			return err
		}
		scaffolded = true
	}
	if _, err := os.Stat(manifest.PathIn(clonePath)); os.IsNotExist(err) {
		if err := manifest.New().Save(clonePath); err != nil {
			return err
		}
		scaffolded = true
	}
	if scaffolded {
		if committed, err := gitx.AddAndCommit(clonePath, "skillx: scaffold manifest and skills dir",
			manifest.FileName, "skills"); err != nil {
			fmt.Printf("warning: could not commit scaffold: %v\n", err)
		} else if committed {
			fmt.Println("Scaffolded skillx.toml and skills/ (committed; push when ready).")
		}
	}
	return nil
}

// runInitCleanup offers to remove unmanaged leftovers (old npx skill dirs,
// stray install links, broken links). Defaults are maximally conservative:
// the interactive confirm defaults to No (Enter keeps everything),
// non-interactive runs remove nothing without an explicit --clean, and
// every deletion re-verifies the item is still unmanaged. Returns the
// number of items removed.
func runInitCleanup(cfg *config.Config, interactive bool) (int, error) {
	report, err := buildLayoutReport(cfg, nil, nil)
	if err != nil {
		return 0, err
	}
	items := report.cleanupCandidates()
	if len(items) == 0 {
		fmt.Println("Nothing to clean up.")
		return 0, nil
	}
	fmt.Printf("%s left over (old npx skills, stray or broken links).\n",
		plural(len(items), "unmanaged item"))

	selected := items
	switch {
	case initClean:
		fmt.Println("--clean: removing all of them.")
	case !interactive:
		fmt.Println("Keeping everything (no prompt without a terminal; --yes does not imply cleanup).")
		fmt.Println("Run `skillx init --clean` to remove all unmanaged items, or `skillx adopt` to migrate them.")
		return 0, nil
	default:
		ok, err := ui.Confirm(fmt.Sprintf("Remove %s now?", plural(len(items), "unmanaged item")), false)
		if err != nil {
			return 0, err
		}
		if !ok {
			fmt.Println("Kept everything. Run `skillx init --clean` later if you change your mind.")
			return 0, nil
		}
		opts := make([]ui.Option, len(items))
		for i, e := range items {
			opts[i] = ui.Option{Label: cleanupLabel(e), Value: e.path, Selected: true}
		}
		chosen, err := ui.MultiSelect("Remove which items? (uncheck anything you want to keep)", opts)
		if err != nil {
			return 0, err
		}
		pick := map[string]bool{}
		for _, p := range chosen {
			pick[p] = true
		}
		selected = nil
		for _, e := range items {
			if pick[e.path] {
				selected = append(selected, e)
			}
		}
		if len(selected) == 0 {
			fmt.Println("Nothing selected; kept everything.")
			return 0, nil
		}
	}

	own, err := bagOwnership(cfg)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range selected {
		// Full paths, printed BEFORE deletion; real dirs marked explicitly.
		if e.isDir {
			fmt.Printf("removing dir    %s (real files)\n", e.path)
		} else {
			fmt.Printf("removing link   %s\n", e.path)
		}
		if err := removeUnmanagedItem(e, own, cfg.ExpandedClonePath()); err != nil {
			fmt.Printf("  skipped: %v\n", err)
			continue
		}
		removed++
	}
	fmt.Printf("Removed %s.\n", plural(removed, "item"))
	return removed, nil
}

func cleanupLabel(e layoutEntry) string {
	p := fsutil.ContractPath(e.path)
	switch {
	case e.isDir:
		return fmt.Sprintf("dir    %s (real files)", p)
	case e.broken:
		return fmt.Sprintf("broken %s -> %s", p, e.target)
	default:
		return fmt.Sprintf("link   %s -> %s", p, e.target)
	}
}

// removeUnmanagedItem deletes one cleanup item with guardrails: it
// re-verifies (with the same ownership classification used everywhere)
// that the item is still unmanaged at deletion time, and refuses to touch
// anything resolving into the clone.
func removeUnmanagedItem(e layoutEntry, own linker.Ownership, clonePath string) error {
	fi, err := os.Lstat(e.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // already gone
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		if own.Classify(e.path) == linker.ClassOwned {
			return fmt.Errorf("%s is managed by skillx now; refusing to remove", e.path)
		}
		return os.Remove(e.path)
	}
	if fi.IsDir() {
		// Real files. Refuse anything that resolves into the clone.
		resolvedClone, err := filepath.EvalSymlinks(clonePath)
		if err != nil {
			resolvedClone = filepath.Clean(clonePath)
		}
		resolved, err := filepath.EvalSymlinks(e.path)
		if err != nil {
			resolved = filepath.Clean(e.path)
		}
		if resolved == resolvedClone || strings.HasPrefix(resolved, resolvedClone+string(filepath.Separator)) {
			return fmt.Errorf("%s resolves into the clone; refusing to remove", e.path)
		}
		return os.RemoveAll(e.path)
	}
	return fmt.Errorf("%s is a regular file; refusing to remove", e.path)
}

// detectAgents reports which agents' base dirs (e.g. ~/.claude) exist here.
func detectAgents(cfg *config.Config) map[string]bool {
	out := map[string]bool{}
	for name, a := range cfg.Agents {
		base := filepath.Dir(fsutil.ExpandPath(a.Path)) // ~/.claude/skills -> ~/.claude
		if st, err := os.Stat(base); err == nil && st.IsDir() {
			out[name] = true
		}
	}
	return out
}

func agentNamesSorted(cfg *config.Config) []string {
	return sortedKeys(cfg.Agents)
}

// absolutize anchors a user-supplied path to the current directory so the
// stored config means the same thing from anywhere. "~" paths pass through
// (they are expanded at use time).
func absolutize(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || filepath.IsAbs(p) {
		return p
	}
	if abs, err := filepath.Abs(p); err == nil {
		return fsutil.ContractPath(abs)
	}
	return p
}

func pathExistsNonEmpty(p string) bool {
	entries, err := os.ReadDir(p)
	return err == nil && len(entries) > 0
}

// sameRepoURL compares git URLs leniently (trailing ".git" and "/" ignored).
func sameRepoURL(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.TrimSuffix(s, "/")
		s = strings.TrimSuffix(s, ".git")
		return s
	}
	return norm(a) == norm(b)
}
