package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mbroton/skillx/internal/fsutil"
	"github.com/mbroton/skillx/internal/gitx"
	"github.com/mbroton/skillx/internal/manifest"
	"github.com/mbroton/skillx/internal/refparse"
	"github.com/mbroton/skillx/internal/skillfind"
	"github.com/mbroton/skillx/internal/ui"
)

var addCmd = &cobra.Command{
	Use:   "add <ref>",
	Short: "Collect a skill into your bag (and start using it here)",
	Long: `Vendors one or more skills from <ref> into your bag — the skills repo —
and commits. <ref> may be:

  owner/repo                                    GitHub shorthand
  https://github.com/owner/repo[/tree/br/sub]   full URL (https or ssh)
  git@github.com:owner/repo
  ./some/dir                                    a local path

Remote sources get provenance (source repo, path, commit) recorded in
skillx.toml so ` + "`skillx update <skill>`" + ` can re-vendor them later.
Pushing is manual (or --push).

After vendoring, the new skills are installed on this machine (hub link +
agent spokes); --agent limits which agents, --no-use skips installing.`,
	Args: cobra.ExactArgs(1),
	RunE: runAdd,
}

var (
	addSkills []string
	addAgents []string
	addNoUse  bool
	addPush   bool
	addForce  bool
)

func init() {
	addCmd.Flags().StringArrayVar(&addSkills, "skill", nil, "skill name to collect (repeatable; default: prompt, or the only one found)")
	addCmd.Flags().StringArrayVar(&addAgents, "agent", nil, "install for these agents only (repeatable; default: all enabled)")
	addCmd.Flags().BoolVar(&addNoUse, "no-use", false, "only collect into the bag; do not install on this machine")
	addCmd.Flags().BoolVar(&addPush, "push", false, "push the clone to origin after committing")
	addCmd.Flags().BoolVar(&addForce, "force", false, "overwrite a same-named skill in the bag even when it came from elsewhere")
	rootCmd.AddCommand(addCmd)
}

// fetchedSource is a resolved skill source on local disk.
type fetchedSource struct {
	root     string // directory to search for skills
	repoDir  string // git checkout root ("" for plain local paths)
	source   string // provenance, e.g. "github.com/owner/repo" ("" for local)
	cloneURL string // URL the source was cloned from ("" for local)
	branch   string // branch it was cloned at ("" for the default branch)
	commit   string // resolved commit ("" for local)
	cleanup  func()
}

// fetchRef materializes a parsed ref on local disk (shallow clone for
// remotes) and resolves provenance.
func fetchRef(ref *refparse.Ref) (*fetchedSource, error) {
	if ref.Kind == refparse.Local {
		st, err := os.Stat(ref.LocalPath)
		if err != nil || !st.IsDir() {
			return nil, fmt.Errorf("local path %s is not a directory", ref.LocalPath)
		}
		return &fetchedSource{root: ref.LocalPath, cleanup: func() {}}, nil
	}
	tmp, err := os.MkdirTemp("", "skillx-add-*")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	fmt.Printf("Fetching %s ...\n", ref.Source)
	if err := gitx.Clone(ref.CloneURL, tmp, true, ref.Branch); err != nil {
		cleanup()
		return nil, err
	}
	commit, err := gitx.Head(tmp)
	if err != nil {
		cleanup()
		return nil, err
	}
	root := tmp
	if ref.SubPath != "" {
		root = filepath.Join(tmp, filepath.FromSlash(ref.SubPath))
	}
	return &fetchedSource{root: root, repoDir: tmp, source: ref.Source,
		cloneURL: ref.CloneURL, branch: ref.Branch, commit: commit, cleanup: cleanup}, nil
}

// selectSkills discovers skills under src and narrows them via --skill flags
// or an interactive multi-select.
func selectSkills(src *fetchedSource) ([]skillfind.Skill, error) {
	found, err := skillfind.Discover(src.root)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no skills found (no directory with a %s) under %s", skillfind.MarkerFile, src.root)
	}
	if len(addSkills) > 0 {
		return skillfind.Filter(found, addSkills)
	}
	if len(found) == 1 {
		return found, nil
	}
	if !ui.IsInteractive() {
		names := make([]string, len(found))
		for i, s := range found {
			names[i] = s.Name
		}
		return nil, fmt.Errorf("multiple skills found (%s) and stdin is not a terminal; pass --skill <name>",
			strings.Join(names, ", "))
	}
	opts := make([]ui.Option, len(found))
	for i, s := range found {
		opts[i] = ui.Option{Label: s.Name, Value: s.Name, Selected: true}
	}
	chosen, err := ui.MultiSelect("Which skills do you want to collect?", opts)
	if err != nil {
		return nil, err
	}
	if len(chosen) == 0 {
		return nil, fmt.Errorf("no skills selected")
	}
	return skillfind.Filter(found, chosen)
}

// skipOverwrite guards against silently replacing a same-named skill that
// came from somewhere else (or is the user's own). Re-vendoring from the
// SAME source is always fine — that is what update does. Otherwise the user
// must confirm (or pass --force); non-interactively it is an error.
func skipOverwrite(m *manifest.Manifest, name, dest, newSource string) (bool, error) {
	if addForce {
		return false, nil
	}
	if _, err := os.Stat(dest); err != nil {
		return false, nil // nothing there yet
	}
	prev := m.Skills[name]
	if prev.Vendored() && newSource != "" && prev.Source == newSource {
		return false, nil // same provenance; a plain refresh
	}
	from := "your own skill, no provenance"
	if prev.Vendored() {
		from = "from " + prev.Source
	}
	if !ui.IsInteractive() {
		return false, fmt.Errorf("%s already exists in the bag (%s); pass --force to overwrite it", name, from)
	}
	ok, err := ui.Confirm(fmt.Sprintf("%s already exists in the bag (%s). Overwrite it?", name, from), false)
	if err != nil {
		return false, err
	}
	if !ok {
		fmt.Printf("Skipped %s (kept the existing skill).\n", name)
		return true, nil
	}
	return false, nil
}

func runAdd(cmd *cobra.Command, args []string) error {
	cfg, m, err := loadConfigAndManifest()
	if err != nil {
		return err
	}
	if len(addAgents) > 0 {
		if _, err := agentDirs(cfg, addAgents); err != nil {
			return err // validate names early
		}
	}
	ref, err := refparse.Parse(args[0])
	if err != nil {
		return err
	}
	src, err := fetchRef(ref)
	if err != nil {
		return err
	}
	defer src.cleanup()

	skills, err := selectSkills(src)
	if err != nil {
		return err
	}

	// Vendor into the bag.
	clone := cfg.ExpandedClonePath()
	var names []string
	for _, s := range skills {
		dest := filepath.Join(cfg.SkillsDir(), s.Name)
		if skip, err := skipOverwrite(m, s.Name, dest, src.source); err != nil {
			return err
		} else if skip {
			continue
		}
		if err := fsutil.ReplaceDir(s.Dir, dest); err != nil {
			return fmt.Errorf("vendor %s: %w", s.Name, err)
		}
		if src.source != "" {
			entry := manifest.Skill{Source: src.source, Branch: src.branch, Commit: src.commit}
			if src.cloneURL != manifest.DeriveCloneURL(src.source) {
				entry.URL = src.cloneURL // e.g. SSH; keep the transport for updates
			}
			if rel, err := filepath.Rel(src.repoDir, s.Dir); err == nil {
				entry.Path = filepath.ToSlash(rel)
			}
			m.Skills[s.Name] = entry
		} else {
			// Local source: any stale provenance no longer applies.
			delete(m.Skills, s.Name)
		}
		names = append(names, s.Name)
		fmt.Printf("Collected %s into %s\n", s.Name, fsutil.ContractPath(dest))
	}
	if len(names) == 0 {
		fmt.Println("Nothing collected.")
		return nil
	}
	if err := m.Save(clone); err != nil {
		return err
	}
	msg := fmt.Sprintf("skillx: add %s", strings.Join(names, ", "))
	if src.source != "" {
		msg += fmt.Sprintf(" (from %s@%.7s)", src.source, src.commit)
	}
	if committed, err := gitx.AddAndCommit(clone, msg); err != nil {
		return err
	} else if committed {
		fmt.Printf("Committed to the bag: %s\n", msg)
	}
	if addPush {
		fmt.Println("Pushing to origin ...")
		if err := gitx.Push(clone); err != nil {
			return err
		}
	}

	if addNoUse {
		fmt.Printf("Not installed here (--no-use). Run `skillx use %s` when you want it.\n",
			strings.Join(names, " "))
		return nil
	}
	fmt.Println()
	created, err := installSkills(cfg, names, addAgents)
	if err != nil {
		return err
	}
	if created > 0 {
		fmt.Printf("Installed: %s.\n", plural(created, "link"))
	} else {
		fmt.Println("Already installed.")
	}
	return nil
}
