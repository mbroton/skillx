// Package config loads and saves skillx's per-machine configuration.
//
// The config lives at $XDG_CONFIG_HOME/skillx/config.toml (defaulting to
// ~/.config/skillx/config.toml). It records where the skills repo is cloned,
// where the hub directory lives, and which agents this machine knows about.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/mbroton/skillx/internal/fsutil"
)

// Agent describes one agent integration (a spoke).
type Agent struct {
	// Path is the agent's global skills directory, e.g. "~/.claude/skills".
	Path string `toml:"path"`
	// Disabled marks the agent as ignored on this machine (per-machine override).
	Disabled bool `toml:"disabled,omitempty"`
}

// Config is the machine-local skillx configuration.
type Config struct {
	// Repo is the git URL of the private skills repository (source of truth).
	Repo string `toml:"repo"`
	// ClonePath is where that repo is cloned locally (the canonical editable copy).
	ClonePath string `toml:"clone_path"`
	// Hub is the neutral hub directory, default "~/.agents/skills".
	Hub string `toml:"hub"`
	// Agents maps agent name -> agent definition. Agents are config data, not code.
	Agents map[string]Agent `toml:"agents"`
}

// DefaultAgents returns the agents shipped by default.
func DefaultAgents() map[string]Agent {
	return map[string]Agent{
		"claude": {Path: "~/.claude/skills"},
		"codex":  {Path: "~/.codex/skills"},
	}
}

// DefaultHub is the default hub directory (unexpanded).
const DefaultHub = "~/.agents/skills"

// Path returns the config file path, respecting XDG_CONFIG_HOME.
func Path() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "~"
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "skillx", "config.toml")
}

// DefaultClonePath returns the default location for the skills repo clone,
// respecting XDG_DATA_HOME.
func DefaultClonePath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "~"
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "skillx", "repo")
}

// ErrNotFound indicates no config file exists yet (run `skillx init`).
var ErrNotFound = errors.New("skillx is not configured on this machine; run `skillx init`")

// Load reads the config file. Returns ErrNotFound when it does not exist.
func Load() (*Config, error) {
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path(), err)
	}
	if c.Hub == "" {
		c.Hub = DefaultHub
	}
	if c.Agents == nil {
		c.Agents = DefaultAgents()
	}
	return &c, nil
}

// Save writes the config file, creating parent directories as needed.
func (c *Config) Save() error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// ExpandedClonePath returns ClonePath with "~" expanded.
func (c *Config) ExpandedClonePath() string { return fsutil.ExpandPath(c.ClonePath) }

// ExpandedHub returns Hub with "~" expanded.
func (c *Config) ExpandedHub() string { return fsutil.ExpandPath(c.Hub) }

// SkillsDir returns the canonical skills directory inside the clone.
func (c *Config) SkillsDir() string { return filepath.Join(c.ExpandedClonePath(), "skills") }

// EnabledAgents returns non-disabled agents sorted by name.
func (c *Config) EnabledAgents() []NamedAgent {
	var out []NamedAgent
	for name, a := range c.Agents {
		if a.Disabled {
			continue
		}
		out = append(out, NamedAgent{Name: name, Agent: a})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AgentNames returns the names of all enabled agents, sorted.
func (c *Config) AgentNames() []string {
	agents := c.EnabledAgents()
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name)
	}
	return names
}

// NamedAgent pairs an agent with its config name.
type NamedAgent struct {
	Name string
	Agent
}

// ExpandedPath returns the agent's skills dir with "~" expanded.
func (a NamedAgent) ExpandedPath() string { return fsutil.ExpandPath(a.Path) }

// LocalDir returns the agent's skills dir relative to a project root,
// e.g. "<project>/.claude/skills" for agent "claude".
func (a NamedAgent) LocalDir(projectRoot string) string {
	return filepath.Join(projectRoot, "."+a.Name, "skills")
}
