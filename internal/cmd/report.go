package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/mbroton/skillx/internal/adopt"
	"github.com/mbroton/skillx/internal/config"
	"github.com/mbroton/skillx/internal/fsutil"
	"github.com/mbroton/skillx/internal/linker"
	"github.com/mbroton/skillx/internal/skillfind"
)

// The layout report renders the current state of the hub and agent dirs,
// grouped by directory (hub first — it is what the spokes actually serve),
// one line per entry, with symlinks vs real directories obvious at a
// glance. It is shared by `skillx status` and `skillx init`'s cleanup
// stage. Presentation only: it never modifies anything.

type entryKind int

const (
	kindManaged entryKind = iota
	kindUnmanagedSkill
	kindUnmanagedLink
	kindForeignLink
	kindBrokenOwned
	kindBrokenUnknown
	kindOther
)

type layoutEntry struct {
	name   string
	path   string // absolute path of the entry
	isLink bool
	isDir  bool
	broken bool
	kind   entryKind
	target string // abbreviated target, for links
	note   string // extra context, e.g. "blocks a managed link"
}

type layoutGroup struct {
	label   string
	entries []layoutEntry
}

type layoutReport struct {
	groups []layoutGroup
	counts map[entryKind]int
}

// Styles degrade to plain text automatically when stdout is not a TTY.
var (
	styleHeader = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleBad    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// buildLayoutReport scans the hub and agent dirs and classifies every
// entry, reusing the linker's ownership classification for symlinks and
// the adopt package's detection for unmanaged installs. conflictPaths (may
// be nil) marks entries that sit where a managed link is desired.
func buildLayoutReport(cfg *config.Config,
	agentFilter []string, conflictPaths map[string]bool) (*layoutReport, error) {

	dirs, err := agentDirs(cfg, agentFilter)
	if err != nil {
		return nil, err
	}
	inv, err := bagInventory(cfg)
	if err != nil {
		return nil, err
	}
	spec, err := globalSpec(cfg, inv, agentFilter)
	if err != nil {
		return nil, err
	}
	own := spec.Ownership()
	hub := cfg.ExpandedHub()

	candidates, err := adopt.Detect(hub, dirs)
	if err != nil {
		return nil, err
	}
	adoptableDirs := map[string]bool{} // literal paths of unmanaged skill dirs
	for _, c := range candidates {
		adoptableDirs[filepath.Clean(c.Dir)] = true
		for _, e := range c.ExtraCopies {
			adoptableDirs[filepath.Clean(e)] = true
		}
	}
	resolvedAdoptable := adopt.ResolvedDirs(candidates)

	r := &layoutReport{counts: map[entryKind]int{}}
	scan := func(dir, label string) error {
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		group := layoutGroup{label: fmt.Sprintf("%s (%s)", fsutil.ContractPath(dir), label)}
		for _, de := range entries {
			path := filepath.Join(dir, de.Name())
			fi, err := os.Lstat(path)
			if err != nil {
				continue
			}
			e := layoutEntry{name: de.Name(), path: path}
			switch {
			case fi.Mode()&os.ModeSymlink != 0:
				e.isLink = true
				target, err := os.Readlink(path)
				if err != nil {
					continue
				}
				e.target = abbrevTarget(path, target, hub)
				switch own.Classify(path) {
				case linker.ClassOwned:
					e.kind = kindManaged
				case linker.ClassOwnedDangling:
					e.kind, e.broken = kindBrokenOwned, true
				case linker.ClassUnknownDangling:
					e.kind, e.broken = kindBrokenUnknown, true
				default: // ClassForeign: resolvable link outside the clone
					// "Unmanaged install" = a spoke pointing INTO THE HUB at
					// a real, adoptable skill dir. Links pointing elsewhere
					// (e.g. into another agent's dir) stay plain foreign,
					// even if they eventually chain to the hub.
					if targetInDir(path, target, hub) && adopt.IsInstallLink(path, resolvedAdoptable) {
						e.kind = kindUnmanagedLink
					} else {
						e.kind = kindForeignLink
					}
				}
			case fi.IsDir():
				e.isDir = true
				if adoptableDirs[filepath.Clean(path)] {
					e.kind = kindUnmanagedSkill
				} else if skillfind.IsSkillDir(path) {
					// Real skill dir not surfaced by adopt (e.g. shadowed by
					// a hub install of the same name). Still unmanaged.
					e.kind = kindUnmanagedSkill
				} else {
					e.kind = kindOther
				}
			default:
				e.kind = kindOther
			}
			if conflictPaths[filepath.Clean(path)] {
				e.note = "blocks a managed link"
			}
			r.counts[e.kind]++
			group.entries = append(group.entries, e)
		}
		sort.Slice(group.entries, func(i, j int) bool {
			return group.entries[i].name < group.entries[j].name
		})
		r.groups = append(r.groups, group)
		return nil
	}

	if err := scan(hub, "hub"); err != nil {
		return nil, err
	}
	agentNames := make([]string, 0, len(dirs))
	for n := range dirs {
		agentNames = append(agentNames, n)
	}
	sort.Strings(agentNames)
	for _, n := range agentNames {
		if err := scan(dirs[n], n); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// targetInDir reports whether a symlink's immediate target points inside dir.
func targetInDir(linkPath, target, dir string) bool {
	return strings.HasPrefix(linker.AbsTarget(linkPath, target), dir+string(filepath.Separator))
}

// abbrevTarget renders a symlink target compactly: targets inside the hub
// become "hub/<name>", everything else gets ~ for the home dir.
func abbrevTarget(linkPath, target, hub string) string {
	abs := linker.AbsTarget(linkPath, target)
	if abs == hub {
		return "hub"
	}
	if strings.HasPrefix(abs, hub+string(filepath.Separator)) {
		return "hub/" + abs[len(hub)+1:]
	}
	return fsutil.ContractPath(abs)
}

// render prints the grouped layout. With showAll=false, healthy managed
// links and ignored entries are skipped; status passes showAll=true to
// list everything.
func (r *layoutReport) render(w io.Writer, showAll bool) {
	for _, g := range r.groups {
		var lines []string
		for _, e := range g.entries {
			if !showAll && (e.kind == kindManaged || e.kind == kindOther) {
				continue
			}
			lines = append(lines, renderEntry(e))
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintln(w, styleHeader.Render(g.label))
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
		fmt.Fprintln(w)
	}
}

func renderEntry(e layoutEntry) string {
	var marker string
	var mstyle lipgloss.Style
	switch {
	case e.broken:
		marker, mstyle = "broken", styleBad
	case e.isLink:
		marker, mstyle = "link  ", styleDim
	case e.isDir:
		marker, mstyle = "dir   ", styleWarn
	default:
		marker, mstyle = "file  ", styleDim
	}

	var label string
	var lstyle lipgloss.Style
	switch e.kind {
	case kindManaged:
		label, lstyle = "managed", styleOK
	case kindUnmanagedSkill:
		label, lstyle = "unmanaged skill (adoptable)", styleWarn
	case kindUnmanagedLink:
		label, lstyle = "unmanaged install (adoptable)", styleWarn
	case kindForeignLink:
		label, lstyle = "foreign", styleDim
	case kindBrokenOwned:
		label, lstyle = "broken skillx link", styleBad
	case kindBrokenUnknown:
		label, lstyle = "broken, origin unknown", styleBad
	default:
		label, lstyle = "ignored (not a skill)", styleDim
	}

	s := "  " + mstyle.Render(marker) + " " + e.name
	if e.isLink {
		s += " -> " + styleDim.Render(e.target)
	}
	s += "  " + lstyle.Render("["+label+"]")
	if e.note != "" {
		s += " " + styleBad.Render("("+e.note+")")
	}
	return s
}

// summaryLines returns the closing summary: counts plus only the hints
// that apply. The "left alone" reassurance lives here, once, instead of on
// every line.
func (r *layoutReport) summaryLines() []string {
	var out []string
	if n := r.counts[kindUnmanagedSkill] + r.counts[kindUnmanagedLink]; n > 0 {
		var parts []string
		if s := r.counts[kindUnmanagedSkill]; s > 0 {
			parts = append(parts, plural(s, "unmanaged skill"))
		}
		if l := r.counts[kindUnmanagedLink]; l > 0 {
			parts = append(parts, plural(l, "unmanaged link"))
		}
		out = append(out, fmt.Sprintf(
			"%s found — run `skillx adopt` to bring them under skillx management.",
			strings.Join(parts, " and ")))
	}
	if n := r.counts[kindBrokenOwned]; n > 0 {
		out = append(out, fmt.Sprintf(
			"%s — `skillx update` prunes them (or `skillx init --clean`).", plural(n, "broken skillx link")))
	}
	if n := r.counts[kindBrokenUnknown]; n > 0 {
		out = append(out, fmt.Sprintf(
			"%s of unknown origin — left alone; remove manually if unwanted.",
			plural(n, "broken link")))
	}
	if n := r.counts[kindForeignLink]; n > 0 {
		out = append(out, fmt.Sprintf(
			"%s unrelated to skillx — left alone.", plural(n, "foreign link")))
	}
	return out
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// cleanupCandidates returns the entries `skillx init`'s cleanup stage may
// offer to remove: unmanaged real skill dirs, unmanaged install links, and
// broken links. Managed entries, truly foreign links (they may serve a
// purpose skillx cannot judge), and non-skill files/dirs are never offered.
func (r *layoutReport) cleanupCandidates() []layoutEntry {
	var out []layoutEntry
	for _, g := range r.groups {
		for _, e := range g.entries {
			switch e.kind {
			case kindUnmanagedSkill, kindUnmanagedLink, kindBrokenOwned, kindBrokenUnknown:
				out = append(out, e)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// stage prints a visually distinct stage header, preceded by a blank line
// so consecutive stages never run together.
func stage(title string) {
	fmt.Println()
	fmt.Println(styleHeader.Render("── " + title + " ──"))
}

// conflictPathsOf collects the paths of plan conflicts (regular entries
// sitting where a managed link is desired).
func conflictPathsOf(plan *linker.Plan) map[string]bool {
	out := map[string]bool{}
	for _, is := range plan.Issues {
		if is.Kind == linker.IssueConflict {
			out[filepath.Clean(is.Path)] = true
		}
	}
	return out
}
