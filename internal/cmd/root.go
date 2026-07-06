// Package cmd wires up the skillx CLI (cobra).
package cmd

import (
	"github.com/spf13/cobra"
)

// version is stamped at build time via
// -ldflags "-X github.com/mbroton/skillx/internal/cmd.version=v1.2.3".
var version = "dev"

var rootCmd = &cobra.Command{
	Use:     "skillx",
	Version: version,
	Short:   "Collect agent skills in a private git repo and use them anywhere",
	Long: `skillx treats your private skills git repo as a bag of skills you collect
(directories with a SKILL.md), and your machines as places you use them:

  the bag       your skills repo; a local clone is the editable copy.
                add / rm / update / list manage it.
  using skills  "skillx use" links skills from the bag into the hub
                (~/.agents/skills) and each agent's skills dir
                (~/.claude/skills, ~/.codex/skills, ...). The filesystem is
                the database — installed means linked, nothing else.

New machine: skillx init && skillx use --all

skillx only ever touches symlinks it owns (links whose whole chain resolves
into the clone) — skills installed by other tools, your own files, and
unattributable broken links are reported, never modified. "skillx adopt"
migrates existing installs into the bag.`,
	SilenceUsage: true,
}

// Execute runs the CLI.
func Execute() error {
	return rootCmd.Execute()
}
