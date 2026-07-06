package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mbroton/skillx/internal/gitx"
)

var rmCmd = &cobra.Command{
	Use:     "rm <skill>",
	Aliases: []string{"remove"},
	Short:   "Remove a skill from the bag (and unlink it here)",
	Long: `Deletes the skill from the bag — its files in the clone and any
provenance entry — commits, and removes its links on this machine in the
same breath (links to a deleted skill are useless). Push is manual.

To stop using a skill on this machine while keeping it in the bag, use
` + "`skillx use --drop <skill>`" + ` instead.`,
	Args: cobra.ExactArgs(1),
	RunE: runRm,
}

func init() {
	rootCmd.AddCommand(rmCmd)
}

func runRm(cmd *cobra.Command, args []string) error {
	cfg, m, err := loadConfigAndManifest()
	if err != nil {
		return err
	}
	name := args[0]
	inv, err := bagInventory(cfg)
	if err != nil {
		return err
	}
	if err := mustBeInBag([]string{name}, inv); err != nil {
		return err
	}

	// Unlink first, while the links still resolve.
	removed, err := unlinkSkills(cfg, []string{name}, "removed from the bag", nil)
	if err != nil {
		return err
	}
	if removed > 0 {
		fmt.Printf("Unlinked %s here (%s).\n", name, plural(removed, "link"))
	}

	// Then delete from the bag.
	clone := cfg.ExpandedClonePath()
	canonical := filepath.Join(cfg.SkillsDir(), name)
	if _, err := gitx.Run(clone, "rm", "-r", "--ignore-unmatch", "-q", "--", filepath.Join("skills", name)); err != nil {
		return err
	}
	if err := os.RemoveAll(canonical); err != nil { // untracked leftovers
		return err
	}
	delete(m.Skills, name)
	if err := m.Save(clone); err != nil {
		return err
	}
	msg := fmt.Sprintf("skillx: rm %s", name)
	if committed, err := gitx.AddAndCommit(clone, msg); err != nil {
		return err
	} else if committed {
		fmt.Printf("Committed to the bag: %s (push when ready).\n", msg)
	}
	fmt.Printf("%s is gone from the bag. Other machines drop their links on their next `skillx update`.\n", name)
	return nil
}
