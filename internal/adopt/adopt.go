// Package adopt detects skills installed by other tools (e.g. `npx skills`)
// so skillx can migrate them into the user's skills repo.
//
// Two shapes are detected in global scope:
//
//  1. hub installs: REAL directories containing a SKILL.md directly in the
//     hub (~/.agents/skills/<name>), typically with relative spoke symlinks
//     in agent dirs pointing at them;
//  2. copied installs: real skill directories sitting directly in an
//     agent's skills dir.
//
// Detection never modifies anything; the adopt command does the moving.
package adopt

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/mbroton/skillx/internal/skillfind"
)

// Candidate is one adoptable skill found on this machine.
type Candidate struct {
	// Name is the skill (directory) name.
	Name string
	// Dir is where the real files live now (hub entry or agent dir entry).
	Dir string
	// FromHub is true for hub installs, false for copied installs.
	FromHub bool
	// Agents lists agent names derived from existing spoke links that point
	// at this hub entry (hub installs) or from which agent dirs hold a real
	// copy (copied installs). Empty when a hub install has no spokes; the
	// caller should fall back to all configured agents.
	Agents []string
	// ExtraCopies are additional real copies of a copied install in other
	// agent dirs. They are left in place and reported, never deleted.
	ExtraCopies []string
}

// Detect scans the hub and the agent dirs (name -> expanded path) for
// adoptable skills. Results are sorted by name; hub installs win name
// collisions with copied installs.
func Detect(hub string, agentDirs map[string]string) ([]Candidate, error) {
	agentNames := make([]string, 0, len(agentDirs))
	for n := range agentDirs {
		agentNames = append(agentNames, n)
	}
	sort.Strings(agentNames)

	byName := map[string]*Candidate{}

	// 1. Real skill directories in the hub.
	hubEntries, err := readDirIfExists(hub)
	if err != nil {
		return nil, err
	}
	for _, e := range hubEntries {
		path := filepath.Join(hub, e.Name())
		if !isRealSkillDir(path) {
			continue
		}
		c := &Candidate{Name: e.Name(), Dir: path, FromHub: true}
		// Derive agents from spokes that resolve to this hub entry.
		resolvedHubEntry, err := filepath.EvalSymlinks(path)
		if err != nil {
			resolvedHubEntry = filepath.Clean(path)
		}
		for _, agent := range agentNames {
			spoke := filepath.Join(agentDirs[agent], e.Name())
			fi, err := os.Lstat(spoke)
			if err != nil || fi.Mode()&os.ModeSymlink == 0 {
				continue
			}
			resolved, err := filepath.EvalSymlinks(spoke)
			if err != nil {
				continue
			}
			if filepath.Clean(resolved) == resolvedHubEntry {
				c.Agents = append(c.Agents, agent)
			}
		}
		byName[c.Name] = c
	}

	// 2. Real skill directories sitting directly in agent dirs.
	for _, agent := range agentNames {
		dir := agentDirs[agent]
		entries, err := readDirIfExists(dir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			if !isRealSkillDir(path) {
				continue
			}
			if existing, ok := byName[e.Name()]; ok {
				if !existing.FromHub {
					existing.Agents = append(existing.Agents, agent)
					existing.ExtraCopies = append(existing.ExtraCopies, path)
				}
				// Hub install with the same name wins; the copy is not
				// recorded (`skillx status` reports it as a foreign entry).
				continue
			}
			byName[e.Name()] = &Candidate{
				Name:    e.Name(),
				Dir:     path,
				Agents:  []string{agent},
				FromHub: false,
			}
		}
	}

	out := make([]Candidate, 0, len(byName))
	for _, c := range byName {
		sort.Strings(c.Agents)
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ResolvedDirs returns the canonical (symlink-resolved) paths of every
// candidate skill directory, including extra copies. Used to recognize
// links that point at adoptable installs.
func ResolvedDirs(candidates []Candidate) map[string]bool {
	out := map[string]bool{}
	add := func(p string) {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			out[filepath.Clean(resolved)] = true
		} else {
			out[filepath.Clean(p)] = true
		}
	}
	for _, c := range candidates {
		add(c.Dir)
		for _, e := range c.ExtraCopies {
			add(e)
		}
	}
	return out
}

// IsInstallLink reports whether the symlink at link resolves to one of the
// candidate skill directories (see ResolvedDirs). Such links are spokes of
// an unmanaged install (e.g. `npx skills`) that `skillx adopt` can migrate —
// distinct from truly foreign links pointing elsewhere.
func IsInstallLink(link string, resolvedDirs map[string]bool) bool {
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		return false
	}
	return resolvedDirs[filepath.Clean(resolved)]
}

// isRealSkillDir reports whether path is a real (non-symlink) directory
// containing a SKILL.md.
func isRealSkillDir(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return false
	}
	return skillfind.IsSkillDir(path)
}

func readDirIfExists(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}
