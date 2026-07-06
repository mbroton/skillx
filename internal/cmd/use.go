package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mbroton/skillx/internal/config"
	"github.com/mbroton/skillx/internal/fsutil"
	"github.com/mbroton/skillx/internal/linker"
	"github.com/mbroton/skillx/internal/ui"
)

var useCmd = &cobra.Command{
	Use:   "use [skill...]",
	Short: "Choose which bag skills are installed on this machine",
	Long: `Installs skills from the bag onto this machine (hub link into the clone,
spoke links into each enabled agent's skills dir) — and uninstalls them.

  skillx use                 one multi-select of every skill in the bag,
                             pre-checked = currently installed; check to
                             install, uncheck to remove, Enter applies both
  skillx use <skill>...      install those skills
  skillx use --all           install everything (the new-machine one-liner)
  skillx use --drop <skill>  uninstall (repeatable; scripting/non-TTY)
  skillx use <skill> --local copy the skill into the current project
                             (<project>/.agents/skills + relative spokes);
                             plain "use --local" re-fastens project links

--agent limits both directions: installs create links only for those
agents, and --drop removes only their spokes (the hub link goes too once
no agent's spoke remains).

Everything is reversible: installing is just symlinks, and uninstalling
never deletes skill files from the bag (that is ` + "`skillx rm`" + `).`,
	RunE: runUse,
}

var (
	useAll    bool
	useDrop   []string
	useAgents []string
	useLocal  bool
	useCopy   bool
)

func init() {
	useCmd.Flags().BoolVar(&useAll, "all", false, "install every skill in the bag")
	useCmd.Flags().StringArrayVar(&useDrop, "drop", nil, "uninstall this skill's links (repeatable)")
	useCmd.Flags().StringArrayVar(&useAgents, "agent", nil, "limit to these agents (repeatable; default: all enabled)")
	useCmd.Flags().BoolVar(&useLocal, "local", false, "install into the current project instead of globally")
	useCmd.Flags().BoolVar(&useCopy, "copy", false, "copy files into agent dirs instead of symlinking (escape hatch)")
	rootCmd.AddCommand(useCmd)
}

func runUse(cmd *cobra.Command, args []string) error {
	if useLocal {
		switch {
		case useAll:
			return fmt.Errorf("--local cannot be combined with --all; name the skills to copy into the project")
		case len(useDrop) > 0:
			return fmt.Errorf("--local cannot be combined with --drop; delete the skill from <project>/.agents/skills and re-run `skillx use --local`")
		case useCopy && len(args) == 0:
			return fmt.Errorf("--local --copy needs skill names to copy")
		}
		return runUseLocal(args)
	}
	cfg, _, err := loadConfigAndManifest()
	if err != nil {
		return err
	}
	inv, err := bagInventory(cfg)
	if err != nil {
		return err
	}

	var toInstall, toDrop []string
	switch {
	case useAll:
		toInstall = inv
		toDrop = useDrop
	case len(args) > 0 || len(useDrop) > 0:
		if err := mustBeInBag(args, inv); err != nil {
			return err
		}
		toInstall = args
		toDrop = useDrop
	case !ui.IsInteractive():
		return ui.NonInteractiveErr("a skill selection", "skill names, --all, or --drop <name>")
	default:
		if len(inv) == 0 {
			fmt.Println("The bag is empty. Collect a skill first: `skillx add <owner/repo>`.")
			return nil
		}
		installed, err := installedSet(cfg, inv)
		if err != nil {
			return err
		}
		opts := make([]ui.Option, len(inv))
		for i, name := range inv {
			opts[i] = ui.Option{Label: name, Value: name, Selected: installed[name]}
		}
		chosen, err := ui.MultiSelect("Use which skills on this machine? (checked = installed)", opts)
		if err != nil {
			return err
		}
		chosenSet := map[string]bool{}
		for _, c := range chosen {
			chosenSet[c] = true
		}
		toInstall = chosen
		for _, name := range inv {
			if installed[name] && !chosenSet[name] {
				toDrop = append(toDrop, name)
			}
		}
	}

	if len(toInstall) == 0 && len(toDrop) == 0 {
		fmt.Println("Nothing to do.")
		return nil
	}

	created := 0
	if len(toInstall) > 0 {
		if useCopy {
			// Escape hatch: real files in the spokes, hub link only via the
			// linker (spoke locations are occupied by the copies).
			if err := copyIntoAgentDirs(cfg, toInstall, useAgents); err != nil {
				return err
			}
			if created, err = installHubLinks(cfg, toInstall); err != nil {
				return err
			}
		} else if created, err = installSkills(cfg, toInstall, useAgents); err != nil {
			return err
		}
	}
	removed := 0
	if len(toDrop) > 0 {
		if removed, err = unlinkSkills(cfg, toDrop, "unchecked", useAgents); err != nil {
			return err
		}
	}

	switch {
	case created == 0 && removed == 0:
		fmt.Println("Everything already in place.")
	default:
		var parts []string
		if created > 0 {
			parts = append(parts, fmt.Sprintf("%s created/fixed", plural(created, "link")))
		}
		if removed > 0 {
			parts = append(parts, fmt.Sprintf("%s removed", plural(removed, "link")))
		}
		fmt.Printf("Done: %s.\n", strings.Join(parts, ", "))
	}
	return nil
}

func mustBeInBag(names, inventory []string) error {
	inBag := map[string]bool{}
	for _, n := range inventory {
		inBag[n] = true
	}
	for _, n := range names {
		if !inBag[n] {
			return fmt.Errorf("skill %q is not in the bag (have: %s); collect it first with `skillx add`",
				n, strings.Join(inventory, ", "))
		}
	}
	return nil
}

// runUseLocal copies named bag skills into the current project's local hub
// and (re-)fastens relative spoke links for every skill in that hub. With
// no names it only re-fastens — what `sync --local` used to do.
func runUseLocal(names []string) error {
	cfg, err := loadConfigForLocal()
	if err != nil {
		return err
	}
	root, err := projectRoot()
	if err != nil {
		return err
	}
	localAgents, err := localAgentDirs(cfg, useAgents)
	if err != nil {
		return err
	}
	spec := linker.LocalSpec{ProjectRoot: root, Agents: localAgents}

	if len(names) > 0 {
		inv, err := bagInventory(cfg)
		if err != nil {
			return err
		}
		if err := mustBeInBag(names, inv); err != nil {
			return err
		}
		for _, name := range names {
			src := filepath.Join(cfg.SkillsDir(), name)
			dest := filepath.Join(spec.LocalHubDir(), name)
			if err := fsutil.ReplaceDir(src, dest); err != nil {
				return err
			}
			fmt.Printf("Copied %s into %s\n", name, dest)
		}
	}

	if useCopy {
		for _, name := range names {
			src := filepath.Join(spec.LocalHubDir(), name)
			for _, rel := range localAgents {
				dest := filepath.Join(root, rel, name)
				if err := checkCopyDest(dest, spec.Ownership()); err != nil {
					return err
				}
				if err := fsutil.ReplaceDir(src, dest); err != nil {
					return err
				}
				if err := writeCopyMarker(dest); err != nil {
					return err
				}
				fmt.Printf("Copied %s into %s\n", name, dest)
			}
		}
		return nil
	}

	desired, err := spec.Desired()
	if err != nil {
		return err
	}
	if len(desired) == 0 {
		fmt.Printf("No local hub at %s (nothing to link).\n", spec.LocalHubDir())
		return nil
	}
	plan, err := linker.Compute(linker.Input{
		Desired:     desired,
		ManagedDirs: spec.ManagedDirs(),
		Ownership:   spec.Ownership(),
	})
	if err != nil {
		return err
	}
	if err := finishPlan(plan, spec.Ownership()); err != nil {
		return err
	}
	fmt.Println("Commit .agents/ and the agent skill links to your project's repo.")
	return nil
}

// copyIntoAgentDirs is the --copy escape hatch: real files in the spokes.
// Existing skillx links and previous --copy output (recognized by their
// marker file) are replaced; anything else in the way is refused — a real
// skill directory installed by another tool is never overwritten.
func copyIntoAgentDirs(cfg *config.Config, skillNames, agents []string) error {
	dirs, err := agentDirs(cfg, agents)
	if err != nil {
		return err
	}
	own := linker.NewOwnership(cfg.SkillsDir())
	for _, name := range skillNames {
		src := filepath.Join(cfg.SkillsDir(), name)
		for _, dir := range dirs {
			dest := filepath.Join(dir, name)
			if err := checkCopyDest(dest, own); err != nil {
				return err
			}
			if err := fsutil.ReplaceDir(src, dest); err != nil {
				return err
			}
			if err := writeCopyMarker(dest); err != nil {
				return err
			}
			fmt.Printf("Copied %s into %s (escape hatch; status reports it as unmanaged)\n",
				name, fsutil.ContractPath(dest))
		}
	}
	return nil
}

// checkCopyDest decides whether --copy may replace what currently sits at
// dest: nothing, a symlink skillx owns, or a previous --copy run (marker
// file present). Everything else — foreign links, real skill directories
// from other tools, user files — is refused.
func checkCopyDest(dest string, own linker.Ownership) error {
	fi, err := os.Lstat(dest)
	if err != nil {
		return nil // nothing there
	}
	isLink := fi.Mode()&os.ModeSymlink != 0
	switch {
	case isLink && own.OwnsLink(dest):
		return os.Remove(dest) // our spoke; make room for the copy
	case !isLink && fi.IsDir() && hasCopyMarker(dest):
		return nil // previous --copy run; ReplaceDir refreshes it
	default:
		return fmt.Errorf("refusing to overwrite %s: not created by skillx (adopt it with `skillx adopt`, or remove it yourself)",
			fsutil.ContractPath(dest))
	}
}
