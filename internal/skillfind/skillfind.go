// Package skillfind discovers agent skills inside a directory tree. A skill
// is any directory containing a SKILL.md file. Discovery looks at the root
// itself, the root's direct children, and children of a "skills/" directory.
package skillfind

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MarkerFile identifies a skill directory.
const MarkerFile = "SKILL.md"

// Skill is a discovered skill: its name (directory base name) and location.
type Skill struct {
	Name string
	Dir  string
}

// IsSkillDir reports whether dir directly contains a SKILL.md file.
func IsSkillDir(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, MarkerFile))
	return err == nil && st.Mode().IsRegular()
}

// Discover finds skills under root. It checks, in order:
//
//  1. root itself (a repo that is a single skill),
//  2. direct children of root,
//  3. direct children of root/skills.
//
// Results are unique by name (first hit wins) and sorted by name.
func Discover(root string) ([]Skill, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	seen := map[string]bool{}
	var out []Skill
	add := func(dir string) {
		name := filepath.Base(dir)
		if seen[name] {
			return
		}
		seen[name] = true
		out = append(out, Skill{Name: name, Dir: dir})
	}

	if IsSkillDir(root) {
		abs, err := filepath.Abs(root)
		if err != nil {
			abs = root
		}
		add(abs)
		return out, nil
	}

	scan := func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sub := filepath.Join(dir, e.Name())
			if IsSkillDir(sub) {
				add(sub)
			}
		}
		return nil
	}

	if err := scan(root); err != nil {
		return nil, err
	}
	if err := scan(filepath.Join(root, "skills")); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Filter keeps only the named skills, erroring on names that were not found.
func Filter(skills []Skill, names []string) ([]Skill, error) {
	if len(names) == 0 {
		return skills, nil
	}
	byName := map[string]Skill{}
	for _, s := range skills {
		byName[s.Name] = s
	}
	var out []Skill
	for _, n := range names {
		s, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("skill %q not found in source (available: %s)", n, namesOf(skills))
		}
		out = append(out, s)
	}
	return out, nil
}

func namesOf(skills []Skill) string {
	if len(skills) == 0 {
		return "none"
	}
	names := make([]string, len(skills))
	for i, sk := range skills {
		names[i] = sk.Name
	}
	return strings.Join(names, ", ")
}
