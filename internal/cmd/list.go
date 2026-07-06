package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "Show the bag: every skill, its source, and where it's installed here",
	Args:  cobra.NoArgs,
	RunE:  runList,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	cfg, m, err := loadConfigAndManifest()
	if err != nil {
		return err
	}
	inv, err := bagInventory(cfg)
	if err != nil {
		return err
	}
	if len(inv) == 0 {
		fmt.Println("The bag is empty. Collect a skill: `skillx add <owner/repo>`.")
		return nil
	}
	own, err := bagOwnership(cfg)
	if err != nil {
		return err
	}
	installed, err := installedSet(cfg, inv)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SKILL\tSOURCE\tINSTALLED")
	for _, name := range inv {
		source := "own"
		if s, ok := m.Skills[name]; ok && s.Vendored() {
			source = s.Source
			if s.Commit != "" {
				source += fmt.Sprintf("@%.7s", s.Commit)
			}
		}
		state := "-"
		if installed[name] {
			if agents := spokeAgents(cfg, own, name); len(agents) > 0 {
				state = strings.Join(agents, ", ")
			} else {
				state = "hub only"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", name, source, state)
	}
	return w.Flush()
}
