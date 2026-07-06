package linker

import (
	"os"
	"path/filepath"
	"sort"
)

// GlobalSpec describes the desired global hub-and-spoke state in plain paths:
//
//	canonical: <SkillsDir>/<name>          (real files, inside the clone)
//	hub:       <Hub>/<name>                -> canonical (absolute symlink)
//	spoke:     <AgentDirs[a]>/<name>       -> hub entry (absolute symlink)
type GlobalSpec struct {
	// SkillsDir is <clone>/skills.
	SkillsDir string
	// Hub is the neutral hub directory, e.g. ~/.agents/skills.
	Hub string
	// AgentDirs maps agent name -> that agent's global skills directory.
	AgentDirs map[string]string
	// Skills lists the skill names to link (hub entry + a spoke into every
	// agent dir). There is no per-agent mapping: the filesystem is the
	// database, and callers choose which agents by shaping AgentDirs.
	Skills []string
}

// Desired expands the spec into concrete links: a hub link per skill plus
// a spoke per agent dir.
func (g GlobalSpec) Desired() []Desired {
	var out []Desired
	names := append([]string(nil), g.Skills...)
	sort.Strings(names)
	agents := make([]string, 0, len(g.AgentDirs))
	for a := range g.AgentDirs {
		agents = append(agents, a)
	}
	sort.Strings(agents)
	for _, name := range names {
		canonical := filepath.Join(g.SkillsDir, name)
		missing := !dirExists(canonical)
		hubLink := filepath.Join(g.Hub, name)
		out = append(out, Desired{Path: hubLink, Target: canonical, SourceMissing: missing})
		for _, agent := range agents {
			out = append(out, Desired{
				Path:          filepath.Join(g.AgentDirs[agent], name),
				Target:        hubLink,
				SourceMissing: missing,
			})
		}
	}
	return out
}

// ManagedDirs returns the directories the global spec owns entries in.
func (g GlobalSpec) ManagedDirs() []string {
	dirs := []string{g.Hub}
	names := make([]string, 0, len(g.AgentDirs))
	for n := range g.AgentDirs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		dirs = append(dirs, g.AgentDirs[n])
	}
	return dirs
}

// Ownership returns the ownership domain for the global spec:
// resolution-based on the clone's skills dir only. The hub is deliberately
// NOT a root — it can contain real skill directories managed by other tools
// (e.g. `npx skills`), and links resolving to those are foreign. The bag's
// skill names help attribute broken links (owned-dangling vs unknown).
func (g GlobalSpec) Ownership() Ownership {
	return NewOwnership(g.SkillsDir).WithManifestNames(g.Skills)
}

// LocalSpec describes a project-scope layout:
//
//	hub:   <ProjectRoot>/.agents/skills/<name>   (REAL copied files)
//	spoke: <ProjectRoot>/.<agent>/skills/<name>  -> ../../.agents/skills/<name>
//
// Spokes are relative symlinks so they can be committed and work for
// collaborators who check the project out elsewhere.
type LocalSpec struct {
	ProjectRoot string
	// Agents maps agent name -> skills dir relative to the project root
	// (e.g. "claude" -> ".claude/skills").
	Agents map[string]string
	// Skills lists the skill names present in the local hub. When nil, the
	// hub directory is scanned.
	Skills []string
}

// LocalHubDir returns <ProjectRoot>/.agents/skills.
func (l LocalSpec) LocalHubDir() string {
	return filepath.Join(l.ProjectRoot, ".agents", "skills")
}

// Desired expands the local spec into relative spoke links for every skill
// in the local hub, for every agent.
func (l LocalSpec) Desired() ([]Desired, error) {
	skills := l.Skills
	if skills == nil {
		entries, err := os.ReadDir(l.LocalHubDir())
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				skills = append(skills, e.Name())
			}
		}
	}
	sort.Strings(skills)

	agentNames := make([]string, 0, len(l.Agents))
	for n := range l.Agents {
		agentNames = append(agentNames, n)
	}
	sort.Strings(agentNames)

	var out []Desired
	for _, skill := range skills {
		hubEntry := filepath.Join(l.LocalHubDir(), skill)
		missing := !dirExists(hubEntry)
		for _, agent := range agentNames {
			linkPath := filepath.Join(l.ProjectRoot, l.Agents[agent], skill)
			rel, err := filepath.Rel(filepath.Dir(linkPath), hubEntry)
			if err != nil {
				return nil, err
			}
			out = append(out, Desired{Path: linkPath, Target: rel, SourceMissing: missing})
		}
	}
	return out, nil
}

// ManagedDirs returns the agent skills dirs inside the project.
func (l LocalSpec) ManagedDirs() []string {
	names := make([]string, 0, len(l.Agents))
	for n := range l.Agents {
		names = append(names, n)
	}
	sort.Strings(names)
	dirs := make([]string, 0, len(names))
	for _, n := range names {
		dirs = append(dirs, filepath.Join(l.ProjectRoot, l.Agents[n]))
	}
	return dirs
}

// Ownership for the local spec: pointer-based on the project's local hub.
// Unlike global scope, the local hub contains real files by design and IS
// the canonical source, so any link pointing into it is legitimately
// managed here — including broken ones (their hub entry was deleted), which
// plain `skillx use --local` may clean up.
func (l LocalSpec) Ownership() Ownership {
	return NewPointerOwnership(l.LocalHubDir())
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
