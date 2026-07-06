// Package refparse parses skill source references accepted by `skillx add`:
//
//   - owner/repo                                  (GitHub shorthand)
//   - github.com/owner/repo[/tree/branch[/sub]]
//   - https://github.com/owner/repo[.git][/tree/branch[/sub]]
//   - git@github.com:owner/repo[.git]
//   - ssh://git@github.com/owner/repo[.git]
//   - a local filesystem path (absolute, ./relative, ~/..., or existing dir)
package refparse

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/mbroton/skillx/internal/fsutil"
)

// Kind distinguishes local paths from remote repositories.
type Kind int

const (
	// Remote is a git repository to clone.
	Remote Kind = iota
	// Local is a directory on this machine.
	Local
)

// Ref is a parsed skill source reference.
type Ref struct {
	Kind Kind
	// LocalPath is set for Kind == Local (expanded).
	LocalPath string
	// CloneURL is the URL to pass to `git clone` (Kind == Remote).
	CloneURL string
	// Source is the canonical provenance string, e.g. "github.com/owner/repo".
	Source string
	// Branch is the branch from a /tree/<branch> URL, if any.
	Branch string
	// SubPath is the path inside the repo from a /tree/<branch>/<subpath> URL.
	SubPath string
}

var ownerRepoRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*)/[A-Za-z0-9._-]+$`)

// Parse interprets a user-supplied reference. Local paths win when the string
// looks like a path (prefix) or names an existing directory; otherwise the
// string must parse as a repository reference.
func Parse(ref string) (*Ref, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("empty reference")
	}

	// Explicit local path prefixes.
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "./") ||
		strings.HasPrefix(ref, "../") || ref == "." || ref == ".." ||
		ref == "~" || strings.HasPrefix(ref, "~/") {
		return localRef(ref)
	}

	// scp-like ssh: git@host:owner/repo(.git)
	if m := scpRe.FindStringSubmatch(ref); m != nil {
		host, path := m[2], strings.TrimSuffix(m[3], ".git")
		return &Ref{
			Kind:     Remote,
			CloneURL: ref,
			Source:   host + "/" + path,
		}, nil
	}

	// file:// URLs: a "remote" on the local filesystem (bare repo on a
	// disk/NAS). Provenance keeps the full URL so re-vendoring works.
	if strings.HasPrefix(ref, "file://") {
		return &Ref{Kind: Remote, CloneURL: ref, Source: ref}, nil
	}

	// Full URLs (https://, ssh://, git://).
	if strings.Contains(ref, "://") {
		return parseURL(ref)
	}

	// host/owner/repo[/tree/...] without scheme, e.g. github.com/owner/repo.
	if firstSeg, _, ok := strings.Cut(ref, "/"); ok && strings.Contains(firstSeg, ".") {
		return parseURL("https://" + ref)
	}

	// owner/repo shorthand -> github.com. An existing local dir of the same
	// name wins, so "docs/guides" keeps working as a path.
	if ownerRepoRe.MatchString(ref) {
		if st, err := os.Stat(ref); err == nil && st.IsDir() {
			return localRef(ref)
		}
		return &Ref{
			Kind:     Remote,
			CloneURL: "https://github.com/" + ref + ".git",
			Source:   "github.com/" + ref,
		}, nil
	}

	// Last resort: an existing local directory (bare name).
	if st, err := os.Stat(ref); err == nil && st.IsDir() {
		return localRef(ref)
	}

	return nil, fmt.Errorf("cannot interpret %q: expected owner/repo, a git URL, or a local path", ref)
}

var scpRe = regexp.MustCompile(`^([A-Za-z0-9._-]+)@([A-Za-z0-9._-]+):(.+)$`)

func localRef(p string) (*Ref, error) {
	expanded := fsutil.ExpandPath(p)
	return &Ref{Kind: Local, LocalPath: expanded, Source: ""}, nil
}

func parseURL(raw string) (*Ref, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("cannot parse URL %q: %w", raw, err)
	}
	host := u.Host
	if host == "" {
		return nil, fmt.Errorf("URL %q has no host", raw)
	}
	segs := splitPath(u.Path)
	if len(segs) < 2 {
		return nil, fmt.Errorf("URL %q does not name a repository (need host/owner/repo)", raw)
	}
	owner, repo := segs[0], strings.TrimSuffix(segs[1], ".git")
	r := &Ref{
		Kind:   Remote,
		Source: host + "/" + owner + "/" + repo,
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	userInfo := ""
	if u.User != nil && u.User.String() != "" {
		userInfo = u.User.String() + "@"
	}
	r.CloneURL = fmt.Sprintf("%s://%s%s/%s/%s.git", scheme, userInfo, host, owner, repo)

	rest := segs[2:]
	if len(rest) > 0 {
		switch rest[0] {
		case "tree", "blob":
			if len(rest) < 2 {
				return nil, fmt.Errorf("URL %q has /%s/ but no branch", raw, rest[0])
			}
			// Note: branch names containing "/" are ambiguous in GitHub tree
			// URLs; the first segment after /tree/ is taken as the branch.
			r.Branch = rest[1]
			if len(rest) > 2 {
				r.SubPath = strings.Join(rest[2:], "/")
			}
		default:
			return nil, fmt.Errorf("URL %q has unsupported path after repo (expected /tree/<branch>/...)", raw)
		}
	}
	return r, nil
}

func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
