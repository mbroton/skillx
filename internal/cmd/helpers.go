package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mbroton/skillx/internal/config"
	"github.com/mbroton/skillx/internal/fsutil"
	"github.com/mbroton/skillx/internal/gitx"
	"github.com/mbroton/skillx/internal/linker"
	"github.com/mbroton/skillx/internal/manifest"
)

// loadConfigAndManifest loads the machine config and the manifest from the
// clone, verifying the clone exists.
func loadConfigAndManifest() (*config.Config, *manifest.Manifest, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	clone := cfg.ExpandedClonePath()
	if st, err := os.Stat(clone); err != nil || !st.IsDir() {
		return nil, nil, fmt.Errorf("skills repo clone not found at %s; run `skillx init`", clone)
	}
	m, err := manifest.Load(clone)
	if err != nil {
		return nil, nil, err
	}
	return cfg, m, nil
}

// loadConfigForLocal loads the machine config for local (project) scope
// operations. Local scope works even on a machine without `skillx init`:
// it falls back to the default agent set.
func loadConfigForLocal() (*config.Config, error) {
	cfg, err := config.Load()
	if errors.Is(err, config.ErrNotFound) {
		return &config.Config{Hub: config.DefaultHub, Agents: config.DefaultAgents()}, nil
	}
	return cfg, err
}

// agentDirs returns name -> expanded global skills dir for enabled agents,
// optionally limited to filter (error on unknown names).
func agentDirs(cfg *config.Config, filter []string) (map[string]string, error) {
	return filteredAgentDirs(cfg, filter, config.NamedAgent.ExpandedPath)
}

// localAgentDirs returns name -> project-relative skills dir (".claude/skills")
// for enabled agents, optionally limited to filter.
func localAgentDirs(cfg *config.Config, filter []string) (map[string]string, error) {
	return filteredAgentDirs(cfg, filter, func(a config.NamedAgent) string {
		return "." + a.Name + "/skills"
	})
}

func filteredAgentDirs(cfg *config.Config, filter []string,
	dir func(config.NamedAgent) string) (map[string]string, error) {

	dirs := map[string]string{}
	for _, a := range cfg.EnabledAgents() {
		dirs[a.Name] = dir(a)
	}
	if len(filter) == 0 {
		return dirs, nil
	}
	limited := map[string]string{}
	for _, name := range filter {
		d, ok := dirs[name]
		if !ok {
			return nil, fmt.Errorf("unknown or disabled agent %q (configured: %s)", name, joinNames(dirs))
		}
		limited[name] = d
	}
	return limited, nil
}

// sortedKeys returns the keys of m, sorted.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func joinNames[V any](m map[string]V) string {
	if len(m) == 0 {
		return "none"
	}
	return strings.Join(sortedKeys(m), ", ")
}

// bagInventory lists the skills in the bag: the directory names under
// <clone>/skills. The filesystem is the database — the manifest is only
// consulted for provenance, never for inventory.
func bagInventory(cfg *config.Config) ([]string, error) {
	entries, err := os.ReadDir(cfg.SkillsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// globalSpec builds the linker spec for the given skill names and agents.
func globalSpec(cfg *config.Config, skills []string, agentFilter []string) (linker.GlobalSpec, error) {
	dirs, err := agentDirs(cfg, agentFilter)
	if err != nil {
		return linker.GlobalSpec{}, err
	}
	return linker.GlobalSpec{
		SkillsDir: cfg.SkillsDir(),
		Hub:       cfg.ExpandedHub(),
		AgentDirs: dirs,
		Skills:    skills,
	}, nil
}

// bagOwnership returns the ownership domain for this machine, with the full
// bag inventory as attribution names for broken links.
func bagOwnership(cfg *config.Config) (linker.Ownership, error) {
	inv, err := bagInventory(cfg)
	if err != nil {
		return linker.Ownership{}, err
	}
	return linker.NewOwnership(cfg.SkillsDir()).WithManifestNames(inv), nil
}

// installedSet reports which bag skills are installed on this machine: the
// hub link exists and resolves into the clone. Read live from disk.
func installedSet(cfg *config.Config, inventory []string) (map[string]bool, error) {
	own, err := bagOwnership(cfg)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	hub := cfg.ExpandedHub()
	for _, name := range inventory {
		if own.Classify(filepath.Join(hub, name)) == linker.ClassOwned {
			out[name] = true
		}
	}
	return out, nil
}

// spokeAgents lists the enabled agents whose spoke link for a skill is
// owned (resolves into the clone).
func spokeAgents(cfg *config.Config, own linker.Ownership, name string) []string {
	var out []string
	for _, a := range cfg.EnabledAgents() {
		if own.Classify(filepath.Join(a.ExpandedPath(), name)) == linker.ClassOwned {
			out = append(out, a.Name)
		}
	}
	return out
}

// installSkills creates hub + spoke links for the named skills (targeted:
// never prunes anything else). Returns the number of links created/fixed.
func installSkills(cfg *config.Config, names []string, agentFilter []string) (int, error) {
	if len(names) == 0 {
		return 0, nil
	}
	spec, err := globalSpec(cfg, names, agentFilter)
	if err != nil {
		return 0, err
	}
	plan, err := linker.Compute(linker.Input{
		Desired:   spec.Desired(),
		Ownership: spec.Ownership(),
		// No ManagedDirs: installing named skills must not touch others.
	})
	if err != nil {
		return 0, err
	}
	return finishActions(plan, spec.Ownership())
}

// installHubLinks creates only the hub links for the named skills (used by
// --copy mode, where spoke locations hold real copied files).
func installHubLinks(cfg *config.Config, names []string) (int, error) {
	spec := linker.GlobalSpec{
		SkillsDir: cfg.SkillsDir(),
		Hub:       cfg.ExpandedHub(),
		Skills:    names,
	}
	plan, err := linker.Compute(linker.Input{
		Desired:   spec.Desired(),
		Ownership: spec.Ownership(),
	})
	if err != nil {
		return 0, err
	}
	return finishActions(plan, spec.Ownership())
}

// unlinkSkills removes the owned links of the named skills. With an empty
// agentFilter it removes the hub link and every spoke. With a filter it
// removes only those agents' spokes, then drops the hub link as well when
// no enabled agent's spoke remains — a hub link nothing points at serves
// nobody. Returns the number of links removed.
func unlinkSkills(cfg *config.Config, names []string, reason string, agentFilter []string) (int, error) {
	if len(names) == 0 {
		return 0, nil
	}
	own, err := bagOwnership(cfg)
	if err != nil {
		return 0, err
	}
	dirs, err := agentDirs(cfg, agentFilter)
	if err != nil {
		return 0, err
	}
	hub := cfg.ExpandedHub()
	var paths []string
	for _, name := range names {
		if len(agentFilter) == 0 {
			paths = append(paths, filepath.Join(hub, name))
		}
		for _, dir := range dirs {
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	plan := linker.RemovalsFor(paths, own, reason)
	removed, err := finishActions(plan, own)
	if err != nil || len(agentFilter) == 0 {
		return removed, err
	}

	allDirs, err := agentDirs(cfg, nil)
	if err != nil {
		return removed, err
	}
	var hubPaths []string
	for _, name := range names {
		remaining := false
		for _, dir := range allDirs {
			if own.Classify(filepath.Join(dir, name)) == linker.ClassOwned {
				remaining = true
				break
			}
		}
		if !remaining {
			hubPaths = append(hubPaths, filepath.Join(hub, name))
		}
	}
	if len(hubPaths) == 0 {
		return removed, nil
	}
	hubPlan := linker.RemovalsFor(hubPaths, own, "no agent uses it anymore")
	n, err := finishActions(hubPlan, own)
	return removed + n, err
}

// renderAction formats a planned action like linker.Action.String, but with
// home-relative (~) paths for readability.
func renderAction(a linker.Action) string {
	c := fsutil.ContractPath
	switch a.Op {
	case linker.OpCreate:
		return fmt.Sprintf("create %s -> %s", c(a.Path), c(a.Target))
	case linker.OpFix:
		return fmt.Sprintf("fix    %s -> %s (was %s)", c(a.Path), c(a.Target), c(a.Old))
	case linker.OpRemove:
		return fmt.Sprintf("remove %s (-> %s; %s)", c(a.Path), c(a.Old), a.Reason)
	}
	return a.String()
}

// renderIssue is renderAction's counterpart for issues.
func renderIssue(is linker.Issue) string {
	return fmt.Sprintf("%s: %s (%s)", is.Kind, fsutil.ContractPath(is.Path), is.Detail)
}

// finishActions prints and applies a plan's actions and non-noise issues.
func finishActions(plan *linker.Plan, own linker.Ownership) (int, error) {
	for _, a := range plan.Actions {
		fmt.Println("  " + renderAction(a))
	}
	if err := linker.Apply(plan, own); err != nil {
		return 0, err
	}
	for _, is := range plan.Issues {
		if is.Kind == linker.IssueForeignEntry {
			continue // e.g. --copy output or user files; status reports these
		}
		fmt.Printf("  note: %s\n", renderIssue(is))
	}
	return len(plan.Actions), nil
}

// copyMarker is dropped into directories created by `--copy` so a later run
// can tell its own output apart from skill directories installed by other
// tools (which are never overwritten).
const copyMarker = ".skillx-copy"

func writeCopyMarker(dir string) error {
	return os.WriteFile(filepath.Join(dir, copyMarker),
		[]byte("created by `skillx use --copy`; skillx may replace this directory on refresh\n"), 0o644)
}

func hasCopyMarker(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, copyMarker))
	return err == nil && st.Mode().IsRegular()
}

// projectRoot finds the current project root: the enclosing git repo's top
// level, or the working directory when not in a repo.
func projectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if top := gitx.TopLevel(cwd); top != "" {
		return top, nil
	}
	return cwd, nil
}

// finishPlan prints and applies a computed plan. Used by local (project)
// scope, where output stays small.
func finishPlan(plan *linker.Plan, own linker.Ownership) error {
	if len(plan.Actions) == 0 {
		fmt.Printf("Everything in place (%d links ok).\n", plan.OK)
	} else {
		for _, a := range plan.Actions {
			fmt.Println("  " + renderAction(a))
		}
		if err := linker.Apply(plan, own); err != nil {
			return err
		}
	}
	for _, is := range plan.Issues {
		if is.Kind == linker.IssueForeignEntry {
			continue // real files in a project are normal; nothing to report
		}
		fmt.Printf("  note: %s\n", renderIssue(is))
	}
	return nil
}
