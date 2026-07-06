package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mbroton/skillx/internal/fsutil"
	"github.com/mbroton/skillx/internal/gitx"
)

var statusCmd = &cobra.Command{
	Use:     "status",
	Aliases: []string{"doctor"},
	Short:   "Inspect this machine: bag state, hub and agent dirs, leftovers",
	Args:    cobra.NoArgs,
	RunE:    runStatus,
}

var statusOffline bool

func init() {
	statusCmd.Flags().BoolVar(&statusOffline, "offline", false, "skip the network fetch; freshness is as of the last fetch")
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg, _, err := loadConfigAndManifest()
	if err != nil {
		return err
	}
	clone := cfg.ExpandedClonePath()
	dirs, _ := agentDirs(cfg, nil) // nil filter cannot fail
	fmt.Printf("repo:   %s\n", cfg.Repo)
	fmt.Printf("clone:  %s\n", fsutil.ContractPath(clone))
	fmt.Printf("hub:    %s\n", fsutil.ContractPath(cfg.ExpandedHub()))
	fmt.Printf("agents: %s\n", joinNames(dirs))
	fmt.Println()

	// Bag (clone) state.
	if dirty, err := gitx.Dirty(clone); err != nil {
		fmt.Printf("bag: cannot inspect (%v)\n", err)
	} else if dirty {
		fmt.Println("bag: has uncommitted changes")
	} else {
		fmt.Println("bag: clean")
	}
	fetched := !statusOffline
	if fetched {
		if err := gitx.Fetch(clone); err != nil {
			fmt.Printf("bag: remote unreachable (%v)\n", err)
			fetched = false
		}
	}
	if ahead, behind, err := gitx.AheadBehind(clone); err != nil {
		fmt.Println("bag: no upstream configured, skipping freshness check")
	} else {
		asOf := ""
		if !fetched {
			asOf = " (as of the last fetch)"
		}
		switch {
		case behind > 0 && ahead > 0:
			fmt.Printf("bag: diverged from origin (%d ahead, %d behind)%s; run `skillx update`\n", ahead, behind, asOf)
		case behind > 0:
			fmt.Printf("bag: %d commit(s) behind origin%s; run `skillx update`\n", behind, asOf)
		case ahead > 0:
			fmt.Printf("bag: %d commit(s) not pushed to origin%s\n", ahead, asOf)
		default:
			fmt.Printf("bag: up to date with origin%s\n", asOf)
		}
	}
	fmt.Println()

	// Full grouped layout: hub first, then each agent dir, every entry
	// listed with symlinks and real directories clearly distinguished.
	report, err := buildLayoutReport(cfg, nil, nil)
	if err != nil {
		return err
	}
	report.render(os.Stdout, true)
	fmt.Printf("%s on this machine.\n", plural(report.counts[kindManaged], "managed link"))

	hints := report.summaryLines()
	for _, l := range hints {
		fmt.Println(l)
	}
	return nil
}
