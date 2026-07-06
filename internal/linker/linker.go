// Package linker is skillx's hub-and-spoke symlink engine.
//
// It computes a plan (create / fix / remove symlinks) from a desired state
// and applies it. The central safety rule: the linker only ever creates,
// modifies, or deletes symlinks it OWNS.
//
// Ownership comes in two modes, because the two scopes have different
// canonical directories:
//
//   - GLOBAL scope (resolution-based, NewOwnership): a link is owned iff
//     following its whole symlink chain lands inside the CLONE's skills
//     directory — the only directory skillx exclusively owns. "Points into
//     the hub" is NOT sufficient: the hub (~/.agents/skills) legitimately
//     contains real directories managed by other tools (e.g. `npx skills`),
//     and spokes resolving to those are foreign even though they point into
//     the hub. Links whose chain cannot fully resolve are classified best
//     effort: if the unresolvable step points into the clone, or the link's
//     basename matches a manifest skill, they are owned-dangling (reported;
//     removed only under --prune); anything else is unknown-dangling and is
//     NEVER removed by the linker (init's explicit opt-in cleanup stage is
//     the one place such links may be deleted). When in doubt, report
//     instead of delete.
//
//   - LOCAL scope (pointer-based, NewPointerOwnership): inside a project the
//     local hub (<project>/.agents/skills) contains real files BY DESIGN and
//     is itself the canonical source, so a link whose target points into the
//     local hub is legitimately managed there — resolvable or not. Full-chain
//     resolution would be wrong here: it cannot distinguish "our spoke whose
//     hub entry was deleted" from anything else, and there is no third party
//     sharing the local hub to protect.
//
// The package is pure with respect to skillx: it works on plain paths and
// knows nothing about config files, manifests, or git.
package linker

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Op is a planned operation on a symlink.
type Op int

const (
	// OpCreate creates a new symlink.
	OpCreate Op = iota
	// OpFix replaces an owned symlink whose target is wrong.
	OpFix
	// OpRemove deletes an owned symlink that should no longer exist.
	OpRemove
)

func (o Op) String() string {
	switch o {
	case OpCreate:
		return "create"
	case OpFix:
		return "fix"
	case OpRemove:
		return "remove"
	default:
		return "unknown"
	}
}

// Action is one planned symlink operation.
type Action struct {
	Op     Op
	Path   string // the symlink location
	Target string // desired target (empty for OpRemove)
	Old    string // previous target for OpFix / OpRemove
	Reason string
}

func (a Action) String() string {
	switch a.Op {
	case OpCreate:
		return fmt.Sprintf("create %s -> %s", a.Path, a.Target)
	case OpFix:
		return fmt.Sprintf("fix    %s -> %s (was %s)", a.Path, a.Target, a.Old)
	case OpRemove:
		return fmt.Sprintf("remove %s (-> %s; %s)", a.Path, a.Old, a.Reason)
	}
	return ""
}

// IssueKind classifies things the linker refuses to touch or wants to flag.
type IssueKind int

const (
	// IssueConflict: a regular file/dir sits where a link is desired.
	IssueConflict IssueKind = iota
	// IssueForeignLink: a symlink not owned by skillx is in the way or
	// present in a managed directory.
	IssueForeignLink
	// IssueForeignEntry: a regular file/dir inside a managed directory.
	IssueForeignEntry
	// IssueDangling: an owned link whose canonical source files are missing
	// (removed only with --prune).
	IssueDangling
	// IssueUnknownDangling: a broken symlink whose origin cannot be
	// determined. Never removed, not even with --prune.
	IssueUnknownDangling
)

func (k IssueKind) String() string {
	switch k {
	case IssueConflict:
		return "conflict"
	case IssueForeignLink:
		return "foreign link"
	case IssueForeignEntry:
		return "foreign entry"
	case IssueDangling:
		return "dangling"
	case IssueUnknownDangling:
		return "dangling (unknown origin)"
	default:
		return "unknown"
	}
}

// Issue is something the linker reports but does not act on (except
// IssueDangling under prune).
type Issue struct {
	Kind   IssueKind
	Path   string
	Detail string
}

func (i Issue) String() string {
	return fmt.Sprintf("%s: %s (%s)", i.Kind, i.Path, i.Detail)
}

// Desired is one symlink that should exist. Target is written verbatim into
// the symlink (it may be relative to the link's directory).
type Desired struct {
	Path   string
	Target string
	// SourceMissing marks a desired link whose ultimate source files do not
	// exist (e.g. a manifest skill whose directory is gone from the clone).
	// Such links are never created; existing ones are flagged as dangling
	// and removed only under prune.
	SourceMissing bool
}

// LinkClass is the ownership classification of an existing symlink.
type LinkClass int

const (
	// ClassForeign: not skillx's; never touched.
	ClassForeign LinkClass = iota
	// ClassOwned: skillx's; may be fixed or removed.
	ClassOwned
	// ClassOwnedDangling: broken, but best-effort evidence says it is
	// skillx's (chain heads into the clone, or the name is in the manifest).
	// Reported; removed only under prune.
	ClassOwnedDangling
	// ClassUnknownDangling: broken and unattributable. Reported, never
	// removed.
	ClassUnknownDangling
)

// maxChainHops bounds symlink chain walks (mirrors typical ELOOP limits).
const maxChainHops = 40

// Ownership decides whether a symlink belongs to skillx. See the package
// comment for the two modes.
type Ownership struct {
	roots         []string // cleaned roots + their EvalSymlinks forms
	pointerMode   bool     // local scope: pointing into a root suffices
	manifestNames map[string]bool
}

// NewOwnership builds a resolution-based (global scope) ownership domain:
// a link is owned iff its full symlink chain resolves into one of the roots
// (the clone's skills dir). Roots are canonicalized best-effort.
func NewOwnership(canonicalRoots ...string) Ownership {
	return Ownership{roots: expandRoots(canonicalRoots)}
}

// NewPointerOwnership builds a pointer-based (local scope) ownership domain:
// a link is owned iff its immediate target points into one of the roots
// (the project's local hub), whether or not it resolves.
func NewPointerOwnership(roots ...string) Ownership {
	return Ownership{roots: expandRoots(roots), pointerMode: true}
}

// WithManifestNames registers skill names used as a fallback when
// classifying broken links: a dangling link whose basename is a manifest
// skill is treated as owned-dangling rather than unknown.
func (o Ownership) WithManifestNames(names []string) Ownership {
	o.manifestNames = map[string]bool{}
	for _, n := range names {
		o.manifestNames[n] = true
	}
	return o
}

func expandRoots(roots []string) []string {
	var out []string
	for _, r := range roots {
		if r == "" {
			continue
		}
		clean := filepath.Clean(r)
		out = append(out, clean)
		if resolved, err := filepath.EvalSymlinks(clean); err == nil && resolved != clean {
			out = append(out, resolved)
		}
	}
	return out
}

// OwnsTarget reports whether a symlink target (as read from the link at
// linkPath) points into the ownership roots. This is a pure path-prefix
// check; global-scope ownership additionally requires resolution (Classify).
func (o Ownership) OwnsTarget(linkPath, target string) bool {
	return o.underRoots(AbsTarget(linkPath, target))
}

func (o Ownership) underRoots(abs string) bool {
	for _, root := range o.roots {
		if abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// AbsTarget resolves a symlink target (as read from the link at linkPath)
// to a cleaned absolute path: relative targets are taken relative to the
// link's directory.
func AbsTarget(linkPath, target string) string {
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}
	return filepath.Clean(target)
}

// Classify determines what the symlink at path is to skillx. Non-symlinks
// (including missing paths) are ClassForeign.
func (o Ownership) Classify(path string) LinkClass {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return ClassForeign
	}
	target, err := os.Readlink(path)
	if err != nil {
		return ClassForeign
	}

	if o.pointerMode {
		if o.OwnsTarget(path, target) {
			return ClassOwned
		}
		return ClassForeign
	}

	// Resolution mode: the whole chain must land inside a root.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		if o.underRoots(filepath.Clean(resolved)) {
			return ClassOwned
		}
		return ClassForeign
	}

	// The chain does not fully resolve — walk it to find the step that
	// breaks and classify best-effort.
	pointsAtUs := false
	cur := path
	for range maxChainHops {
		t, err := os.Readlink(cur)
		if err != nil {
			break
		}
		abs := AbsTarget(cur, t)
		if o.underRoots(abs) {
			pointsAtUs = true
		}
		fi, err := os.Lstat(abs)
		if err != nil {
			break // unresolvable step; abs already inspected
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			// Fully walked but EvalSymlinks failed (e.g. an unreadable
			// intermediate component). Be conservative.
			break
		}
		cur = abs
	}
	if pointsAtUs || o.manifestNames[filepath.Base(path)] {
		return ClassOwnedDangling
	}
	return ClassUnknownDangling
}

// OwnsLink reports whether skillx may modify or delete the symlink at path
// (it classifies as owned or owned-dangling). Apply re-checks this before
// every destructive step.
func (o Ownership) OwnsLink(path string) bool {
	switch o.Classify(path) {
	case ClassOwned, ClassOwnedDangling:
		return true
	default:
		return false
	}
}

// Input describes one planning run.
type Input struct {
	// Desired links that should exist.
	Desired []Desired
	// ManagedDirs are scanned for owned symlinks that are no longer desired
	// (they get removed) and for foreign entries (reported).
	ManagedDirs []string
	// Ownership defines which links skillx may touch.
	Ownership Ownership
	// Prune also removes owned dangling links (broken links attributable to
	// skillx). Unknown broken links are never removed.
	Prune bool
}

// Plan is the computed set of actions plus everything worth reporting.
type Plan struct {
	Actions []Action
	Issues  []Issue
	// OK counts desired links that are already correct.
	OK int
}

// Compute builds a plan. It reads the filesystem but never modifies it.
func Compute(in Input) (*Plan, error) {
	p := &Plan{}
	desiredByPath := map[string]Desired{}
	for _, d := range in.Desired {
		desiredByPath[filepath.Clean(d.Path)] = d
	}

	// Phase 1: desired links.
	for _, d := range in.Desired {
		fi, err := os.Lstat(d.Path)
		switch {
		case err != nil && os.IsNotExist(err):
			if d.SourceMissing {
				p.Issues = append(p.Issues, Issue{IssueDangling, d.Path,
					"skill source files are missing; link not created"})
				continue
			}
			p.Actions = append(p.Actions, Action{Op: OpCreate, Path: d.Path, Target: d.Target})
		case err != nil:
			return nil, fmt.Errorf("lstat %s: %w", d.Path, err)
		case fi.Mode()&os.ModeSymlink != 0:
			current, err := os.Readlink(d.Path)
			if err != nil {
				return nil, fmt.Errorf("readlink %s: %w", d.Path, err)
			}
			class := in.Ownership.Classify(d.Path)
			if d.SourceMissing {
				switch class {
				case ClassOwned, ClassOwnedDangling:
					if in.Prune {
						p.Actions = append(p.Actions, Action{Op: OpRemove, Path: d.Path,
							Old: current, Reason: "dangling: skill source files are missing"})
					} else {
						p.Issues = append(p.Issues, Issue{IssueDangling, d.Path,
							"points to missing skill files; use --prune to remove"})
					}
				case ClassUnknownDangling:
					p.Issues = append(p.Issues, Issue{IssueUnknownDangling, d.Path,
						"broken link of unknown origin; left alone"})
				default:
					p.Issues = append(p.Issues, Issue{IssueForeignLink, d.Path,
						"not owned by skillx; left alone"})
				}
				continue
			}
			if current == d.Target {
				p.OK++
				continue
			}
			switch class {
			case ClassOwned, ClassOwnedDangling:
				p.Actions = append(p.Actions, Action{Op: OpFix, Path: d.Path,
					Target: d.Target, Old: current, Reason: "wrong target"})
			case ClassUnknownDangling:
				p.Issues = append(p.Issues, Issue{IssueUnknownDangling, d.Path,
					fmt.Sprintf("broken link to %s, origin unknown; left alone", current)})
			default:
				p.Issues = append(p.Issues, Issue{IssueForeignLink, d.Path,
					fmt.Sprintf("points to %s, not owned by skillx; left alone", current)})
			}
		default:
			p.Issues = append(p.Issues, Issue{IssueConflict, d.Path,
				"a regular file or directory is in the way; skillx will not touch it"})
		}
	}

	// Phase 2: managed directories — owned strays and foreign entries.
	for _, dir := range in.ManagedDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", dir, err)
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			if _, isDesired := desiredByPath[filepath.Clean(path)]; isDesired {
				continue // handled in phase 1
			}
			fi, err := os.Lstat(path)
			if err != nil {
				continue
			}
			if fi.Mode()&os.ModeSymlink == 0 {
				p.Issues = append(p.Issues, Issue{IssueForeignEntry, path,
					"not managed by skillx"})
				continue
			}
			current, err := os.Readlink(path)
			if err != nil {
				continue
			}
			switch in.Ownership.Classify(path) {
			case ClassOwned:
				p.Actions = append(p.Actions, Action{Op: OpRemove, Path: path,
					Old: current, Reason: "no longer in manifest"})
			case ClassOwnedDangling:
				if in.Prune {
					p.Actions = append(p.Actions, Action{Op: OpRemove, Path: path,
						Old: current, Reason: "dangling skillx link"})
				} else {
					p.Issues = append(p.Issues, Issue{IssueDangling, path,
						"broken skillx link; use --prune to remove"})
				}
			case ClassUnknownDangling:
				p.Issues = append(p.Issues, Issue{IssueUnknownDangling, path,
					fmt.Sprintf("broken link to %s, origin unknown; left alone", current)})
			default:
				p.Issues = append(p.Issues, Issue{IssueForeignLink, path,
					fmt.Sprintf("points to %s, not owned by skillx; left alone", current)})
			}
		}
	}

	sort.Slice(p.Actions, func(i, j int) bool {
		if p.Actions[i].Path != p.Actions[j].Path {
			return p.Actions[i].Path < p.Actions[j].Path
		}
		return p.Actions[i].Op < p.Actions[j].Op
	})
	sort.Slice(p.Issues, func(i, j int) bool { return p.Issues[i].Path < p.Issues[j].Path })
	return p, nil
}

// RemovalsFor plans the removal of the given symlink paths (with reason
// attached to each action). Missing paths are skipped; only links
// classified owned or owned-dangling get remove actions — everything else
// is reported as an issue and left alone.
func RemovalsFor(paths []string, own Ownership, reason string) *Plan {
	p := &Plan{}
	for _, path := range paths {
		fi, err := os.Lstat(path)
		if err != nil {
			continue // nothing there
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			p.Issues = append(p.Issues, Issue{IssueForeignEntry, path,
				"not a symlink; skillx will not touch it"})
			continue
		}
		current, err := os.Readlink(path)
		if err != nil {
			continue
		}
		switch own.Classify(path) {
		case ClassOwned, ClassOwnedDangling:
			p.Actions = append(p.Actions, Action{Op: OpRemove, Path: path,
				Old: current, Reason: reason})
		case ClassUnknownDangling:
			p.Issues = append(p.Issues, Issue{IssueUnknownDangling, path,
				"broken link of unknown origin; left alone"})
		default:
			p.Issues = append(p.Issues, Issue{IssueForeignLink, path,
				fmt.Sprintf("points to %s, not owned by skillx; left alone", current)})
		}
	}
	sort.Slice(p.Actions, func(i, j int) bool { return p.Actions[i].Path < p.Actions[j].Path })
	return p
}

// PruneDangling plans the removal of owned-dangling links inside dirs —
// links attributable to skillx whose skill files no longer exist (e.g. a
// skill removed from the bag on another machine). Healthy owned links,
// foreign links, and unattributable broken links are untouched.
func PruneDangling(dirs []string, own Ownership) (*Plan, error) {
	p := &Plan{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", dir, err)
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			fi, err := os.Lstat(path)
			if err != nil || fi.Mode()&os.ModeSymlink == 0 {
				continue
			}
			if own.Classify(path) != ClassOwnedDangling {
				continue
			}
			current, err := os.Readlink(path)
			if err != nil {
				continue
			}
			p.Actions = append(p.Actions, Action{Op: OpRemove, Path: path,
				Old: current, Reason: "dangling: skill no longer in the bag"})
		}
	}
	sort.Slice(p.Actions, func(i, j int) bool { return p.Actions[i].Path < p.Actions[j].Path })
	return p, nil
}

// Apply executes the plan's actions. Ownership was established by Compute
// under the classification rules above; before every destructive step Apply
// re-verifies that the link is STILL the exact link the plan decided about
// (same path, still a symlink, same target). A link swapped between plan
// and apply therefore refuses to be touched. Re-running full classification
// here would be wrong: applying one action (e.g. removing a hub link) may
// legitimately break the chains of later actions in the same plan.
func Apply(p *Plan, own Ownership) error {
	var errs []string
	for _, a := range p.Actions {
		var err error
		switch a.Op {
		case OpCreate:
			err = createLink(a.Path, a.Target)
		case OpFix:
			err = replaceLink(a.Path, a.Target, a.Old)
		case OpRemove:
			err = removeLink(a.Path, a.Old)
		}
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("apply: %s", strings.Join(errs, "; "))
	}
	return nil
}

func createLink(path, target string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(target, path); err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	return nil
}

// verifyUnchanged confirms path is still a symlink with the exact target
// the plan recorded.
func verifyUnchanged(path, plannedTarget string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("refusing to touch %s: %w", path, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("refusing to touch %s: no longer a symlink", path)
	}
	current, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("refusing to touch %s: %w", path, err)
	}
	if current != plannedTarget {
		return fmt.Errorf("refusing to touch %s: target changed since planning (%s, was %s)",
			path, current, plannedTarget)
	}
	return nil
}

func replaceLink(path, target, plannedOld string) error {
	if err := verifyUnchanged(path, plannedOld); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return createLink(path, target)
}

func removeLink(path, plannedOld string) error {
	if err := verifyUnchanged(path, plannedOld); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
