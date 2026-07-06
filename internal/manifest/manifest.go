// Package manifest reads and writes skillx.toml, the portable, git-tracked
// manifest at the root of the skills repository (the "bag").
//
// The manifest records PROVENANCE ONLY: where vendored third-party skills
// came from, so `skillx update` can re-vendor them. It is not an inventory —
// the bag's inventory is the skills/ directory listing itself — and it says
// nothing about which machines or agents use a skill (the filesystem is the
// database for that). The user's own skills need no entry at all.
//
// Legacy manifests may contain an `agents` field from an earlier design;
// it is ignored.
package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// FileName is the manifest file name at the root of the skills repo.
const FileName = "skillx.toml"

// Skill is one provenance entry.
type Skill struct {
	// Source is the origin repo for vendored third-party skills,
	// e.g. "github.com/someone/repo".
	Source string `toml:"source,omitempty"`
	// Path is the skill's path inside the source repo.
	Path string `toml:"path,omitempty"`
	// Branch is the branch the skill was vendored from (empty: the source
	// repo's default branch).
	Branch string `toml:"branch,omitempty"`
	// Commit is the source commit the skill was vendored at.
	Commit string `toml:"commit,omitempty"`
	// URL is the clone URL, recorded only when it cannot be derived from
	// Source (e.g. SSH remotes, so re-vendoring keeps the same transport).
	URL string `toml:"url,omitempty"`
}

// CloneURL returns the URL to clone the skill's source repo from: the
// recorded URL when present, otherwise one derived from Source.
func (s Skill) CloneURL() string {
	if s.URL != "" {
		return s.URL
	}
	return DeriveCloneURL(s.Source)
}

// DeriveCloneURL turns a provenance source (e.g. "github.com/owner/repo")
// into an https clone URL. Sources that already carry a scheme are returned
// as-is.
func DeriveCloneURL(source string) string {
	if strings.Contains(source, "://") {
		return source
	}
	return "https://" + source + ".git"
}

// Vendored reports whether the skill was vendored from a third-party repo.
func (s Skill) Vendored() bool { return s.Source != "" }

// Manifest is the full skillx.toml contents.
type Manifest struct {
	Skills map[string]Skill `toml:"skills"`
}

// New returns an empty manifest.
func New() *Manifest { return &Manifest{Skills: map[string]Skill{}} }

// PathIn returns the manifest path inside a skills repo clone.
func PathIn(cloneDir string) string { return filepath.Join(cloneDir, FileName) }

// Load reads the manifest from a skills repo clone. A missing file yields an
// empty manifest and no error. Unknown fields (e.g. the legacy `agents`
// list) are ignored.
func Load(cloneDir string) (*Manifest, error) {
	data, err := os.ReadFile(PathIn(cloneDir))
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, err
	}
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", PathIn(cloneDir), err)
	}
	if m.Skills == nil {
		m.Skills = map[string]Skill{}
	}
	return &m, nil
}

// Save writes the manifest into a skills repo clone. Entries without any
// provenance are dropped — they carry no information.
func (m *Manifest) Save(cloneDir string) error {
	for name, s := range m.Skills {
		if !s.Vendored() {
			delete(m.Skills, name)
		}
	}
	data, err := toml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(PathIn(cloneDir), data, 0o644)
}

// Names returns the names of skills with provenance entries, sorted.
func (m *Manifest) Names() []string {
	names := make([]string, 0, len(m.Skills))
	for n := range m.Skills {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
