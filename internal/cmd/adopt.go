package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mbroton/skillx/internal/adopt"
	"github.com/mbroton/skillx/internal/config"
	"github.com/mbroton/skillx/internal/fsutil"
	"github.com/mbroton/skillx/internal/gitx"
	"github.com/mbroton/skillx/internal/ui"
)

var adoptCmd = &cobra.Command{
	Use:   "adopt",
	Short: "Migrate existing skill installs (e.g. from `npx skills`) into your repo",
	Long: `Detects skills that are not yet managed by skillx:

  - real directories with a SKILL.md in the hub (~/.agents/skills), the
    layout ` + "`npx skills`" + ` uses, and
  - real skill directories copied straight into agent skills dirs.

Selected skills are MOVED into the bag (<clone>/skills/<name>), committed,
and re-linked exactly where they were installed before: agents are derived
from the existing spoke links, so the machine's install state is preserved
while the files move into the bag. No provenance is recorded (the origin is
unknown) and nothing is written to the manifest.`,
	Args: cobra.NoArgs,
	RunE: runAdopt,
}

var (
	adoptAll    bool
	adoptSkills []string
	adoptDryRun bool
	adoptForce  bool
)

func init() {
	adoptCmd.Flags().BoolVar(&adoptAll, "all", false, "adopt every detected skill")
	adoptCmd.Flags().StringArrayVar(&adoptSkills, "skill", nil, "skill name to adopt (repeatable)")
	adoptCmd.Flags().BoolVar(&adoptDryRun, "dry-run", false, "print what would be adopted without changing anything")
	adoptCmd.Flags().BoolVar(&adoptForce, "force", false, "overwrite a same-named skill already in the clone")
	rootCmd.AddCommand(adoptCmd)
}

func runAdopt(cmd *cobra.Command, args []string) error {
	cfg, _, err := loadConfigAndManifest()
	if err != nil {
		return err
	}
	_, err = adoptFlow(cfg, adoptOptions{
		all:    adoptAll,
		skills: adoptSkills,
		dryRun: adoptDryRun,
		force:  adoptForce,
	})
	return err
}

type adoptOptions struct {
	all    bool
	skills []string
	dryRun bool
	force  bool
}

// detectAdoptable finds adoption candidates for the current config.
func detectAdoptable(cfg *config.Config) ([]adopt.Candidate, error) {
	dirs, err := agentDirs(cfg, nil)
	if err != nil {
		return nil, err
	}
	return adopt.Detect(cfg.ExpandedHub(), dirs)
}

// adoptFlow is shared by `skillx adopt` and the offer inside `skillx init`.
// It reports how many skills were adopted.
func adoptFlow(cfg *config.Config, opts adoptOptions) (int, error) {
	candidates, err := detectAdoptable(cfg)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		fmt.Println("No existing skills found to adopt.")
		return 0, nil
	}

	selected, err := selectCandidates(candidates, opts)
	if err != nil {
		return 0, err
	}
	if len(selected) == 0 {
		fmt.Println("Nothing selected; no changes made.")
		return 0, nil
	}

	clone := cfg.ExpandedClonePath()
	adopted := 0
	var moved []adopt.Candidate
	for _, c := range selected {
		label := "spokes: " + strings.Join(c.Agents, ", ")
		if len(c.Agents) == 0 {
			label = "no spokes"
		}
		dest := filepath.Join(cfg.SkillsDir(), c.Name)
		if _, err := os.Stat(dest); err == nil && !opts.force {
			fmt.Printf("skip %s: %s already exists in the bag (use --force to overwrite)\n",
				c.Name, fsutil.ContractPath(dest))
			continue
		}
		if opts.dryRun {
			fmt.Printf("would adopt %s from %s (%s)\n",
				c.Name, fsutil.ContractPath(c.Dir), label)
			continue
		}
		if opts.force {
			if err := os.RemoveAll(dest); err != nil {
				return adopted, err
			}
		}
		if err := fsutil.MoveDir(c.Dir, dest); err != nil {
			return adopted, fmt.Errorf("adopt %s: %w", c.Name, err)
		}
		// A `--copy` marker travels with the files; it has no meaning in the bag.
		_ = os.Remove(filepath.Join(dest, copyMarker))
		fmt.Printf("adopted %s -> %s (%s)\n",
			c.Name, fsutil.ContractPath(dest), label)
		for _, extra := range c.ExtraCopies {
			fmt.Printf("note: another copy of %s remains at %s; skillx will not delete it — remove it manually\n",
				c.Name, extra)
		}
		moved = append(moved, c)
		adopted++
	}

	if opts.dryRun || adopted == 0 {
		return 0, nil
	}
	msg := fmt.Sprintf("skillx: adopt %d existing skill(s)", adopted)
	if committed, err := gitx.AddAndCommit(clone, msg); err != nil {
		return adopted, err
	} else if committed {
		fmt.Printf("Committed to the bag: %s\n", msg)
	}

	// Re-link each adopted skill exactly where it was installed before:
	// hub link always (the files just moved out of the hub), spokes only
	// for the agents that already had them. The old npx-style spokes point
	// at the same hub paths, so they now resolve into the clone and are
	// re-fastened as owned links.
	fmt.Println()
	for _, c := range moved {
		var err error
		if len(c.Agents) == 0 {
			_, err = installHubLinks(cfg, []string{c.Name})
		} else {
			_, err = installSkills(cfg, []string{c.Name}, c.Agents)
		}
		if err != nil {
			return adopted, err
		}
	}
	return adopted, nil
}

func selectCandidates(candidates []adopt.Candidate, opts adoptOptions) ([]adopt.Candidate, error) {
	if opts.all {
		return candidates, nil
	}
	if len(opts.skills) > 0 {
		byName := map[string]adopt.Candidate{}
		for _, c := range candidates {
			byName[c.Name] = c
		}
		var out []adopt.Candidate
		for _, name := range opts.skills {
			c, ok := byName[name]
			if !ok {
				return nil, fmt.Errorf("no adoptable skill named %q (found: %s)", name, candidateNames(candidates))
			}
			out = append(out, c)
		}
		return out, nil
	}
	if !ui.IsInteractive() {
		if opts.dryRun {
			return candidates, nil // read-only: preview everything
		}
		return nil, ui.NonInteractiveErr("a skill selection", "--all or --skill <name>")
	}
	options := make([]ui.Option, len(candidates))
	for i, c := range candidates {
		where := "hub"
		if !c.FromHub {
			where = "copied"
		}
		label := fmt.Sprintf("%s (%s", c.Name, where)
		if len(c.Agents) > 0 {
			label += "; agents: " + strings.Join(c.Agents, ", ")
		}
		label += ")"
		options[i] = ui.Option{Label: label, Value: c.Name, Selected: true}
	}
	chosen, err := ui.MultiSelect("Adopt which existing skills into your repo?", options)
	if err != nil {
		return nil, err
	}
	pick := map[string]bool{}
	for _, name := range chosen {
		pick[name] = true
	}
	var out []adopt.Candidate
	for _, c := range candidates {
		if pick[c.Name] {
			out = append(out, c)
		}
	}
	return out, nil
}

func candidateNames(candidates []adopt.Candidate) string {
	names := make([]string, len(candidates))
	for i, c := range candidates {
		names[i] = c.Name
	}
	return strings.Join(names, ", ")
}
