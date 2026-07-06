package cmd

import (
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

var updateCmd = &cobra.Command{
	Use:   "update [skill...]",
	Short: "Pull the bag from origin, or re-vendor third-party skills",
	Long: `Without arguments: fetches the bag, shows incoming changes, fast-forwards
(after confirmation, or with --yes), then prunes owned dangling links —
skills removed from the bag on another machine disappear here too. That is
the only automatic reconciliation skillx does, and it only ever touches
links verified as its own.

With skill names: for each vendored skill, re-fetches its source repo,
compares against the recorded commit, shows what changed, and (after
confirmation) re-vendors it, updates the provenance commit, and commits.`,
	RunE: runUpdate,
}

var updateYes bool

func init() {
	updateCmd.Flags().BoolVarP(&updateYes, "yes", "y", false, "do not ask for confirmation")
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(cmd *cobra.Command, args []string) error {
	cfg, m, err := loadConfigAndManifest()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return updateBag(cfg)
	}
	return updateVendored(cfg, m, args)
}

func updateBag(cfg *config.Config) error {
	clone := cfg.ExpandedClonePath()
	fmt.Println("Fetching origin ...")
	if err := gitx.Fetch(clone); err != nil {
		return fmt.Errorf("fetch failed (offline?): %w", err)
	}
	_, behind, err := gitx.AheadBehind(clone)
	if err != nil {
		return fmt.Errorf("no upstream configured for the clone: %w", err)
	}
	if behind == 0 {
		fmt.Println("The bag is up to date with origin.")
	} else {
		log, err := gitx.LogRange(clone, "HEAD", "@{upstream}", "")
		if err == nil && log != "" {
			fmt.Printf("Incoming changes (%d commit(s)):\n%s\n\n", behind, log)
		}
		if !updateYes {
			if !ui.IsInteractive() {
				return ui.NonInteractiveErr("confirmation to pull", "--yes")
			}
			ok, err := ui.Confirm(fmt.Sprintf("Fast-forward the bag by %d commit(s)?", behind), true)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("aborted")
			}
		}
		if err := gitx.PullFFOnly(clone); err != nil {
			return err
		}
	}

	// Prune owned dangling links: skills removed on another machine leave
	// hub/spoke links pointing at nothing. Ownership is verified per link.
	inv, err := bagInventory(cfg)
	if err != nil {
		return err
	}
	spec, err := globalSpec(cfg, inv, nil)
	if err != nil {
		return err
	}
	plan, err := linker.PruneDangling(spec.ManagedDirs(), spec.Ownership())
	if err != nil {
		return err
	}
	if len(plan.Actions) > 0 {
		fmt.Println()
		pruned, err := finishActions(plan, spec.Ownership())
		if err != nil {
			return err
		}
		fmt.Printf("Pruned %s to skills no longer in the bag.\n", plural(pruned, "dangling link"))
	}

	// Point at anything new worth installing.
	installed, err := installedSet(cfg, inv)
	if err != nil {
		return err
	}
	var uninstalled []string
	for _, name := range inv {
		if !installed[name] {
			uninstalled = append(uninstalled, name)
		}
	}
	if len(uninstalled) > 0 {
		fmt.Printf("%s in the bag not installed here (%s) — run `skillx use`.\n",
			plural(len(uninstalled), "skill"), strings.Join(uninstalled, ", "))
	}
	return nil
}

func updateVendored(cfg *config.Config, m *manifest.Manifest, names []string) error {
	clone := cfg.ExpandedClonePath()
	var changed []string
	for _, name := range names {
		entry, ok := m.Skills[name]
		if !ok || !entry.Vendored() {
			fmt.Printf("%s: no provenance recorded (your own skill); update it by editing the bag. Skipping.\n", name)
			continue
		}
		didChange, err := revendor(cfg, m, name, entry)
		if err != nil {
			return fmt.Errorf("update %s: %w", name, err)
		}
		if didChange {
			changed = append(changed, name)
		}
	}
	if len(changed) > 0 {
		if err := m.Save(clone); err != nil {
			return err
		}
		if _, err := gitx.AddAndCommit(clone, fmt.Sprintf("skillx: update %s", strings.Join(changed, ", "))); err != nil {
			return err
		}
		fmt.Println("Committed updates to the bag (push when ready).")
	}
	return nil
}

// revendor fetches a vendored skill's source and re-copies it when the
// source moved past the recorded commit. Reports whether anything changed.
func revendor(cfg *config.Config, m *manifest.Manifest, name string, entry manifest.Skill) (bool, error) {
	tmp, err := os.MkdirTemp("", "skillx-update-*")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)

	fmt.Printf("%s: fetching %s ...\n", name, entry.Source)
	// The recorded clone URL keeps the original transport (e.g. SSH), and
	// the recorded branch keeps non-default-branch vendors honest. Full
	// clone so we can show history between the recorded and new commits.
	if err := gitx.Clone(entry.CloneURL(), tmp, false, entry.Branch); err != nil {
		return false, err
	}
	newCommit, err := gitx.Head(tmp)
	if err != nil {
		return false, err
	}
	if newCommit == entry.Commit {
		fmt.Printf("%s: up to date (%.7s)\n", name, entry.Commit)
		return false, nil
	}

	skillSrc := filepath.Join(tmp, filepath.FromSlash(entry.Path))
	if st, err := os.Stat(skillSrc); err != nil || !st.IsDir() {
		return false, fmt.Errorf("path %q no longer exists in %s", entry.Path, entry.Source)
	}
	fmt.Printf("%s: %.7s -> %.7s\n", name, entry.Commit, newCommit)
	if entry.Commit != "" && gitx.HasCommit(tmp, entry.Commit) {
		if log, err := gitx.LogRange(tmp, entry.Commit, newCommit, entry.Path); err == nil && log != "" {
			fmt.Println(log)
		} else if log == "" {
			fmt.Printf("%s: no changes under %s (repo moved for other reasons)\n", name, entry.Path)
		}
	} else {
		fmt.Printf("%s: recorded commit not found in source history; cannot show a diff\n", name)
	}

	if !updateYes {
		if !ui.IsInteractive() {
			return false, ui.NonInteractiveErr("confirmation to re-vendor", "--yes")
		}
		ok, err := ui.Confirm(fmt.Sprintf("Re-vendor %s at %.7s?", name, newCommit), true)
		if err != nil {
			return false, err
		}
		if !ok {
			fmt.Printf("%s: skipped\n", name)
			return false, nil
		}
	}

	dest := filepath.Join(cfg.SkillsDir(), name)
	if err := fsutil.ReplaceDir(skillSrc, dest); err != nil {
		return false, err
	}
	entry.Commit = newCommit
	m.Skills[name] = entry
	fmt.Printf("%s: re-vendored at %.7s\n", name, newCommit)
	return true, nil
}
